package apigxmodel

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 出站尝试（`apigx_attempts`）：对账的依据。
//
// 计划书 §9.2 要求的执行顺序是「持久记录请求意图与预算预留 → 取得容量 → 复验权限 →
// **持久记录出站尝试** → 发送」。把尝试先落库的意义在于：进程若在中途退出，
// 事后能凭这条记录知道"当时确实占用了容量/发出了请求"，从而做补偿——
// 这正是对账（reconciliation）需要的**唯一事实来源**。

// AttemptState 取值。
const (
	// AttemptStateStarted 已开始（尚未看到结果）。
	AttemptStateStarted int = 1
	// AttemptStateSucceeded 成功完成。
	AttemptStateSucceeded int = 2
	// AttemptStateFailed 失败完成。
	AttemptStateFailed int = 3
	// AttemptStateAbandoned 被对账判定为**遗弃**（进程中途退出，未留下结果）。
	AttemptStateAbandoned int = 4
)

// BeginAttempt 记录一次出站尝试的开始。
//
// `AttemptUID` 用容量租约 ID：它是该次尝试的幂等键，对账时据此**精确释放**对应的租约
// （容量账本的释放本身就是幂等的，重复释放不会把计数减成负数）。
func BeginAttempt(db *gorm.DB, attempt *Attempt) error {
	if db == nil || attempt == nil {
		return fmt.Errorf("%w: attempt is required", ErrInvalidArgument)
	}
	if attempt.AttemptUID == "" {
		return fmt.Errorf("%w: attempt_uid is required（对账需要它来精确定位租约）", ErrInvalidArgument)
	}
	if attempt.StartedAt == 0 {
		attempt.StartedAt = common.GetTimestamp()
	}
	attempt.State = AttemptStateStarted
	attempt.CreatedAt = common.GetTimestamp()
	return db.Create(attempt).Error
}

// CompleteAttempt 记录尝试的最终状态（按 AttemptUID 定位，幂等）。
//
// 只允许从「已开始」迁移：重复调用（例如失败路径与成功路径各触发一次）不会把已完成的
// 结果覆盖掉，避免对账与审计看到互相矛盾的状态。
func CompleteAttempt(db *gorm.DB, attemptUID string, state int, httpStatus int) error {
	if attemptUID == "" {
		return fmt.Errorf("%w: attempt_uid is required", ErrInvalidArgument)
	}
	if state != AttemptStateSucceeded && state != AttemptStateFailed {
		return fmt.Errorf("%w: state must be succeeded or failed", ErrInvalidArgument)
	}
	res := db.Model(&Attempt{}).
		Where("attempt_uid = ? AND state = ?", attemptUID, AttemptStateStarted).
		Updates(map[string]any{
			"state":        state,
			"http_status":  httpStatus,
			"completed_at": common.GetTimestamp(),
		})
	return res.Error
}

// ListStaleAttempts 返回「已开始但早已超过合理时长」的尝试：这些就是需要补偿的对象。
//
// 判据只用时间：进程崩溃不会留下任何标记，能观察到的事实就是"开始了但一直没结果"。
func ListStaleAttempts(db *gorm.DB, startedBefore int64, limit int) ([]Attempt, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db is required", ErrInvalidArgument)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var attempts []Attempt
	err := db.Where("state = ? AND started_at > 0 AND started_at < ?", AttemptStateStarted, startedBefore).
		Order("started_at ASC").Limit(limit).Find(&attempts).Error
	return attempts, err
}

// MarkAttemptAbandoned 把陈旧尝试标记为遗弃（仅在仍处于「已开始」时生效，幂等）。
func MarkAttemptAbandoned(db *gorm.DB, attemptUID string) error {
	if attemptUID == "" {
		return fmt.Errorf("%w: attempt_uid is required", ErrInvalidArgument)
	}
	return db.Model(&Attempt{}).
		Where("attempt_uid = ? AND state = ?", attemptUID, AttemptStateStarted).
		Updates(map[string]any{
			"state":        AttemptStateAbandoned,
			"completed_at": common.GetTimestamp(),
		}).Error
}

