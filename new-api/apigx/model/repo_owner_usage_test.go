package apigxmodel

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeOwnerUsageCombinesAttemptsAndEffectiveUsage(t *testing.T) {
	db := newRepoDB(t)
	const (
		owner      int64 = 8101
		otherOwner int64 = 8102
		now        int64 = 1_800_000_000
	)
	_, channel := seedGroupAndChannel(t, db, owner)
	key := &UpstreamKey{ChannelID: channel.ID, StableKeyID: "owner-usage-key", Name: "主 Key", Status: UpstreamKeyStatusEnabled}
	require.NoError(t, db.Create(key).Error)

	request := &Request{RequestUID: "owner-usage-request", ApiModelID: "shared-model", CreatedAt: now}
	require.NoError(t, db.Create(request).Error)
	require.NoError(t, db.Create(&Attempt{
		AttemptUID:        "owner-usage-attempt-0",
		RequestID:         request.ID,
		AttemptIndex:      0,
		SelectedChannelID: channel.ID,
		UpstreamKeyID:     key.ID,
		UpstreamModelID:   "provider-model",
		State:             AttemptStateFailed,
		HttpStatus:        429,
		StartedAt:         now,
		TtftMs:            80,
	}).Error)
	require.NoError(t, db.Create(&Attempt{
		AttemptUID:        "owner-usage-attempt-1",
		RequestID:         request.ID,
		AttemptIndex:      1,
		SelectedChannelID: channel.ID,
		UpstreamKeyID:     key.ID,
		UpstreamModelID:   "provider-model",
		State:             AttemptStateSucceeded,
		HttpStatus:        200,
		StartedAt:         now + 1,
		TtftMs:            120,
	}).Error)

	original := ownerUsageEvent("owner-usage-original", owner, channel.ID, key.ID, "shared-model", CapabilityChat,
		UsageDimensionInputTokens, 100_000, UsageTrustLocalEstimated, UsageCostStateReferenceEstimated, 100, now)
	require.NoError(t, RecordUsageEvent(db, original))
	firstCorrection := &UsageEvent{
		EventUID:      "owner-usage-correction-1",
		QuantityMilli: 120_000,
		Trust:         UsageTrustUpstreamReported,
		CostState:     UsageCostStateReferenceEstimated,
		CostUsdMicro:  120,
		OccurredAt:    now + 500, // 必须被根事件时间覆盖，仍归入原使用窗口。
		OwnerUserID:   otherOwner,
	}
	require.NoError(t, AppendUsageCorrection(db, original.ID, firstCorrection))
	latestCorrection := &UsageEvent{
		EventUID:      "owner-usage-correction-2",
		QuantityMilli: 130_000,
		Trust:         UsageTrustUpstreamReported,
		CostState:     UsageCostStateReferenceEstimated,
		CostUsdMicro:  130,
	}
	require.NoError(t, AppendUsageCorrection(db, firstCorrection.ID, latestCorrection))
	assert.EqualValues(t, original.ID, latestCorrection.CorrectionOfEventID, "连续校正应统一指回根事件")
	assert.EqualValues(t, owner, latestCorrection.OwnerUserID, "校正不能改变分享者归属")
	assert.EqualValues(t, now, latestCorrection.OccurredAt, "校正不能移动原事件时间")

	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-usage-unpriced", owner, channel.ID, key.ID, "shared-model",
		CapabilityChat, UsageDimensionOutputTokens, 50_000, UsageTrustUpstreamReported, UsageCostStateUnpriced, 0, now)))
	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-usage-pending-fx", owner, channel.ID, key.ID, "shared-model",
		CapabilityAudioRecognition, UsageDimensionInputAudioSeconds, 3_500, UsageTrustLocalEstimated, UsageCostStatePendingFx, 0, now)))
	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-usage-zero", owner, channel.ID, key.ID, "shared-model",
		CapabilityImageGeneration, UsageDimensionImageCount, 1_000, UsageTrustLocalEstimated, UsageCostStateExplicitZero, 0, now)))
	// 别人的事件不得进入当前分享者汇总。
	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-usage-other-owner", otherOwner, channel.ID, key.ID, "shared-model",
		CapabilityChat, UsageDimensionInputTokens, 9_999_000, UsageTrustUpstreamReported, UsageCostStateReferenceEstimated, 9_999, now)))

	items, hasMore, err := SummarizeOwnerUsage(db, owner, OwnerUsageByChannel, now-10, now+10, 1, 20)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, items, 1)
	got := items[0]
	assert.EqualValues(t, channel.ID, got.GroupID)
	assert.Equal(t, channel.Name, got.Name)
	assert.EqualValues(t, 1, got.RequestCount)
	assert.EqualValues(t, 2, got.AttemptCount)
	assert.EqualValues(t, 1, got.RetryCount)
	assert.EqualValues(t, 1, got.SucceededAttemptCount)
	assert.EqualValues(t, 1, got.FailedAttemptCount)
	assert.Zero(t, got.UnfinishedAttemptCount)
	assert.EqualValues(t, 200, got.TtftMsSum)
	assert.EqualValues(t, 2, got.TtftSamples)
	assert.EqualValues(t, 130, got.EstimatedCostUSDMicro, "原事件与旧校正不得和最新校正重复相加")
	assert.EqualValues(t, 1, got.EstimatedCostEventCount)
	assert.EqualValues(t, 1, got.UnpricedEventCount)
	assert.EqualValues(t, 1, got.PendingFXEventCount)
	assert.EqualValues(t, 1, got.ExplicitZeroEventCount)

	quantities := make(map[string]OwnerUsageQuantity, len(got.Usage))
	for _, quantity := range got.Usage {
		quantities[fmt.Sprintf("%d:%d", quantity.Capability, quantity.Dimension)] = quantity
	}
	input := quantities[fmt.Sprintf("%d:%d", CapabilityChat, UsageDimensionInputTokens)]
	assert.EqualValues(t, 130_000, input.QuantityMilli)
	assert.EqualValues(t, 130_000, input.UpstreamReportedMilli)
	assert.Equal(t, "token", input.Unit)
	audio := quantities[fmt.Sprintf("%d:%d", CapabilityAudioRecognition, UsageDimensionInputAudioSeconds)]
	assert.EqualValues(t, 3_500, audio.QuantityMilli)
	assert.Equal(t, "second", audio.Unit)

	byKey, _, err := SummarizeOwnerUsage(db, owner, OwnerUsageByKey, now-10, now+10, 1, 20)
	require.NoError(t, err)
	require.Len(t, byKey, 1)
	assert.Equal(t, "主 Key", byKey[0].Name)
	assert.NotContains(t, byKey[0].Name, "SecretRef")

	byModel, _, err := SummarizeOwnerUsage(db, owner, OwnerUsageByModel, now-10, now+10, 1, 20)
	require.NoError(t, err)
	require.Len(t, byModel, 1)
	assert.Equal(t, "shared-model", byModel[0].GroupKey)
	assert.EqualValues(t, 2, byModel[0].AttemptCount, "模型维度应使用请求的 API 模型，而非上游模型名")
}

