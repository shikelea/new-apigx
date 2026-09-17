// Package capacity 实现 APIGX 的共享容量控制（并发 / 速率 / 任务槽位）。
//
// # 为什么必须自建
//
// 底座没有渠道/Key 级的并发上限：`model/channel.go` 的选择互斥锁只保护「选择过程」，
// `middleware/model-rate-limit.go` 是**用户级**窗口限流。这一点已在阶段 0 实测确认
// （8 个并发请求的到达时间跨度仅 170ms，即全部同时在飞）。
//
// # 设计约束（来自计划书 §9.2 与 docs/stage0/gate-review.md）
//
//  1. **原子预留**：多维度必须「全有或全无」，不能只做「先读计数再发送」——那会在并发下突破上限。
//  2. **幂等归还**：每次尝试有唯一 ID，重复释放不得把计数减成负数。
//  3. **租约语义**：所有计数都带 TTL；进程崩溃后靠过期自动回收，不能永久泄漏槽位。
//  4. **独立于计费**：本包**不读写**用户钱包、也不依赖 `effectiveQuota`。
//     底座 `shouldTrust` 在大额余额下会把 effectiveQuota 置 0 并跳过令牌预扣（TD-09），
//     若容量预留挂在它后面就会静默失效。
//  5. **Redis 不可用时拒绝新调用**，而不是放行——「无法安全判断共享额度」时必须停止准入。
//
// 本包只做容量账本；把哪些维度、用什么上限接入请求路径由上层（relayhook）负责。
package capacity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// DefaultKeyPrefix 是容量计数器的 Redis key 前缀。
const DefaultKeyPrefix = "apigx:cap:"

// LeaseTTL 是 HTTP 请求槽位的默认租约时长。
//
// 语义：持有者必须在到期前续租（长流用 Renew），否则计数自动回落。
// 选 90 秒是因为它明显长于普通请求，又足够短到进程崩溃后能较快恢复容量。
const LeaseTTL = 90 * time.Second

// ErrRedisUnavailable 表示容量账本不可用。
//
// 计划书 §9.2：Redis 状态丢失或写入失败时必须**停止新准入**、进入对账，
// 不能从空计数直接恢复为「全部空闲」。
var ErrRedisUnavailable = errors.New("capacity ledger unavailable (redis disabled or not configured)")

// ErrExceeded 表示某个维度容量不足。调用方据此撤销本次全部未生效预留，并重选候选。
type ErrExceeded struct {
	Dimension string
}

func (e *ErrExceeded) Error() string {
	return fmt.Sprintf("capacity exceeded on dimension %q", e.Dimension)
}

// Dimension 是一个需要预留的容量维度。
//
// Amount 通常是 1（并发槽位），也可以是消耗量（如 TPM 的 token 数）。
type Dimension struct {
	Key    string
	Limit  int64
	Amount int64
}

// ---------------------------------------------------------------------------
// 维度 key 构造
//
// 每个维度都要能同时约束「同账户的多渠道、多渠道的多 Key」（计划书 §9.2：
// 不能因切换模型或 Key 而叠加容量）。因此 key 里带的是**稳定 ID**，不含名称。
// ---------------------------------------------------------------------------

// UserConcurrency 用户维度的并发槽位。
func UserConcurrency(userID int64) string { return "user:" + i64(userID) + ":concurrency" }

// TokenConcurrency 平台 Key（底座 tokens.id）维度的并发槽位。
func TokenConcurrency(tokenID int64) string { return "token:" + i64(tokenID) + ":concurrency" }

// ChannelConcurrency 渠道维度的并发槽位。
func ChannelConcurrency(channelID int64) string { return "channel:" + i64(channelID) + ":concurrency" }

// OfferingConcurrency 模型供给维度的并发槽位。
func OfferingConcurrency(offeringID int64) string {
	return "offering:" + i64(offeringID) + ":concurrency"
}

// UpstreamKeyConcurrency 上游 Key 维度的并发槽位。
func UpstreamKeyConcurrency(upstreamKeyID int64) string {
	return "upkey:" + i64(upstreamKeyID) + ":concurrency"
}

// UpstreamAccountConcurrency 上游账户池维度的并发槽位。
//
// 这是「多模型、多 Key、多渠道共用同一上游额度」的约束载体：
// 同一账户下的渠道即使各自独立计数未满，也必须受该池限制。
func UpstreamAccountConcurrency(accountID int64) string {
	return "upaccount:" + i64(accountID) + ":concurrency"
}

