// Package relayhook 承载 APIGX 与 relay 链路的接线点。
//
// 本文件是 T1.7 第二部分：把 apigx/capacity 的容量账本接进请求生命周期。
//
// **默认关闭**（`APIGX_CAPACITY_ENABLED` 未设置时为 false），因此不改变既有链路行为；
// 打开后才对渠道并发做预留与归还。这样做是为了在 `apigx_capacity_policies`（T1.3 的
// 领域表）接管配置之前，能先把接线本身在真实请求路径上验证清楚。
package relayhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	capacityEnabled atomic.Bool
	// channelLimit 是渠道并发上限。0 表示**未配置**。
	//
	// 按 CODE_STYLE 第 11 条，未配置不得解释为「不限」；但在配置体系接好之前，
	// 未配置只能表示「本开关不施加约束」——因此 0 时直接跳过，并在文档中登记这一过渡行为。
	channelLimit atomic.Int64
	// leaseTTL 是容量租约时长，长流由后台协程续租。
	//
	// 注意粒度：TTL 以**秒**为单位下发（`ttl/time.Second`），因此小于 1 秒的值会被截断为 0
	// 并被 Reserve 回退为默认值；配置时必须用整秒，且要明显大于续租间隔（ttl/3）。
	leaseTTL = 90 * time.Second
)

func init() {
	capacityEnabled.Store(common.GetEnvOrDefaultBool("APIGX_CAPACITY_ENABLED", false))
	channelLimit.Store(int64(common.GetEnvOrDefault("APIGX_CHANNEL_CONCURRENCY_LIMIT", 0)))
}

// Configure 设置运行期开关与渠道并发上限（供测试与未来的后台配置使用）。
func Configure(enabled bool, limit int64) {
	capacityEnabled.Store(enabled)
	channelLimit.Store(limit)
}

// Lease 表示一次请求持有的容量租约。
type Lease struct {
	ctrl *capacity.Controller
	id   string
	ttl  time.Duration
	stop chan struct{}
	once sync.Once
}

// Acquire 在渠道选定后为其获取容量租约。
//
// 参数 channelID 用 int 以匹配底座 `model.Channel.Id` 的类型。
//
// 返回 (nil, true)  —— 本开关未启用、渠道无上限或渠道未知：不施加约束；
// 返回 (nil, false) —— 容量不足或账本不可用：**已经写出错误响应**，调用方必须直接返回；
// 返回 (l, true)    —— 已持有租约，调用方必须在请求结束时调用 l.Release()。
func Acquire(c *gin.Context, channelID int) (*Lease, bool) {
	if !capacityEnabled.Load() || channelID <= 0 {
		return nil, true
	}
	limit := channelLimit.Load()
	if limit <= 0 {
		// 未配置渠道并发上限：不施加约束（过渡行为，见包注释）
		return nil, true
	}

	ctrl := capacity.New(common.RDB)
	id := uuid.NewString()
	dims := []capacity.Dimension{{
		Key:    capacity.ChannelConcurrency(int64(channelID)),
		Limit:  limit,
		Amount: 1,
	}}

	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if err := ctrl.Reserve(ctx, id, dims, leaseTTL); err != nil {
		abortForError(c, err)
		return nil, false
	}

	lease := &Lease{ctrl: ctrl, id: id, ttl: leaseTTL, stop: make(chan struct{})}
	lease.startRenew()
	return lease, true
}

// Release 归还租约并停止续租。幂等：重复调用只释放一次。
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		close(l.stop)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.ctrl.Release(ctx, l.id); err != nil {
			common.SysLog("apigx capacity: release failed: " + err.Error())
		}
	})
}

// startRenew 为长流续租。
//
// 计划书 §9.2：同步与流式调用要持有槽位到实际结束，长流必须续租；
// 续租失败说明租约已被回收，此时**不能假装仍持有**，必须记录并停止。
func (l *Lease) startRenew() {
	interval := l.ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				ok, err := l.ctrl.Renew(ctx, l.id, l.ttl)
				cancel()
				if err != nil {
					common.SysLog("apigx capacity: renew failed: " + err.Error())
					return
				}
				if !ok {
					common.SysLog(fmt.Sprintf("apigx capacity: lease %s expired before completion", l.id))
					return
				}
			}
		}
	}()
}

// abortForError 把容量错误映射为对外响应。
//
// 对外只给状态码与脱敏信息；内部归因（具体维度）只进日志（计划书 §9.1 的错误码约定）。
func abortForError(c *gin.Context, err error) {
	if c == nil {
		return
	}
	var exceeded *capacity.ErrExceeded
	switch {
	case errors.As(err, &exceeded):
		common.SysLog("apigx capacity: exceeded on " + exceeded.Dimension)
		c.Header("Retry-After", "1")
		abortJSON(c, http.StatusTooManyRequests, "capacity_exceeded",
			"channel capacity is temporarily exceeded, please retry later")
	case errors.Is(err, capacity.ErrRedisUnavailable):
		// Redis 不可用时必须**拒绝准入**，不能从空计数恢复为「全部空闲」
		common.SysLog("apigx capacity: ledger unavailable, refusing new admissions")
		abortJSON(c, http.StatusServiceUnavailable, "capacity_ledger_unavailable",
			"capacity ledger unavailable, refusing new requests")
	default:
		common.SysLog("apigx capacity: reserve failed: " + err.Error())
		abortJSON(c, http.StatusServiceUnavailable, "capacity_reserve_failed",
			"unable to reserve capacity, refusing new request")
	}
}

func abortJSON(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{"message": message, "type": "new_api_error", "code": code},
	})
}
