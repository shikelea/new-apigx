package apigxmodel

import (
	"fmt"
	"sort"
	"strconv"

	"gorm.io/gorm"
)

// OwnerUsageSummaryItem 是分享者消耗页的一行。请求质量来自 Attempt，真实用量与成本来自 UsageEvent；
// 两种事实分开聚合，避免用 token 或请求次数反推图片、音频、视频等能力的实际消耗。
type OwnerUsageSummaryItem struct {
	GroupID                 int64                `json:"group_id,omitempty"`
	GroupKey                string               `json:"group_key"`
	Name                    string               `json:"name"`
	RequestCount            int64                `json:"request_count"`
	AttemptCount            int64                `json:"attempt_count"`
	RetryCount              int64                `json:"retry_count"`
	SucceededAttemptCount   int64                `json:"succeeded_attempt_count"`
	FailedAttemptCount      int64                `json:"failed_attempt_count"`
	UnfinishedAttemptCount  int64                `json:"unfinished_attempt_count"`
	TtftMsSum               int64                `json:"ttft_ms_sum"`
	TtftSamples             int64                `json:"ttft_samples"`
	EstimatedCostUSDMicro   int64                `json:"estimated_cost_usd_micro"`
	EstimatedCostEventCount int64                `json:"estimated_cost_event_count"`
	PendingFXEventCount     int64                `json:"pending_fx_event_count"`
	UnpricedEventCount      int64                `json:"unpriced_event_count"`
	ExplicitZeroEventCount  int64                `json:"explicit_zero_event_count"`
	Usage                   []OwnerUsageQuantity `json:"usage"`
}

// OwnerUsageQuantity 保留能力与计量维度，quantity_milli 的 1000 表示 1 个自然单位。
// reported/estimated 分开返回；不可得的计量只增加事件数，不伪装成 0 用量。
type OwnerUsageQuantity struct {
	Capability            int    `json:"capability"`
	Dimension             int    `json:"dimension"`
	Unit                  string `json:"unit"`
	QuantityMilli         int64  `json:"quantity_milli"`
	UpstreamReportedMilli int64  `json:"upstream_reported_milli"`
	LocalEstimatedMilli   int64  `json:"local_estimated_milli"`
	UnknownEventCount     int64  `json:"unknown_event_count"`
	UnavailableEventCount int64  `json:"unavailable_event_count"`
}

type ownerUsageAttemptAggregate struct {
	GroupID                int64
	GroupKey               string
	RequestCount           int64
	AttemptCount           int64
	RetryCount             int64
	SucceededAttemptCount  int64
	FailedAttemptCount     int64
	UnfinishedAttemptCount int64
	TtftMsSum              int64
	TtftSamples            int64
}

type ownerUsageCostAggregate struct {
	GroupID                 int64
	GroupKey                string
	EstimatedCostUSDMicro   int64
	EstimatedCostEventCount int64
	PendingFXEventCount     int64
	UnpricedEventCount      int64
	ExplicitZeroEventCount  int64
}

type ownerUsageQuantityAggregate struct {
	GroupID               int64
	GroupKey              string
	Capability            int
	Dimension             int
	QuantityMilli         int64
	UpstreamReportedMilli int64
	LocalEstimatedMilli   int64
	UnknownEventCount     int64
	UnavailableEventCount int64
}

type ownerUsageIDCandidate struct {
	GroupID int64
}

type ownerUsageStringCandidate struct {
	GroupKey string
}