// UpstreamAccountDailyUsage 上游共享账户池的每日用量（按计量单位，如 token 数）。
func UpstreamAccountDailyUsage(accountID int64, day int64) string {
	return "upaccount:" + i64(accountID) + ":daily:" + i64(day)
}

// ChannelRPM 渠道在给定时间窗内的请求数。
func ChannelRPM(channelID int64, window int64) string {
	return "channel:" + i64(channelID) + ":rpm:" + i64(window)
}

// ChannelTPM 渠道在给定时间窗内的 token 数。
func ChannelTPM(channelID int64, window int64) string {
	return "channel:" + i64(channelID) + ":tpm:" + i64(window)
}

// AsyncTaskSlot 异步任务槽位（与 HTTP 并发分开记账，计划书 §9.2 第 3 条）。
func AsyncTaskSlot(scope string, id int64) string {
	return scope + ":" + i64(id) + ":tasks"
}

// MinuteWindow 返回用于窗口化限额的分钟桶编号（UTC epoch 分钟）。
func MinuteWindow(t time.Time) int64 { return t.UTC().Unix() / 60 }

func i64(v int64) string { return strconv.FormatInt(v, 10) }

// ---------------------------------------------------------------------------
// Lua 脚本
// ---------------------------------------------------------------------------

// reserveScript 原子预留：先全量检查，再全量扣减。
//
// KEYS[1..n] = 各维度计数器；KEYS[n+1] = 本次尝试的租约 hash
// ARGV[1] = 租约 TTL（秒）；其后每维度两个值：limit、amount
// 返回 {1, ""} 表示成功；{0, <维度 key>} 表示该维度超限（此时**没有任何扣减发生**）。
const reserveScript = `
local leaseKey = KEYS[#KEYS]
local ttl = tonumber(ARGV[1])
local n = #KEYS - 1
for i = 1, n do
  local limit = tonumber(ARGV[1 + (i - 1) * 2 + 1])
  local amount = tonumber(ARGV[1 + (i - 1) * 2 + 2])
  local current = tonumber(redis.call('GET', KEYS[i]) or '0')
  if current + amount > limit then
    return {0, KEYS[i]}
  end
end
for i = 1, n do
  local amount = tonumber(ARGV[1 + (i - 1) * 2 + 2])
  redis.call('INCRBY', KEYS[i], amount)
  redis.call('EXPIRE', KEYS[i], ttl)
  redis.call('HSET', leaseKey, KEYS[i], amount)
end
redis.call('EXPIRE', leaseKey, ttl)
return {1, ''}
`

// releaseScript 幂等归还：租约不存在即视为已释放。
//
// 必须用「读租约 -> 按记录额度扣减 -> 删租约」的方式，而不是简单地把计数减 1：
// 这样重复释放不会造成负数，也不会误伤他人持有的槽位。
const releaseScript = `
local leaseKey = KEYS[1]
local entries = redis.call('HGETALL', leaseKey)
if #entries == 0 then
  return 0
end
for i = 1, #entries, 2 do
  local key = entries[i]
  local amount = tonumber(entries[i + 1])
  local left = redis.call('DECRBY', key, amount)
  if left <= 0 then
    redis.call('DEL', key)
  end
end
redis.call('DEL', leaseKey)
return 1
`

// renewScript 续租：租约已过期时返回 0，调用方必须停止续租并按失败处理。
const renewScript = `
local leaseKey = KEYS[1]
if redis.call('EXISTS', leaseKey) == 0 then
  return 0
end
local ttl = tonumber(ARGV[1])
local entries = redis.call('HGETALL', leaseKey)
for i = 1, #entries, 2 do
  redis.call('EXPIRE', entries[i], ttl)
end
redis.call('EXPIRE', leaseKey, ttl)
return 1
`

// Controller 是容量账本的操作入口。
type Controller struct {
	rdb    *redis.Client
	prefix string
	// scripts 用 EVALSHA 走脚本缓存，失败时 go-redis 会自动回退 EVAL
	reserve *redis.Script
	release *redis.Script
	renew   *redis.Script
	// window 是只增不减的窗口计数脚本（RPM/TPM 用），与租约模型分开
	window *redis.Script
}

// New 用底座既有的 Redis 客户端（common.RDB）构造容量控制器。
func New(rdb *redis.Client) *Controller { return NewWithPrefix(rdb, DefaultKeyPrefix) }

// NewWithPrefix 允许自定义 key 前缀（测试用，避免与真实计数互相污染）。
func NewWithPrefix(rdb *redis.Client, prefix string) *Controller {
	return &Controller{
		rdb:     rdb,
		prefix:  prefix,
		reserve: redis.NewScript(reserveScript),
		release: redis.NewScript(releaseScript),
		renew:   redis.NewScript(renewScript),
		window:  redis.NewScript(windowScript),
	}
}

