// Package relayhook 承载 APIGX 与 relay 链路的接线点。
//
// 本文件是 T1.7 的接线层：把 apigx/capacity 的容量账本接进请求生命周期。
//
// **默认关闭**（`APIGX_CAPACITY_ENABLED` 未设置时为 false），因此不改变既有链路行为。
//
// 限制来源的过渡状态：当前各维度的上限来自环境变量；最终应来自
// `apigx_capacity_policies`（T1.3 已建表）。为此这里刻意通过 `capacity.Resolve`
// 走四态解析——接上数据库后只需在链首**追加更具体的策略**，接线层不必改动。
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
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Config 是接线层的运行期配置（未来由 apigx_capacity_policies 填充）。
//
// 各 Limit 字段的语义遵循四态：**0 表示「未配置」**，绝不会被解释成「不限」。
type Config struct {
	Enabled      bool
	ChannelLimit int64
	TokenLimit   int64
	UserLimit    int64
	// UpstreamKeyLimit / UpstreamAccountLimit 是上游凭据与共享账户池的并发上限。
	// 账户池这一维是关键：不能因为切换模型或 Key 就叠加容量（计划书 §9.2）。
	UpstreamKeyLimit     int64
	UpstreamAccountLimit int64
	// ChannelRPMLimit 是渠道的每分钟请求数上限。与并发不同，速率计数在窗口内**不归还**。
	ChannelRPMLimit int64
	// ChannelTPMLimit 是渠道的每分钟 token 数上限（需要"可执行的输出上限"，因此校验点
	// 在请求解析之后，见 relayhook.CheckRequestTPM）。
	ChannelTPMLimit int64
}

var (
	currentConfig atomic.Value // 存 Config
	// leaseTTL 是容量租约时长，长流由后台协程续租。
	//
	// 注意粒度：TTL 以**秒**为单位下发（`ttl/time.Second`），因此小于 1 秒的值会被截断为 0
	// 并被 Reserve 回退为默认值；配置时必须用整秒，且要明显大于续租间隔（ttl/3）。
	leaseTTL = 90 * time.Second
)

func init() {
	Configure(Config{
		Enabled:              common.GetEnvOrDefaultBool("APIGX_CAPACITY_ENABLED", false),
		ChannelLimit:         int64(common.GetEnvOrDefault("APIGX_CHANNEL_CONCURRENCY_LIMIT", 0)),
		TokenLimit:           int64(common.GetEnvOrDefault("APIGX_TOKEN_CONCURRENCY_LIMIT", 0)),
		UserLimit:            int64(common.GetEnvOrDefault("APIGX_USER_CONCURRENCY_LIMIT", 0)),
		UpstreamKeyLimit:     int64(common.GetEnvOrDefault("APIGX_UPSTREAM_KEY_CONCURRENCY_LIMIT", 0)),
		UpstreamAccountLimit: int64(common.GetEnvOrDefault("APIGX_UPSTREAM_ACCOUNT_CONCURRENCY_LIMIT", 0)),
		ChannelRPMLimit:      int64(common.GetEnvOrDefault("APIGX_CHANNEL_RPM_LIMIT", 0)),
		ChannelTPMLimit:      int64(common.GetEnvOrDefault("APIGX_CHANNEL_TPM_LIMIT", 0)),
	})
}

// Configure 设置运行期配置（供测试与未来的后台配置使用）。
func Configure(cfg Config) { currentConfig.Store(cfg) }

func getConfig() Config {
	if v, ok := currentConfig.Load().(Config); ok {
		return v
	}
	return Config{}
}

// policyFromLimit 把一个环境/默认限制转成四态策略。
//
// 值为 0 时返回「未配置」而不是「不限」——这是 CODE_STYLE 第 11 条的硬要求。
func policyFromLimit(limit int64) capacity.Policy {
	if limit <= 0 {
		return capacity.Policy{Mode: capacity.LimitModeUnset}
	}
	return capacity.Policy{Mode: capacity.LimitModeValue, Value: limit}
}

