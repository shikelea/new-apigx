package relayhook

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type UsageFact struct {
	Dimension     int
	QuantityMilli int64
	UnitScale     int
	Trust         int
}

type usageAttribution struct {
	ownerUserID       int64
	channelID         int64
	offeringID        int64
	upstreamKeyID     int64
	upstreamAccountID int64
	apiKeyID          int64
	canonicalModelID  int64
}

var usageDBProvider = func() *gorm.DB { return apigxmodel.DefaultDB() }

// RecordSettlementUsage 在底座结算完成后追加不可变的 APIGX 用量分项。
// 记录失败只进入系统日志，不改变已经完成的用户请求或底座扣费结果。
func RecordSettlementUsage(c *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, trust int) {
	if c == nil || relayInfo == nil || strings.TrimSpace(relayInfo.RequestId) == "" {
		return
	}
	db := usageDBProvider()
	if db == nil {
		return
	}
	facts := settlementUsageFacts(relayInfo, usage, trust)
	if len(facts) == 0 {
		return
	}

	attribution := resolveUsageAttribution(db, c, relayInfo)
	now := common.GetTimestamp()
	priceRuleID := int64(0)
	if rule, _ := apigxmodel.ResolveUsagePriceRule(db, attribution.upstreamKeyID, attribution.offeringID, attribution.channelID, now); rule != nil {
		priceRuleID = rule.ID
	}
	cycleKey := ""
	if value, ok := c.Get(admissionCycleKey); ok {
		cycleKey, _ = value.(string)
	}
	capability := usageCapability(relayInfo)
	for _, fact := range facts {
		cost := apigxmodel.ResolveUsageCostFromPriceRule(db, priceRuleID, fact.Dimension, fact.QuantityMilli, fact.UnitScale)
		event := &apigxmodel.UsageEvent{
			EventUID:          "u:" + relayInfo.RequestId + ":" + strconv.Itoa(capability) + ":" + strconv.Itoa(fact.Dimension),
			Kind:              apigxmodel.UsageEventKindUsage,
			UserID:            int64(relayInfo.UserId),
			OwnerUserID:       attribution.ownerUserID,
			ApiKeyID:          attribution.apiKeyID,
			CanonicalModelID:  attribution.canonicalModelID,
			ApiModelID:        relayInfo.OriginModelName,
			OfferingID:        attribution.offeringID,
			ChannelID:         attribution.channelID,
			UpstreamKeyID:     attribution.upstreamKeyID,
			UpstreamAccountID: attribution.upstreamAccountID,
			Capability:        capability,
			Dimension:         fact.Dimension,
			QuantityMilli:     fact.QuantityMilli,
			UnitScale:         cost.UnitScale,
			Trust:             fact.Trust,
			CycleKey:          cycleKey,
			PriceRuleID:       cost.PriceRuleID,
			PriceRuleItemID:   cost.PriceRuleItemID,
			UnitPriceMicro:    cost.UnitPriceMicro,
			Currency:          cost.Currency,
			FxRateMicro:       cost.FxRateMicro,
			CostMicro:         cost.CostMicro,
			CostUsdMicro:      cost.CostUsdMicro,
			CostState:         cost.CostState,
			OccurredAt:        now,
			CreatedAt:         now,
		}
		if err := apigxmodel.RecordUsageEvent(db, event); err != nil && !usageEventAlreadyExists(db, event.EventUID) {
			common.SysError("relayhook: record usage event failed: " + err.Error())
		}
	}
}

func SettlementUsageTrust(c *gin.Context, usage *dto.Usage) int {
	if usage == nil || c == nil || common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens) ||
		(usage.BillingUsage != nil && usage.BillingUsage.Estimated) {
		return apigxmodel.UsageTrustLocalEstimated
	}
	return apigxmodel.UsageTrustUpstreamReported
}