func (c *Controller) counterKey(dim string) string { return c.prefix + dim }
func (c *Controller) leaseKey(attemptID string) string {
	return c.prefix + "lease:" + attemptID
}

func (c *Controller) available() error {
	if c == nil || c.rdb == nil {
		return ErrRedisUnavailable
	}
	return nil
}

// Reserve 原子预留全部维度。
//
// 任一维度不足时**不会产生任何扣减**，并返回 *ErrExceeded（维度名可用于日志与重选）。
// attemptID 必须唯一（计划书：用唯一尝试 ID 保证归还幂等）。
func (c *Controller) Reserve(ctx context.Context, attemptID string, dims []Dimension, ttl time.Duration) error {
	if err := c.available(); err != nil {
		return err
	}
	if attemptID == "" {
		return errors.New("capacity: attemptID is required")
	}
	if len(dims) == 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = LeaseTTL
	}

	keys := make([]string, 0, len(dims)+1)
	argv := make([]any, 0, len(dims)*2+1)
	argv = append(argv, int64(ttl/time.Second))
	seen := make(map[string]struct{}, len(dims))
	for _, d := range dims {
		if d.Key == "" {
			return errors.New("capacity: dimension key is required")
		}
		if d.Amount <= 0 {
			return fmt.Errorf("capacity: dimension %q has non-positive amount %d", d.Key, d.Amount)
		}
		if d.Limit < 0 {
			return fmt.Errorf("capacity: dimension %q has negative limit %d", d.Key, d.Limit)
		}
		// 同一维度重复出现时合并，避免「同一 key 被检查两次、扣减两次」的歪曲
		if _, dup := seen[d.Key]; dup {
			return fmt.Errorf("capacity: duplicate dimension %q", d.Key)
		}
		seen[d.Key] = struct{}{}
		keys = append(keys, c.counterKey(d.Key))
		argv = append(argv, d.Limit, d.Amount)
	}
	keys = append(keys, c.leaseKey(attemptID))

	res, err := c.reserve.Run(ctx, c.rdb, keys, argv...).Result()
	if err != nil {
		return err
	}
	arr, ok := res.([]any)
	if !ok || len(arr) < 2 {
		return fmt.Errorf("capacity: unexpected reserve result %v", res)
	}
	if code, _ := arr[0].(int64); code != 1 {
		dim, _ := arr[1].(string)
		return &ErrExceeded{Dimension: dim}
	}
	return nil
}

// Release 归还本次预留。幂等：重复调用不会把计数减成负数。
func (c *Controller) Release(ctx context.Context, attemptID string) error {
	if err := c.available(); err != nil {
		return err
	}
	if attemptID == "" {
		return errors.New("capacity: attemptID is required")
	}
	_, err := c.release.Run(ctx, c.rdb, []string{c.leaseKey(attemptID)}).Result()
	return err
}

// Renew 续租；返回 false 表示租约已不存在（已过期或已被释放），调用方必须按失败处理。
func (c *Controller) Renew(ctx context.Context, attemptID string, ttl time.Duration) (bool, error) {
	if err := c.available(); err != nil {
		return false, err
	}
	if ttl <= 0 {
		ttl = LeaseTTL
	}
	res, err := c.renew.Run(ctx, c.rdb, []string{c.leaseKey(attemptID)}, int64(ttl/time.Second)).Result()
	if err != nil {
		return false, err
	}
	code, _ := res.(int64)
	return code == 1, nil
}

// Usage 读取某个维度当前的占用量（运维与验收脚本用）。
//
// 计数 key 不存在 == 占用为 0（Release 会把归零的 key 删掉、租约到期也会自动删除），
// 因此必须把 redis.Nil 归一为 0，而不是当成错误——首次实测就踩到了这一点。
func (c *Controller) Usage(ctx context.Context, dim string) (int64, error) {
	if err := c.available(); err != nil {
		return 0, err
	}
	usage, err := c.rdb.Get(ctx, c.counterKey(dim)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return usage, err
}

// Drop 清除某个维度的计数（仅用于测试与人工对账，不要在请求路径上调用）。
func (c *Controller) Drop(ctx context.Context, dims ...string) error {
	if err := c.available(); err != nil {
		return err
	}
	keys := make([]string, 0, len(dims))
	for _, d := range dims {
		keys = append(keys, c.counterKey(d))
	}
	if len(keys) == 0 {
		return nil
	}
	return c.rdb.Del(ctx, keys...).Err()
}
