package relayhook

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// RecordSettlementUsage 在**结算之后**追加分享者的用量记录（#19 第一段）。
//
// 边界（刻意保守，宁缺勿假）：
//   - 只有拿到**真实 token 数**才写；两个维度都为 0 就**不写**（不制造"零用量"的假记录）；
//   - 成本相关字段一律按**未定价**写（cost_state=2、cost_usd_micro=0）—— 取价与计价是后续阶段的事，
//     本轮只接通"记录链路"，不碰底座扣费；
//   - 归属（渠道/供给/Key）从请求上下文解析，**解析不到就留 0**（快照如实反映"当时不知道"）；
//   - 任何失败只记日志，**绝不影响请求结果**。
//
// 幂等：event_uid 由请求 ID + 维度拼成 ⇒ 重试/重复结算不会产生重复事件（唯一约束兜底）。
func RecordSettlementUsage(c *gin.Context, relayInfo *relaycommon.RelayInfo, promptTokens, completionTokens int) {
	if c == nil || relayInfo == nil {
		return
	}
	if promptTokens <= 0 && completionTokens <= 0 {
		return
	}
	db := apigxmodel.DefaultDB()
	if db == nil {
		return
	}

	owner, channelID, offeringID, keyID := resolveUsageAttribution(db, c)
	now := common.GetTimestamp()

	write := func(dimension int, tokens int) {
		uid := "u:" + relayInfo.RequestId + ":" + strconv.Itoa(dimension)
		event := &apigxmodel.UsageEvent{
			EventUID:      uid,
			Kind:          apigxmodel.UsageEventKindUsage,
			UserID:        int64(relayInfo.UserId),
			OwnerUserID:   owner,
			ApiKeyID:      int64(relayInfo.TokenId),
			ApiModelID:    relayInfo.OriginModelName,
			OfferingID:    offeringID,
			ChannelID:     channelID,
			UpstreamKeyID: keyID,
			Dimension:     dimension,
			QuantityMilli: apigxmodel.UsageQuantityMilliFromTokens(tokens),
			UnitScale:     apigxmodel.UsageUnitScalePerMillionTokens,
			Trust:         apigxmodel.UsageTrustUpstreamReported,
			CostState:     apigxmodel.UsageCostStateUnpriced,
			OccurredAt:    now,
			CreatedAt:     now,
		}
		if err := apigxmodel.RecordUsageEvent(db, event); err != nil {
			common.SysError("relayhook: record usage event failed: " + err.Error())
		}
	}

	if promptTokens > 0 {
		write(apigxmodel.UsageDimensionInputTokens, promptTokens)
	}
	if completionTokens > 0 {
		write(apigxmodel.UsageDimensionOutputTokens, completionTokens)
	}
}

// resolveUsageAttribution 从上下文解析"这条用量归谁"：分享者、分享渠道、供给、上游 Key。
//
// 解析不到就返回 0 —— 快照如实反映"当时没记录到"，不猜、不填默认值。
func resolveUsageAttribution(db *gorm.DB, c *gin.Context) (owner, channelID, offeringID, keyID int64) {
	baseChannelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	if baseChannelID <= 0 {
		return 0, 0, 0, 0
	}
	var channel apigxmodel.Channel
	if err := db.Where("base_channel_id = ?", baseChannelID).First(&channel).Error; err != nil {
		return 0, 0, 0, 0
	}
	owner = channel.OwnerUserID
	channelID = channel.ID

	// 上游 Key：底座在请求上下文里给出多渠道索引，映射回我们这边的 Key
	if idx := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex); idx >= 0 {
		if identity, ok := channelpool.ResolveKeyIdentity(db, baseChannelID, idx); ok {
			keyID = identity.UpstreamKeyID
		}
	}
	// 供给：本轮不强求（上下文未必带），留 0 由快照如实表达
	return owner, channelID, 0, keyID
}