func TestSummarizeOwnerUsagePaginationAndTimeRange(t *testing.T) {
	db := newRepoDB(t)
	const (
		owner int64 = 8201
		now   int64 = 1_800_100_000
	)
	_, first := seedGroupAndChannel(t, db, owner)
	_, second := seedGroupAndChannel(t, db, owner)
	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-page-first", owner, first.ID, 0, "m1",
		CapabilityChat, UsageDimensionRequestCount, 1_000, UsageTrustLocalEstimated, UsageCostStateUnpriced, 0, now)))
	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-page-second", owner, second.ID, 0, "m2",
		CapabilityChat, UsageDimensionRequestCount, 1_000, UsageTrustLocalEstimated, UsageCostStateUnpriced, 0, now)))
	// 窗口外数据既不产生行，也不影响 has_more。
	_, oldChannel := seedGroupAndChannel(t, db, owner)
	require.NoError(t, RecordUsageEvent(db, ownerUsageEvent("owner-page-old", owner, oldChannel.ID, 0, "old",
		CapabilityChat, UsageDimensionRequestCount, 1_000, UsageTrustLocalEstimated, UsageCostStateUnpriced, 0, now-10_000)))

	page1, more, err := SummarizeOwnerUsage(db, owner, OwnerUsageByChannel, now-5, now+5, 1, 1)
	require.NoError(t, err)
	require.True(t, more)
	require.Len(t, page1, 1)
	assert.EqualValues(t, first.ID, page1[0].GroupID)

	page2, more, err := SummarizeOwnerUsage(db, owner, OwnerUsageByChannel, now-5, now+5, 2, 1)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, page2, 1)
	assert.EqualValues(t, second.ID, page2[0].GroupID)

	_, _, err = SummarizeOwnerUsage(db, owner, OwnerUsageByChannel, now+1, now, 1, 20)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = SummarizeOwnerUsage(db, owner, OwnerUsageByChannel, now-5, now+5, 1, 101)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func ownerUsageEvent(uid string, owner, channelID, keyID int64, modelID string, capability, dimension int,
	quantity int64, trust, costState int, usdCost, occurredAt int64) *UsageEvent {
	return &UsageEvent{
		EventUID:      uid,
		Kind:          UsageEventKindUsage,
		OwnerUserID:   owner,
		ApiModelID:    modelID,
		ChannelID:     channelID,
		UpstreamKeyID: keyID,
		Capability:    capability,
		Dimension:     dimension,
		QuantityMilli: quantity,
		UnitScale:     UsageUnitScalePerRequest,
		Trust:         trust,
		CostState:     costState,
		CostUsdMicro:  usdCost,
		OccurredAt:    occurredAt,
		CreatedAt:     occurredAt,
	}
}
