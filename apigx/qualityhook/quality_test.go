package qualityhook

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 归因的核心要求（计划书验收：「6 小时窗口内有错误、取消和少样本 | 归因正确」）：
// **平台自身的拒绝不能被算成上游失败**，否则会误伤健康渠道。
func TestClassifyOutcomeSeparatesPlatformRejections(t *testing.T) {
	capacityErr := types.NewErrorWithStatusCode(errors.New("capacity exceeded"),
		types.ErrorCodeChannelNoAvailableKey, http.StatusTooManyRequests)
	assert.Equal(t, apigxmodel.QualityOutcomeRejected, ClassifyOutcome(capacityErr, http.StatusTooManyRequests),
		"容量拒绝发生在我们这里，不是上游质量问题")

	sourceErr := types.NewErrorWithStatusCode(errors.New("source not allowed"),
		types.ErrorCodeChannelNoAvailableKey, http.StatusForbidden)
	assert.Equal(t, apigxmodel.QualityOutcomeRejected, ClassifyOutcome(sourceErr, http.StatusForbidden))

	quotaErr := types.NewErrorWithStatusCode(errors.New("insufficient quota"),
		types.ErrorCodeInsufficientUserQuota, http.StatusForbidden)
	assert.Equal(t, apigxmodel.QualityOutcomeRejected, ClassifyOutcome(quotaErr, http.StatusForbidden))
}

// 成功、上游失败、超时、取消四类如实区分。
func TestClassifyOutcomeBasicCases(t *testing.T) {
	assert.Equal(t, apigxmodel.QualityOutcomeSuccess, ClassifyOutcome(nil, 0))

	upstream := types.NewErrorWithStatusCode(errors.New("upstream 500"),
		types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	assert.Equal(t, apigxmodel.QualityOutcomeUpstream, ClassifyOutcome(upstream, http.StatusInternalServerError))

	// 504 按超时归因
	gatewayTimeout := types.NewErrorWithStatusCode(errors.New("gateway timeout"),
		types.ErrorCodeDoRequestFailed, http.StatusGatewayTimeout)
	assert.Equal(t, apigxmodel.QualityOutcomeTimeout, ClassifyOutcome(gatewayTimeout, http.StatusGatewayTimeout))

	// context deadline 也按超时
	deadline := types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, 0)
	assert.Equal(t, apigxmodel.QualityOutcomeTimeout, ClassifyOutcome(deadline, 0))

	// 客户端取消：单独归因（既不是上游问题，也不是质量问题）
	cancelled := types.NewErrorWithStatusCode(context.Canceled, types.ErrorCodeDoRequestFailed, 0)
	assert.Equal(t, apigxmodel.QualityOutcomeCancel, ClassifyOutcome(cancelled, 0))
}

// 默认关闭：未显式开启时不产生任何统计写入。
func TestQualityStatsDisabledByDefault(t *testing.T) {
	t.Setenv("APIGX_QUALITY_STATS_ENABLED", "")
	assert.False(t, Enabled(), "质量统计必须默认关闭（与其它 APIGX 开关一致）")

	t.Setenv("APIGX_QUALITY_STATS_ENABLED", "true")
	assert.True(t, Enabled())
}

func TestRecordFromRelayPersistsTextThroughput(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quality-hook.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))

	group := &apigxmodel.ShareGroup{OwnerUserID: 1001, Name: "quality", PublicID: "quality-group"}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	channel := &apigxmodel.Channel{
		OwnerUserID: 1001, ShareGroupID: group.ID, PublicID: "quality-channel",
		Name: "quality", BaseChannelID: 7001,
	}
	require.NoError(t, apigxmodel.CreateChannel(db, channel))
	offering := &apigxmodel.ModelOffering{ChannelID: channel.ID, UpstreamModelID: "quality-model"}
	require.NoError(t, apigxmodel.CreateModelOffering(db, 1001, offering))
	require.NoError(t, db.Model(&apigxmodel.ModelOffering{}).Where("id = ?", offering.ID).
		Update("status", apigxmodel.OfferingStatusPublished).Error)

	previousDB := channelpool.CurrentDB
	channelpool.SetDBProvider(func() *gorm.DB { return db })
	t.Cleanup(func() { channelpool.SetDBProvider(previousDB) })
	t.Setenv("APIGX_QUALITY_STATS_ENABLED", "true")

	start := time.Now().Add(-time.Second)
	RecordFromRelay(&relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 7001},
		OriginModelName: "quality-model", StartTime: start,
		FirstResponseTime: start.Add(200 * time.Millisecond), APIGXQualityOutputTokens: 80,
	}, nil)

	view, err := apigxmodel.ResolveQualityView(db, offering.ID, apigxmodel.QualityWindowSixHour, common.GetTimestamp())
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.SuccessCount)
	assert.EqualValues(t, 80, view.OutputTokensSum)
	assert.Greater(t, view.GenerationMsSum, int64(0))
}