func settlementUsageFacts(relayInfo *relaycommon.RelayInfo, usage *dto.Usage, trust int) []UsageFact {
	if relayInfo == nil {
		return nil
	}
	if trust < apigxmodel.UsageTrustUpstreamReported || trust > apigxmodel.UsageTrustUnavailable {
		trust = apigxmodel.UsageTrustUnknown
	}
	facts := make([]UsageFact, 0, 7)
	capability := usageCapability(relayInfo)
	recordTokens := usage != nil
	if capability == apigxmodel.CapabilityImageGeneration || capability == apigxmodel.CapabilityImageEdit {
		recordTokens = recordTokens && relayInfo.APIGXImageUsageReported
	}
	if recordTokens {
		cacheRead := max(usage.PromptTokensDetails.CachedTokens, 0)
		cacheWrite := max(usage.PromptTokensDetails.CacheCreationTokensTotal(), 0)
		input := max(usage.PromptTokens, 0)
		if usage.UsageSemantic != dto.BillingUsageSemanticAnthropic {
			input = max(input-cacheRead-cacheWrite, 0)
		}
		reasoning := max(usage.CompletionTokenDetails.ReasoningTokens, 0)
		output := max(usage.CompletionTokens-reasoning, 0)
		facts = appendPositiveTokenFact(facts, apigxmodel.UsageDimensionInputTokens, input, trust)
		facts = appendPositiveTokenFact(facts, apigxmodel.UsageDimensionOutputTokens, output, trust)
		facts = appendPositiveTokenFact(facts, apigxmodel.UsageDimensionCacheReadTokens, cacheRead, trust)
		facts = appendPositiveTokenFact(facts, apigxmodel.UsageDimensionCacheWriteTokens, cacheWrite, trust)
		facts = appendPositiveTokenFact(facts, apigxmodel.UsageDimensionReasoningTokens, reasoning, trust)
	}

	if capability == apigxmodel.CapabilityImageGeneration || capability == apigxmodel.CapabilityImageEdit {
		count := relayInfo.RequestedImageCount()
		imageTrust := apigxmodel.UsageTrustLocalEstimated
		if relayInfo.BillingImageCount != nil && *relayInfo.BillingImageCount > 0 {
			count = *relayInfo.BillingImageCount
			imageTrust = apigxmodel.UsageTrustUpstreamReported
		}
		if quantity := apigxmodel.UsageQuantityMilliFromCount(int64(count)); quantity > 0 {
			facts = append(facts, UsageFact{
				Dimension: apigxmodel.UsageDimensionImageCount, QuantityMilli: quantity,
				UnitScale: apigxmodel.UsageUnitScalePerImage, Trust: imageTrust,
			})
		}
	}

	switch capability {
	case apigxmodel.CapabilityAudioRecognition:
		facts = appendAudioDurationFact(facts, apigxmodel.UsageDimensionInputAudioSeconds, relayInfo.APIGXInputAudioDurationMilli)
	case apigxmodel.CapabilityAudioGeneration:
		facts = appendAudioDurationFact(facts, apigxmodel.UsageDimensionOutputAudioSeconds, relayInfo.APIGXOutputAudioDurationMilli)
	}

	// 成功结算本身就是一条本地可观察事实。按次价依赖该维度，不能再借假 token 触发成本。
	facts = append(facts, UsageFact{
		Dimension: apigxmodel.UsageDimensionRequestCount, QuantityMilli: 1000,
		UnitScale: apigxmodel.UsageUnitScalePerRequest, Trust: apigxmodel.UsageTrustLocalEstimated,
	})
	return facts
}

func appendPositiveTokenFact(facts []UsageFact, dimension, tokens, trust int) []UsageFact {
	quantity := apigxmodel.UsageQuantityMilliFromTokens(tokens)
	if quantity == 0 {
		return facts
	}
	return append(facts, UsageFact{
		Dimension: dimension, QuantityMilli: quantity,
		UnitScale: apigxmodel.UsageUnitScalePerMillionTokens, Trust: trust,
	})
}

func appendAudioDurationFact(facts []UsageFact, dimension int, quantityMilli int64) []UsageFact {
	trust := apigxmodel.UsageTrustLocalEstimated
	if quantityMilli <= 0 {
		quantityMilli = 0
		trust = apigxmodel.UsageTrustUnavailable
	}
	return append(facts, UsageFact{
		Dimension: dimension, QuantityMilli: quantityMilli,
		UnitScale: apigxmodel.UsageUnitScalePerSecond, Trust: trust,
	})
}

