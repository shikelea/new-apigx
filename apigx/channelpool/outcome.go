package channelpool

import (
	"errors"
	"fmt"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// KeyOutcome 是一次调用结束后对某把上游凭据的反馈。
type KeyOutcome struct {
	// Success 为 true 时记 LastOKAt；否则记 LastErrorAt 与 ErrorCode。
	Success bool
	// ErrorCode 是脱敏后的失败原因（上游状态码或短标签），**不得包含凭据本身**。
	ErrorCode string
	// AuthFailed 表示上游明确拒绝了这份凭据（401/403 之类）：
	// 计划书 §9.2 要求做 **Key 级故障隔离**，此时把该 Key 标为失效而不是停整个渠道。
	AuthFailed bool
	// CooldownSeconds > 0 时把该 Key 置入冷却（例如上游限流）。
	// Key 级限流只让这一把冷却；确认是账户级/渠道级故障才应作用于共享范围。
	CooldownSeconds int64
}

// RecordKeyOutcome 把一次调用的结果记到对应的上游凭据上。
//
// 为什么需要翻译：底座的请求上下文只携带 Key 的**索引**（`ContextKeyChannelMultiKeyIndex`），
// 而我们的统计挂在**稳定凭据 ID** 上。轮换、增删、改权重都会改变索引与凭据的对应关系，
// 因此每次都要用投影把索引翻译回 StableKeyID，否则统计会落到别的凭据上。
//
// 成本说明：本函数会重算一次投影（若干条查询）。阶段 1 接受这个成本；
// 若成为热点，应在投影后把「底座渠道 → 索引映射」放入进程内缓存并在池变更时失效。
func RecordKeyOutcome(db *gorm.DB, baseChannelID int, keyIndex int, outcome KeyOutcome) error {
	if db == nil {
		return errors.New("channelpool: nil db")
	}
	if baseChannelID <= 0 || keyIndex < 0 {
		return fmt.Errorf("%w: invalid base channel or key index", apigxmodel.ErrInvalidArgument)
	}

	var shareChannel apigxmodel.Channel
	err := db.Where("base_channel_id = ?", baseChannelID).First(&shareChannel).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 不是我们的分享渠道（例如管理员自建的普通渠道）：没有可记的对象，静默返回
			return nil
		}
		return err
	}

	projection, err := ProjectKeyPool(db, shareChannel.ID)
	if err != nil {
		return err
	}
	stableKeyID, ok := projection.StableKeyIDForIndex(keyIndex)
	if !ok {
		// 索引不在当前投影里（池刚变更过）：不能猜，直接跳过本次统计
		return fmt.Errorf("%w: key index %d not in current projection of channel %d",
			apigxmodel.ErrNotFound, keyIndex, baseChannelID)
	}

	var key apigxmodel.UpstreamKey
	err = db.Where("channel_id = ? AND stable_key_id = ?", shareChannel.ID, stableKeyID).First(&key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: upstream key %s", apigxmodel.ErrNotFound, stableKeyID)
		}
		return err
	}

	now := common.GetTimestamp()
	updates := map[string]any{"updated_at": now}
	if outcome.Success {
		updates["last_ok_at"] = now
	} else {
		updates["last_error_at"] = now
		updates["last_error_code"] = truncateErrorCode(outcome.ErrorCode)
	}
	if outcome.AuthFailed {
		updates["status"] = apigxmodel.UpstreamKeyStatusInvalid
	}
	if outcome.CooldownSeconds > 0 {
		updates["cooldown_until"] = now + outcome.CooldownSeconds
	}

	return db.Model(&apigxmodel.UpstreamKey{}).
		Where("id = ? AND alive_flag = ?", key.ID, apigxmodel.AliveFlagAlive).
		Updates(updates).Error
}

// truncateErrorCode 把失败原因裁剪到列宽（64），避免写入失败。
func truncateErrorCode(code string) string {
	const maxLen = 64
	if len(code) <= maxLen {
		return code
	}
	return code[:maxLen]
}