const ownerUsageAttemptSelect = "COUNT(DISTINCT CASE WHEN a.request_id > 0 THEN a.request_id ELSE NULL END)" +
	" + SUM(CASE WHEN a.request_id = 0 THEN 1 ELSE 0 END) AS request_count" +
	", COUNT(*) AS attempt_count" +
	", SUM(CASE WHEN a.attempt_index > 0 THEN 1 ELSE 0 END) AS retry_count" +
	", SUM(CASE WHEN a.http_status >= 200 AND a.http_status < 300 THEN 1 ELSE 0 END) AS succeeded_attempt_count" +
	", SUM(CASE WHEN a.http_status > 0 AND (a.http_status < 200 OR a.http_status >= 300) THEN 1 ELSE 0 END) AS failed_attempt_count" +
	", SUM(CASE WHEN a.http_status = 0 THEN 1 ELSE 0 END) AS unfinished_attempt_count" +
	", COALESCE(SUM(a.ttft_ms),0) AS ttft_ms_sum" +
	", SUM(CASE WHEN a.ttft_ms > 0 THEN 1 ELSE 0 END) AS ttft_samples"

const ownerUsageCostSelect = "COALESCE(SUM(CASE WHEN ue.cost_state IN (0,1) THEN ue.cost_usd_micro ELSE 0 END),0) AS estimated_cost_usd_micro" +
	", SUM(CASE WHEN ue.cost_state IN (0,1) THEN 1 ELSE 0 END) AS estimated_cost_event_count" +
	", SUM(CASE WHEN ue.cost_state = 3 THEN 1 ELSE 0 END) AS pending_fx_event_count" +
	", SUM(CASE WHEN ue.cost_state = 2 THEN 1 ELSE 0 END) AS unpriced_event_count" +
	", SUM(CASE WHEN ue.cost_state = 4 THEN 1 ELSE 0 END) AS explicit_zero_event_count"

const ownerUsageQuantitySelect = "COALESCE(SUM(ue.quantity_milli),0) AS quantity_milli" +
	", COALESCE(SUM(CASE WHEN ue.trust = 0 THEN ue.quantity_milli ELSE 0 END),0) AS upstream_reported_milli" +
	", COALESCE(SUM(CASE WHEN ue.trust = 1 THEN ue.quantity_milli ELSE 0 END),0) AS local_estimated_milli" +
	", SUM(CASE WHEN ue.trust = 2 THEN 1 ELSE 0 END) AS unknown_event_count" +
	", SUM(CASE WHEN ue.trust = 3 THEN 1 ELSE 0 END) AS unavailable_event_count"