func usageCapability(relayInfo *relaycommon.RelayInfo) int {
	if relayInfo == nil {
		return apigxmodel.CapabilityUnknown
	}
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeChatCompletions, relayconstant.RelayModeCompletions,
		relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact,
		relayconstant.RelayModeRealtime, relayconstant.RelayModeGemini,
		relayconstant.RelayModeAlphaSearch:
		return apigxmodel.CapabilityChat
	case relayconstant.RelayModeEmbeddings:
		return apigxmodel.CapabilityEmbedding
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeMidjourneyImagine:
		return apigxmodel.CapabilityImageGeneration
	case relayconstant.RelayModeImagesEdits, relayconstant.RelayModeMidjourneyEdits:
		return apigxmodel.CapabilityImageEdit
	case relayconstant.RelayModeAudioTranscription, relayconstant.RelayModeAudioTranslation:
		return apigxmodel.CapabilityAudioRecognition
	case relayconstant.RelayModeAudioSpeech:
		return apigxmodel.CapabilityAudioGeneration
	case relayconstant.RelayModeRerank:
		return apigxmodel.CapabilityRerank
	case relayconstant.RelayModeVideoSubmit, relayconstant.RelayModeMidjourneyVideo:
		return apigxmodel.CapabilityVideo
	default:
		return apigxmodel.CapabilityUnknown
	}
}

// resolveUsageAttribution 从实际底座渠道和 Key 索引恢复 APIGX 稳定归属。
// 解析不到的字段保持 0，不借用候选或池内下一把 Key。
func resolveUsageAttribution(db *gorm.DB, c *gin.Context, relayInfo *relaycommon.RelayInfo) usageAttribution {
	var out usageAttribution
	baseChannelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	if baseChannelID <= 0 {
		return out
	}
	var channel apigxmodel.Channel
	if err := db.Where("base_channel_id = ?", baseChannelID).First(&channel).Error; err != nil {
		return out
	}
	out.ownerUserID = channel.OwnerUserID
	out.channelID = channel.ID

	if index := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex); index >= 0 {
		if identity, ok := channelpool.ResolveKeyIdentity(db, baseChannelID, index); ok {
			out.upstreamKeyID = identity.UpstreamKeyID
			var key apigxmodel.UpstreamKey
			if db.Unscoped().Where("id = ?", out.upstreamKeyID).First(&key).Error == nil {
				out.upstreamAccountID = key.UpstreamAccountID
			}
		}
	}
	modelName := strings.TrimSpace(relayInfo.OriginModelName)
	if relayInfo.ChannelMeta != nil && strings.TrimSpace(relayInfo.UpstreamModelName) != "" {
		modelName = strings.TrimSpace(relayInfo.UpstreamModelName)
	}
	if offeringID, found, err := apigxmodel.FindOfferingByUpstreamModel(db, channel.ID, modelName); err == nil && found {
		out.offeringID = offeringID
		var offering apigxmodel.ModelOffering
		if db.Unscoped().Where("id = ?", offeringID).First(&offering).Error == nil {
			out.canonicalModelID = offering.CanonicalModelID
		}
	}
	if relayInfo.UserId > 0 && relayInfo.TokenId > 0 {
		if key, err := apigxmodel.GetAPIKeyByToken(db, int64(relayInfo.UserId), int64(relayInfo.TokenId)); err == nil {
			out.apiKeyID = key.ID
		}
	}
	return out
}

func usageEventAlreadyExists(db *gorm.DB, eventUID string) bool {
	if db == nil || eventUID == "" {
		return false
	}
	var count int64
	return db.Model(&apigxmodel.UsageEvent{}).Where("event_uid = ?", eventUID).Count(&count).Error == nil && count > 0
}