// GetAttemptByUID 按幂等键读取尝试（供对账与排障使用）。
func GetAttemptByUID(db *gorm.DB, attemptUID string) (*Attempt, error) {
	if db == nil || attemptUID == "" {
		return nil, fmt.Errorf("%w: attempt_uid is required", ErrInvalidArgument)
	}
	var attempt Attempt
	err := db.Where("attempt_uid = ?", attemptUID).First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

// OwnerUsageGroupBy 分享者消耗视图的分组维度（U-3）。
type OwnerUsageGroupBy string

const (
	// OwnerUsageByChannel 按分享渠道聚合。
	OwnerUsageByChannel OwnerUsageGroupBy = "channel"
	// OwnerUsageByKey 按上游 Key 聚合（计划书 §4 明确要求"单 Key 消耗"）。
	OwnerUsageByKey OwnerUsageGroupBy = "key"
	// OwnerUsageByModel 按模型聚合。
	OwnerUsageByModel OwnerUsageGroupBy = "model"
)

// OwnerUsageRow 一行聚合结果（渠道 / Key 维度）。
//
// 口径（**不编造数字**）：Success 为 HTTP 2xx；Failed 为非 2xx 且拿到状态码；Unfinished 为没有状态码；
// 三者相加等于 Attempts，便于对账。平均首字延迟由读取方用 TtftMsSum/TtftSamples 计算。
// **不输出金额**：attempts 表没有费用字段，不臆造。
type OwnerUsageRow struct {
	GroupID      int64  `json:"group_id"`
	Name         string `json:"name"`
	Attempts     int64  `json:"attempts"`
	Success      int64  `json:"success"`
	Failed       int64  `json:"failed"`
	Unfinished   int64  `json:"unfinished"`
	OutputTokens int64  `json:"output_tokens"`
	TtftMsSum    int64  `json:"ttft_ms_sum"`
	TtftSamples  int64  `json:"ttft_samples"`
}

// OwnerUsageModelRow 模型维度的一行（模型名是字符串，单独一个结构体避免类型失真）。
type OwnerUsageModelRow struct {
	Name         string `json:"name"`
	Attempts     int64  `json:"attempts"`
	Success      int64  `json:"success"`
	Failed       int64  `json:"failed"`
	Unfinished   int64  `json:"unfinished"`
	OutputTokens int64  `json:"output_tokens"`
	TtftMsSum    int64  `json:"ttft_ms_sum"`
	TtftSamples  int64  `json:"ttft_samples"`
}

// ownerAttemptsFilter 返回"属于该分享者"的渠道范围条件（归属由渠道 → owner 推出）。
func ownerAttemptsFilter() string {
	return "selected_channel_id IN (SELECT id FROM " + Channel{}.TableName() +
		" WHERE owner_user_id = ? AND deleted_at IS NULL)"
}

const ownerUsageSelect = "COUNT(*) AS attempts" +
	", SUM(CASE WHEN http_status >= 200 AND http_status < 300 THEN 1 ELSE 0 END) AS success" +
	", SUM(CASE WHEN http_status >= 300 THEN 1 ELSE 0 END) AS failed" +
	", SUM(CASE WHEN http_status = 0 THEN 1 ELSE 0 END) AS unfinished" +
	", COALESCE(SUM(output_tokens),0) AS output_tokens" +
	", COALESCE(SUM(ttft_ms),0) AS ttft_ms_sum" +
	", SUM(CASE WHEN ttft_ms > 0 THEN 1 ELSE 0 END) AS ttft_samples"

// SummarizeOwnerAttempts 按渠道或 Key 聚合**某个分享者自己**的调用情况。
func SummarizeOwnerAttempts(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, start, end int64) ([]OwnerUsageRow, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil db", ErrInvalidArgument)
	}
	if ownerUserID <= 0 {
		return nil, fmt.Errorf("%w: owner is required", ErrInvalidArgument)
	}
	if end <= 0 {
		end = common.GetTimestamp()
	}

	column := ""
	switch groupBy {
	case OwnerUsageByChannel:
		column = "selected_channel_id"
	case OwnerUsageByKey:
		column = "upstream_key_id"
	default:
		return nil, fmt.Errorf("%w: unsupported group_by", ErrInvalidArgument)
	}

	query := db.Table(Attempt{}.TableName()).
		Select(column+" AS group_id, "+ownerUsageSelect).
		Where(ownerAttemptsFilter(), ownerUserID)
	if start > 0 {
		query = query.Where("started_at >= ?", start)
	}
	query = query.Where("started_at <= ?", end).Group(column).Order("attempts DESC")

	rows := []OwnerUsageRow{}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// SummarizeOwnerAttemptsByModel 按**模型名**聚合某个分享者自己的调用情况。
func SummarizeOwnerAttemptsByModel(db *gorm.DB, ownerUserID int64, start, end int64) ([]OwnerUsageModelRow, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil db", ErrInvalidArgument)
	}
	if ownerUserID <= 0 {
		return nil, fmt.Errorf("%w: owner is required", ErrInvalidArgument)
	}
	if end <= 0 {
		end = common.GetTimestamp()
	}

	query := db.Table(Attempt{}.TableName()).
		Select("upstream_model_id AS name, "+ownerUsageSelect).
		Where(ownerAttemptsFilter(), ownerUserID)
	if start > 0 {
		query = query.Where("started_at >= ?", start)
	}
	query = query.Where("started_at <= ?", end).Group("upstream_model_id").Order("attempts DESC")

	rows := []OwnerUsageModelRow{}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// FillOwnerUsageNames 给渠道 / Key 维度的聚合结果补上可读名称。
//
// 分开做而不是 JOIN：两种维度的取名来源不同，聚合查询保持简单才能走索引。
func FillOwnerUsageNames(db *gorm.DB, ownerUserID int64, groupBy OwnerUsageGroupBy, rows []OwnerUsageRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].GroupID)
	}

	switch groupBy {
	case OwnerUsageByChannel:
		var channels []Channel
		if err := db.Where("owner_user_id = ? AND id IN ?", ownerUserID, ids).Find(&channels).Error; err != nil {
			return err
		}
		names := make(map[int64]string, len(channels))
		for i := range channels {
			names[channels[i].ID] = channels[i].Name
		}
		for i := range rows {
			rows[i].Name = names[rows[i].GroupID]
		}
	case OwnerUsageByKey:
		var keys []UpstreamKey
		if err := db.Where("id IN ? AND channel_id IN (SELECT id FROM "+Channel{}.TableName()+
			" WHERE owner_user_id = ? AND deleted_at IS NULL)", ids, ownerUserID).Find(&keys).Error; err != nil {
			return err
		}
		names := make(map[int64]string, len(keys))
		for i := range keys {
			label := keys[i].Name
			if label == "" {
				label = keys[i].StableKeyID
			}
			names[keys[i].ID] = label
		}
		for i := range rows {
			rows[i].Name = names[rows[i].GroupID]
		}
	}
	return nil
}