// buildDimensions 依据当前配置与调用者身份，构造本次请求需要预留的容量维度。
//
// 返回值 error 非空表示**明确禁用**（调用方必须拒绝请求），例如某个维度被配置为「禁用」。
// 未配置的维度会被跳过（不产生约束），这与「明确不限」在解析结果上不同、在行为上都等于不约束——
// 但区别必须保留在数据层，且未配置是待补的过渡状态（见包注释）。
func buildDimensions(c *gin.Context, channelID int) ([]capacity.Dimension, error) {
	cfg := getConfig()
	dims := make([]capacity.Dimension, 0, 3)

	add := func(key string, limit int64) error {
		res, err := capacity.Resolve(policyFromLimit(limit))
		if err != nil {
			return err
		}
		switch res.Decision {
		case capacity.DecisionLimited:
			dims = append(dims, capacity.Dimension{Key: key, Limit: res.Value, Amount: 1})
		case capacity.DecisionDisabled:
			return fmt.Errorf("capacity disabled on dimension %s", key)
		default:
			// unset / unlimited：本层不施加约束
		}
		return nil
	}

	// 渠道维度：策略链 = 渠道自身的容量策略（apigx_capacity_policies） → 环境变量兜底。
	// 策略存在数据库里，因此走 channelpool 的短 TTL 缓存；改上限最长一个 TTL 后生效。
	if shareChannelID, ok := channelpool.ShareChannelIDByBase(channelpool.CurrentDB(), channelID); ok {
		chain := channelpool.ChannelConcurrencyChain(channelpool.CurrentDB(), shareChannelID, cfg.ChannelLimit, cfg.ChannelRPMLimit, cfg.ChannelTPMLimit)
		res, err := capacity.Resolve(chain...)
		if err != nil {
			return nil, err
		}
		switch res.Decision {
		case capacity.DecisionLimited:
			dims = append(dims, capacity.Dimension{Key: capacity.ChannelConcurrency(int64(channelID)), Limit: res.Value, Amount: 1})
		case capacity.DecisionDisabled:
			return nil, fmt.Errorf("capacity disabled on dimension %s", capacity.ChannelConcurrency(int64(channelID)))
		default:
			// unset / unlimited：本层不施加约束
		}
	} else if err := add(capacity.ChannelConcurrency(int64(channelID)), cfg.ChannelLimit); err != nil {
		// 不是我们的分享渠道：退回环境变量（管理员自建渠道没有任何 APIGX 策略）
		return nil, err
	}
	if tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId); tokenID > 0 {
		if err := add(capacity.TokenConcurrency(int64(tokenID)), cfg.TokenLimit); err != nil {
			return nil, err
		}
	}
	if userID := common.GetContextKeyInt(c, constant.ContextKeyUserId); userID > 0 {
		if err := add(capacity.UserConcurrency(int64(userID)), cfg.UserLimit); err != nil {
			return nil, err
		}
	}
	return dims, nil
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
// 返回 (nil, true)  —— 本开关未启用、各维度均未配置上限或渠道未知：不施加约束；
// 返回 (nil, false) —— 容量不足 / 维度被禁用 / 账本不可用：**已经写出错误响应**，调用方必须直接返回；
// 返回 (l, true)    —— 已持有租约，调用方必须在请求结束时调用 l.Release()。
func Acquire(c *gin.Context, channelID int) (*Lease, bool) {
	cfg := getConfig()
	if !cfg.Enabled || channelID <= 0 {
		return nil, true
	}

	dims, err := buildDimensions(c, channelID)
	if err != nil {
		common.SysLog("apigx capacity: " + err.Error())
		abortJSON(c, http.StatusForbidden, "capacity_disabled", "this resource is disabled for capacity control")
		return nil, false
	}

	// 上游凭据与共享账户池维度（T1.7）：底座的运行期只给出 Key 索引，
	// 这里翻译成我们的凭据身份（走映射缓存，不会反复解密全部凭据）。
	//
	// **必须放在「所有维度都未配置就提前返回」之前**：否则只配置了账户池上限
	// （而不配渠道/令牌/用户上限）时，会在这里直接返回「不施加约束」，
	// 新维度永远不生效——这是本轮实现时的真实缺陷，由端到端验证发现。
	if cfg.UpstreamKeyLimit > 0 || cfg.UpstreamAccountLimit > 0 {
		if common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
			keyIndex := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
			dims = append(dims, channelpool.BuildKeyCapacityDimensions(
				channelpool.CurrentDB(), channelID, keyIndex,
				channelpool.KeyCapacityLimits{
					UpstreamKeyLimit:     cfg.UpstreamKeyLimit,
					UpstreamAccountLimit: cfg.UpstreamAccountLimit,
				})...)
		}
	}

	// RPM 与并发维度**分开处理**（速率计数不归还），因此必须在这里先解析它，
	// 否则"只配了 RPM、没配任何并发维度"时会在下面的空维度判断处直接返回，
	// 速率限制形同虚设——这是端到端验证抓到的第二个同类缺陷。
	rpmLimit, hasRPM := resolveChannelRPMLimit(c, channelID, cfg.ChannelRPMLimit)

	// 到这里才判断「是否完全没有维度」：所有维度来源（身份维度 + 上游凭据/账户池维度
	// + 速率维度）都已确认，任何一个被配置了就应当施加约束。
	if len(dims) == 0 && !hasRPM {
		// 所有维度都未配置：不施加约束（过渡行为，见包注释）
		return nil, true
	}

	ctrl := capacity.New(common.RDB)
	id := uuid.NewString()

	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if err := ctrl.Reserve(ctx, id, dims, leaseTTL); err != nil {
		abortForError(c, err)
		return nil, false
	}

	// 速率维度（RPM）：**在拿到并发租约之后**再计数。
	// 顺序理由：并发不足时请求根本不会发出去，不该消耗速率额度；反过来则要对已拿到的
	// 租约做补偿（下面就是）。
	// 速率维度（RPM）：**在拿到并发租约之后**再计数——并发不足时请求根本不会发出去，
	// 不该消耗速率额度；反过来则要对已拿到的租约做补偿（下面就是）。
	if hasRPM {
		{
			windowKey := capacity.ChannelRPM(int64(channelID), capacity.MinuteWindow(time.Now()))
			if rpmErr := ctrl.IncrementWindow(ctx, windowKey, rpmLimit, 1, capacity.WindowTTL(time.Minute)); rpmErr != nil {
				// 速率超限：把刚拿到的并发租约**还回去**，避免"没发出去的请求"白占一个槽位
				if releaseErr := ctrl.Release(ctx, id); releaseErr != nil {
					common.SysLog("apigx capacity: release after rpm rejection failed: " + releaseErr.Error())
				}
				abortForError(c, rpmErr)
				return nil, false
			}
		}
	}

	lease := &Lease{ctrl: ctrl, id: id, ttl: leaseTTL, stop: make(chan struct{})}
	lease.startRenew()
	return lease, true
}