// RecordAsyncTaskUsage 把成功任务的最终 usage facts 写成同一套不可变事件。
// 当前只映射有明确稳定语义的字段；插件私有的 units 等字段保持未映射，绝不猜单位。
func RecordAsyncTaskUsage(baseTaskID int64, usageFacts map[string]any) error {
	if baseTaskID <= 0 {
		return nil
	}
	db := usageDBProvider()
	if db == nil {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var task apigxmodel.AsyncTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("base_task_id = ?", baseTaskID).First(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if task.Status != apigxmodel.AsyncTaskStatusSucceeded || task.SettledUsageEventID > 0 {
			return nil
		}
		facts := asyncTaskUsageFacts(task.Capability, usageFacts)
		if len(facts) == 0 {
			return nil
		}
		now := task.FinishAt
		if now <= 0 {
			now = common.GetTimestamp()
		}
		firstEventID := int64(0)
		for _, fact := range facts {
			uid := "t:" + strconv.FormatInt(task.BaseTaskID, 10) + ":" + strconv.Itoa(task.Capability) + ":" + strconv.Itoa(fact.Dimension)
			var existing apigxmodel.UsageEvent
			if err := tx.Where("event_uid = ?", uid).First(&existing).Error; err == nil {
				if firstEventID == 0 {
					firstEventID = existing.ID
				}
				continue
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			cost := apigxmodel.ResolveUsageCostFromPriceRule(tx, task.PriceRuleID, fact.Dimension, fact.QuantityMilli, fact.UnitScale)
			event := &apigxmodel.UsageEvent{
				EventUID: uid, Kind: apigxmodel.UsageEventKindUsage,
				AsyncTaskID: task.ID, UserID: task.UserID, OwnerUserID: task.OwnerUserID,
				ApiKeyID: task.ApiKeyID, CanonicalModelID: task.CanonicalModelID, ApiModelID: task.ApiModelID,
				OfferingID: task.OfferingID, ChannelID: task.ChannelID,
				UpstreamKeyID: task.UpstreamKeyID, UpstreamAccountID: task.UpstreamAccountID,
				Capability: task.Capability, Dimension: fact.Dimension,
				QuantityMilli: fact.QuantityMilli, UnitScale: cost.UnitScale, Trust: fact.Trust,
				PriceRuleID: cost.PriceRuleID, PriceRuleItemID: cost.PriceRuleItemID,
				UnitPriceMicro: cost.UnitPriceMicro, Currency: cost.Currency, FxRateMicro: cost.FxRateMicro,
				CostMicro: cost.CostMicro, CostUsdMicro: cost.CostUsdMicro, CostState: cost.CostState,
				OccurredAt: now, CreatedAt: common.GetTimestamp(),
			}
			if err := apigxmodel.RecordUsageEvent(tx, event); err != nil {
				return err
			}
			if firstEventID == 0 {
				firstEventID = event.ID
			}
		}
		if firstEventID == 0 {
			return nil
		}
		return tx.Model(&apigxmodel.AsyncTask{}).
			Where("id = ? AND settled_usage_event_id = 0", task.ID).
			Update("settled_usage_event_id", firstEventID).Error
	})
}

func asyncTaskUsageFacts(capability int, values map[string]any) []UsageFact {
	facts := make([]UsageFact, 0, 2)
	if seconds, ok := numericUsageFactMilli(values["seconds"]); ok && seconds > 0 {
		dimension := -1
		switch capability {
		case apigxmodel.CapabilityVideo:
			dimension = apigxmodel.UsageDimensionOutputVideoSeconds
		case apigxmodel.CapabilityAudioRecognition:
			dimension = apigxmodel.UsageDimensionInputAudioSeconds
		case apigxmodel.CapabilityAudioGeneration:
			dimension = apigxmodel.UsageDimensionOutputAudioSeconds
		}
		if dimension >= 0 {
			facts = append(facts, UsageFact{
				Dimension: dimension, QuantityMilli: seconds,
				UnitScale: apigxmodel.UsageUnitScalePerSecond, Trust: apigxmodel.UsageTrustUpstreamReported,
			})
		}
	}
	if capability == apigxmodel.CapabilityImageGeneration || capability == apigxmodel.CapabilityImageEdit {
		for _, key := range []string{"images", "image_count"} {
			if count, ok := numericUsageFactMilli(values[key]); ok && count > 0 {
				facts = append(facts, UsageFact{
					Dimension: apigxmodel.UsageDimensionImageCount, QuantityMilli: count,
					UnitScale: apigxmodel.UsageUnitScalePerImage, Trust: apigxmodel.UsageTrustUpstreamReported,
				})
				break
			}
		}
	}
	facts = append(facts, UsageFact{
		Dimension: apigxmodel.UsageDimensionRequestCount, QuantityMilli: 1000,
		UnitScale: apigxmodel.UsageUnitScalePerRequest, Trust: apigxmodel.UsageTrustLocalEstimated,
	})
	return facts
}

func numericUsageFactMilli(value any) (int64, bool) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	default:
		return 0, false
	}
	if number <= 0 || math.IsNaN(number) || math.IsInf(number, 0) || number > float64(math.MaxInt64)/1000 {
		return 0, false
	}
	return int64(math.Round(number * 1000)), true
}