// SummarizeOwnerUsage 返回一个有界页面。调用方必须传入明确时间窗；API 层负责提供默认窗口和上限。
// 返回 hasMore 而不是为了 total 再做一次全量 UNION 计数，避免大表统计页的额外扫描。
func SummarizeOwnerUsage(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, start, end int64, page, pageSize int) ([]OwnerUsageSummaryItem, bool, error) {
	if db == nil {
		return nil, false, fmt.Errorf("%w: nil db", ErrInvalidArgument)
	}
	if ownerUserID <= 0 {
		return nil, false, fmt.Errorf("%w: owner is required", ErrInvalidArgument)
	}
	if start <= 0 || end <= 0 || start > end {
		return nil, false, fmt.Errorf("%w: a valid time range is required", ErrInvalidArgument)
	}
	if page <= 0 || pageSize <= 0 || pageSize > 100 {
		return nil, false, fmt.Errorf("%w: invalid pagination", ErrInvalidArgument)
	}
	if groupBy != OwnerUsageByChannel && groupBy != OwnerUsageByKey && groupBy != OwnerUsageByModel {
		return nil, false, fmt.Errorf("%w: unsupported group_by", ErrInvalidArgument)
	}

	offset := (page - 1) * pageSize
	fetchLimit := offset + pageSize + 1
	if fetchLimit <= 0 || fetchLimit > 100001 {
		return nil, false, fmt.Errorf("%w: page is too deep", ErrInvalidArgument)
	}

	keys, err := ownerUsageGroupCandidates(db, ownerUserID, groupBy, start, end, fetchLimit)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(keys) > offset+pageSize
	if offset >= len(keys) {
		return []OwnerUsageSummaryItem{}, false, nil
	}
	last := offset + pageSize
	if last > len(keys) {
		last = len(keys)
	}
	keys = keys[offset:last]

	items := make([]OwnerUsageSummaryItem, len(keys))
	index := make(map[string]int, len(keys))
	for i, key := range keys {
		items[i] = OwnerUsageSummaryItem{GroupKey: key, Usage: []OwnerUsageQuantity{}}
		if groupBy != OwnerUsageByModel {
			items[i].GroupID, _ = strconv.ParseInt(key, 10, 64)
		}
		index[key] = i
	}

	attempts, err := aggregateOwnerAttempts(db, ownerUserID, groupBy, start, end, keys)
	if err != nil {
		return nil, false, err
	}
	for _, row := range attempts {
		key := ownerUsageAggregateKey(groupBy, row.GroupID, row.GroupKey)
		if i, ok := index[key]; ok {
			items[i].RequestCount = row.RequestCount
			items[i].AttemptCount = row.AttemptCount
			items[i].RetryCount = row.RetryCount
			items[i].SucceededAttemptCount = row.SucceededAttemptCount
			items[i].FailedAttemptCount = row.FailedAttemptCount
			items[i].UnfinishedAttemptCount = row.UnfinishedAttemptCount
			items[i].TtftMsSum = row.TtftMsSum
			items[i].TtftSamples = row.TtftSamples
		}
	}

	costs, quantities, err := aggregateOwnerUsageEvents(db, ownerUserID, groupBy, start, end, keys)
	if err != nil {
		return nil, false, err
	}
	for _, row := range costs {
		key := ownerUsageAggregateKey(groupBy, row.GroupID, row.GroupKey)
		if i, ok := index[key]; ok {
			items[i].EstimatedCostUSDMicro = row.EstimatedCostUSDMicro
			items[i].EstimatedCostEventCount = row.EstimatedCostEventCount
			items[i].PendingFXEventCount = row.PendingFXEventCount
			items[i].UnpricedEventCount = row.UnpricedEventCount
			items[i].ExplicitZeroEventCount = row.ExplicitZeroEventCount
		}
	}
	for _, row := range quantities {
		key := ownerUsageAggregateKey(groupBy, row.GroupID, row.GroupKey)
		if i, ok := index[key]; ok {
			items[i].Usage = append(items[i].Usage, OwnerUsageQuantity{
				Capability:            row.Capability,
				Dimension:             row.Dimension,
				Unit:                  ownerUsageDimensionUnit(row.Dimension),
				QuantityMilli:         row.QuantityMilli,
				UpstreamReportedMilli: row.UpstreamReportedMilli,
				LocalEstimatedMilli:   row.LocalEstimatedMilli,
				UnknownEventCount:     row.UnknownEventCount,
				UnavailableEventCount: row.UnavailableEventCount,
			})
		}
	}

	if err := fillOwnerUsageSummaryNames(db, ownerUserID, groupBy, items); err != nil {
		return nil, false, err
	}
	return items, hasMore, nil
}

func ownerUsageGroupCandidates(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, start, end int64, limit int) ([]string, error) {
	attemptQuery := ownerAttemptSummaryQuery(db, ownerUserID, groupBy, start, end)
	usageQuery := ownerEffectiveUsageQuery(db, ownerUserID, start, end)
	keys := make(map[string]struct{}, limit*2)

	if groupBy == OwnerUsageByModel {
		var attemptRows, usageRows []ownerUsageStringCandidate
		if err := attemptQuery.Select("r.api_model_id AS group_key").Group("r.api_model_id").Order("r.api_model_id ASC").Limit(limit).Scan(&attemptRows).Error; err != nil {
			return nil, err
		}
		if err := usageQuery.Select("ue.api_model_id AS group_key").Group("ue.api_model_id").Order("ue.api_model_id ASC").Limit(limit).Scan(&usageRows).Error; err != nil {
			return nil, err
		}
		for _, row := range append(attemptRows, usageRows...) {
			keys[row.GroupKey] = struct{}{}
		}
	} else {
		attemptColumn, usageColumn := "a.selected_channel_id", "ue.channel_id"
		if groupBy == OwnerUsageByKey {
			attemptColumn, usageColumn = "a.upstream_key_id", "ue.upstream_key_id"
		}
		var attemptRows, usageRows []ownerUsageIDCandidate
		if err := attemptQuery.Select(attemptColumn + " AS group_id").Group(attemptColumn).Order(attemptColumn + " ASC").Limit(limit).Scan(&attemptRows).Error; err != nil {
			return nil, err
		}
		if err := usageQuery.Select(usageColumn + " AS group_id").Group(usageColumn).Order(usageColumn + " ASC").Limit(limit).Scan(&usageRows).Error; err != nil {
			return nil, err
		}
		for _, row := range append(attemptRows, usageRows...) {
			keys[strconv.FormatInt(row.GroupID, 10)] = struct{}{}
		}
	}

	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	if groupBy == OwnerUsageByModel {
		sort.Strings(ordered)
	} else {
		sort.Slice(ordered, func(i, j int) bool {
			left, _ := strconv.ParseInt(ordered[i], 10, 64)
			right, _ := strconv.ParseInt(ordered[j], 10, 64)
			return left < right
		})
	}
	return ordered, nil
}