// resolveChannelRPMLimit 解析渠道的 RPM 上限（策略链：渠道策略 → 环境变量兜底）。
//
// 返回 false 表示「未配置/明确不限」——两种情形都不施加约束（未配置是待补的过渡状态）。
func resolveChannelRPMLimit(c *gin.Context, channelID int, envLimit int64) (int64, bool) {
	shareChannelID, ok := channelpool.ShareChannelIDByBase(channelpool.CurrentDB(), channelID)
	if !ok {
		return envLimit, envLimit > 0
	}
	// 这里只解析 RPM：TPM 的环境值传 0（TPM 有自己的解析入口 CheckRequestTPM，
	// 且它与本函数共用同一份策略缓存，重建时也会把 TPM 链一起建好）。
	chain := channelpool.ChannelRPMChain(channelpool.CurrentDB(), shareChannelID, envLimit, 0)
	res, err := capacity.Resolve(chain...)
	if err != nil {
		common.SysError("apigx capacity: resolve rpm limit failed: " + err.Error())
		return 0, false
	}
	if res.Decision == capacity.DecisionLimited {
		return res.Value, true
	}
	return 0, false
}

// ID 返回租约标识（= 出站尝试的 AttemptUID，对账据此精确定位并释放容量）。
func (l *Lease) ID() string {
	if l == nil {
		return ""
	}
	return l.id
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
		// 内部日志带精确维度；对外只说「容量暂时不足」，不暴露是哪一维
		// （对外写成 "channel capacity" 会误导：超限的也可能是平台 Key 或用户维度）。
		common.SysLog("apigx capacity: exceeded on " + exceeded.Dimension)
		c.Header("Retry-After", "1")
		abortJSON(c, http.StatusTooManyRequests, "capacity_exceeded",
			"capacity is temporarily exceeded, please retry later")
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

// ---------------------------------------------------------------------------
// 每次尝试的租约（与底座的「重试会重新选渠道」对齐）
// ---------------------------------------------------------------------------

// leaseContextKey 是存放在 gin 上下文里的租约键。
const leaseContextKey = "apigx_capacity_lease"

// AcquireForAttempt 为**本次尝试**获取容量租约。
//
// 必须由「每一次尝试都会经过」的位置调用（底座的 `SetupContextForSelectedChannel`）：
// 底座的失败重试会**重新选择渠道**并再次经过该函数，因此：
//   - 每次尝试都按**当次选中的渠道与凭据**重新预留，不会出现「换了渠道却仍用旧渠道的租约」；
//   - 进入新尝试前会先释放上一次的租约，避免重试链上累积占用；
//   - 请求结束由调用方（Distributor 的 defer）统一释放最后一次租约。
//
// 返回 false 表示**容量不足或账本不可用，且已经写出错误响应**，调用方必须停止本次尝试。
func AcquireForAttempt(c *gin.Context, channelID int) bool {
	if c == nil {
		return true
	}
	// 上一次尝试的租约先归还：否则重试会同时占用两个渠道的容量
	ReleaseFromContext(c)

	lease, ok := Acquire(c, channelID)
	if !ok {
		return false
	}
	if lease != nil {
		c.Set(leaseContextKey, lease)
		// **持久记录出站尝试**（计划书 §9.2 要求的执行顺序）：进程若在中途退出，
		// 事后就能凭这条记录知道"当时确实占用了容量"，进而做对账补偿。
		// 记录失败不影响请求本身——容量租约仍在，TTL 与对账都会兜住。
		if db := channelpool.CurrentDB(); db != nil {
			attempt := &apigxmodel.Attempt{
				AttemptUID:        lease.ID(),
				RequestID:         channelpool.RequestIDFromAttemptUID(lease.ID()),
				SelectedChannelID: int64(channelID),
				BaseChannelID:     int64(channelID),
			}
			if beginErr := apigxmodel.BeginAttempt(db, attempt); beginErr != nil {
				common.SysError("apigx capacity: begin attempt failed: " + beginErr.Error())
			}
		}
	}
	return true
}

// ReleaseFromContext 释放上下文里持有的租约（幂等；无租约时为空操作）。
func ReleaseFromContext(c *gin.Context) {
	if c == nil {
		return
	}
	value, exists := c.Get(leaseContextKey)
	if !exists {
		return
	}
	if lease, ok := value.(*Lease); ok && lease != nil {
		// 先落状态再释放：顺序反过来的话，一旦释放后进程退出，尝试会永远停在「已开始」
		// 而被对账误判为遗弃（实际上它是正常完成的）。
		if db := channelpool.CurrentDB(); db != nil && lease.ID() != "" {
			state := apigxmodel.AttemptStateFailed
			status := 0
			if c.Writer != nil {
				status = c.Writer.Status()
				if status < http.StatusBadRequest {
					state = apigxmodel.AttemptStateSucceeded
				}
			}
			if err := apigxmodel.CompleteAttempt(db, lease.ID(), state, status); err != nil {
				common.SysError("apigx capacity: complete attempt failed: " + err.Error())
			}
		}
		lease.Release()
	}
	c.Set(leaseContextKey, (*Lease)(nil))
}
