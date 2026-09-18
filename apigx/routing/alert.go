package routing

import "sync"

// breakerAlertInterval 同一供给的熔断告警最小间隔（秒）。
//
// 取 10 分钟：熔断本身有冷却窗口，10 分钟足以让运维注意到，又不会把日志刷爆。
const breakerAlertInterval int64 = 600

var (
	breakerAlertMu   sync.Mutex
	breakerAlertLast = map[int64]int64{}
)

// claimBreakerAlertSlot 判断"此刻是否应当为该供给发一条熔断告警"，返回 true 表示应当。
//
// 这是**限频闸门**：同一供给在 breakerAlertInterval 秒内只会放行一次。
// 之所以返回 bool 而不是自己打日志：调用方负责取公开标识与拼消息，
// 而"是否该告警"作为一条不变量可以被直接测（不依赖日志文本）。
func claimBreakerAlertSlot(offeringID int64, now int64) bool {
	if offeringID <= 0 {
		return false
	}
	breakerAlertMu.Lock()
	defer breakerAlertMu.Unlock()
	last, seen := breakerAlertLast[offeringID]
	if seen && now-last < breakerAlertInterval {
		return false
	}
	breakerAlertLast[offeringID] = now
	return true
}

// resetBreakerAlertState 清空限频状态（仅测试使用，避免用例间互相影响）。
func resetBreakerAlertState() {
	breakerAlertMu.Lock()
	defer breakerAlertMu.Unlock()
	breakerAlertLast = map[int64]int64{}
}