func ownerAttemptSummaryQuery(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, start, end int64) *gorm.DB {
	query := db.Table(Attempt{}.TableName()+" AS a").
		Where("a.selected_channel_id IN (SELECT id FROM "+Channel{}.TableName()+" WHERE owner_user_id = ? AND deleted_at IS NULL)", ownerUserID).
		Where("a.started_at >= ? AND a.started_at <= ?", start, end)
	if groupBy == OwnerUsageByModel {
		query = query.Joins("JOIN " + Request{}.TableName() + " AS r ON r.id = a.request_id")
	}
	return query
}

func ownerEffectiveUsageQuery(db *gorm.DB, ownerUserID, start, end int64) *gorm.DB {
	return db.Table(UsageEvent{}.TableName()+" AS ue").
		Where("ue.owner_user_id = ?", ownerUserID).
		Where("ue.occurred_at >= ? AND ue.occurred_at <= ?", start, end).
		Where(effectiveOwnerUsagePredicate())
}

// effectiveOwnerUsagePredicate 把校正解释为替换值：根事件与它的所有直接校正中只取 ID 最新的一行。
// AppendUsageCorrection 会把后续校正统一指回根事件，因此不会形成需要递归求值的链。
func effectiveOwnerUsagePredicate() string {
	table := UsageEvent{}.TableName()
	return "ue.id = (SELECT MAX(candidate.id) FROM " + table + " AS candidate" +
		" WHERE candidate.id = CASE WHEN ue.kind = 2 THEN ue.correction_of_event_id ELSE ue.id END" +
		" OR (candidate.kind = 2 AND candidate.correction_of_event_id = CASE WHEN ue.kind = 2 THEN ue.correction_of_event_id ELSE ue.id END))"
}

func aggregateOwnerAttempts(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, start, end int64, keys []string) ([]ownerUsageAttemptAggregate, error) {
	query := ownerAttemptSummaryQuery(db, ownerUserID, groupBy, start, end)
	rows := []ownerUsageAttemptAggregate{}
	if groupBy == OwnerUsageByModel {
		err := query.Select("r.api_model_id AS group_key, "+ownerUsageAttemptSelect).
			Where("r.api_model_id IN ?", keys).Group("r.api_model_id").Scan(&rows).Error
		return rows, err
	}
	ids := ownerUsageNumericKeys(keys)
	column := "a.selected_channel_id"
	if groupBy == OwnerUsageByKey {
		column = "a.upstream_key_id"
	}
	err := query.Select(column+" AS group_id, "+ownerUsageAttemptSelect).
		Where(column+" IN ?", ids).Group(column).Scan(&rows).Error
	return rows, err
}

func aggregateOwnerUsageEvents(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, start, end int64, keys []string) ([]ownerUsageCostAggregate, []ownerUsageQuantityAggregate, error) {
	query := ownerEffectiveUsageQuery(db, ownerUserID, start, end)
	costs := []ownerUsageCostAggregate{}
	quantities := []ownerUsageQuantityAggregate{}
	if groupBy == OwnerUsageByModel {
		filtered := query.Where("ue.api_model_id IN ?", keys)
		if err := filtered.Select("ue.api_model_id AS group_key, " + ownerUsageCostSelect).
			Group("ue.api_model_id").Scan(&costs).Error; err != nil {
			return nil, nil, err
		}
		err := filtered.Select("ue.api_model_id AS group_key, ue.capability, ue.dimension, " + ownerUsageQuantitySelect).
			Group("ue.api_model_id, ue.capability, ue.dimension").
			Order("ue.api_model_id ASC, ue.capability ASC, ue.dimension ASC").Scan(&quantities).Error
		return costs, quantities, err
	}
	ids := ownerUsageNumericKeys(keys)
	column := "ue.channel_id"
	if groupBy == OwnerUsageByKey {
		column = "ue.upstream_key_id"
	}
	filtered := query.Where(column+" IN ?", ids)
	if err := filtered.Select(column + " AS group_id, " + ownerUsageCostSelect).
		Group(column).Scan(&costs).Error; err != nil {
		return nil, nil, err
	}
	err := filtered.Select(column + " AS group_id, ue.capability, ue.dimension, " + ownerUsageQuantitySelect).
		Group(column + ", ue.capability, ue.dimension").
		Order(column + " ASC, ue.capability ASC, ue.dimension ASC").Scan(&quantities).Error
	return costs, quantities, err
}

func ownerUsageNumericKeys(keys []string) []int64 {
	ids := make([]int64, 0, len(keys))
	for _, key := range keys {
		if id, err := strconv.ParseInt(key, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func ownerUsageAggregateKey(groupBy OwnerUsageGroupBy, groupID int64, groupKey string) string {
	if groupBy == OwnerUsageByModel {
		return groupKey
	}
	return strconv.FormatInt(groupID, 10)
}

func fillOwnerUsageSummaryNames(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, items []OwnerUsageSummaryItem) error {
	if groupBy == OwnerUsageByModel {
		for i := range items {
			items[i].Name = items[i].GroupKey
		}
		return nil
	}
	ids := make([]int64, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].GroupID)
	}
	if groupBy == OwnerUsageByChannel {
		var channels []Channel
		if err := db.Where("owner_user_id = ? AND id IN ?", ownerUserID, ids).Find(&channels).Error; err != nil {
			return err
		}
		names := make(map[int64]string, len(channels))
		for i := range channels {
			names[channels[i].ID] = channels[i].Name
		}
		for i := range items {
			items[i].Name = names[items[i].GroupID]
		}
		return nil
	}
	var keys []UpstreamKey
	if err := db.Where("id IN ? AND channel_id IN (SELECT id FROM "+Channel{}.TableName()+
		" WHERE owner_user_id = ? AND deleted_at IS NULL)", ids, ownerUserID).Find(&keys).Error; err != nil {
		return err
	}
	names := make(map[int64]string, len(keys))
	for i := range keys {
		name := keys[i].Name
		if name == "" {
			name = keys[i].StableKeyID
		}
		names[keys[i].ID] = name
	}
	for i := range items {
		items[i].Name = names[items[i].GroupID]
	}
	return nil
}

func ownerUsageDimensionUnit(dimension int) string {
	switch dimension {
	case UsageDimensionInputTokens, UsageDimensionOutputTokens, UsageDimensionCacheReadTokens,
		UsageDimensionCacheWriteTokens, UsageDimensionReasoningTokens:
		return "token"
	case UsageDimensionImageCount:
		return "image"
	case UsageDimensionInputAudioSeconds, UsageDimensionOutputAudioSeconds, UsageDimensionOutputVideoSeconds:
		return "second"
	case UsageDimensionRequestCount:
		return "request"
	case UsageDimensionInputCharacters:
		return "character"
	case UsageDimensionDocumentCount:
		return "document"
	default:
		return "unknown"
	}
}
