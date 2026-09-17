package capacity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// 窗口计数（RPM/TPM 用）。
//
// # 为什么不能沿用「租约」模型
//
// 并发槽位是**请求结束就归还**的（`Reserve` / `Release` 成对出现）；
// 而速率计数必须在**整个窗口内保留**——如果请求结束时把它减回去，RPM 就永远数不到 2，
// 限速形同虚设。因此这里提供一个只增不减的原语：计数随窗口 TTL 自动过期。
//
// 代价是：**准入之后才失败的请求也会占用额度**。这对限速而言是正确的语义
// （它确实向上游发出过请求），因此不需要补偿。

// windowScript 原子地「先判断再累加」：计数不超过上限才累加，并刷新窗口 TTL。
//
// KEYS[1] = 窗口计数 key
// ARGV[1] = 上限，ARGV[2] = 本次累加量，ARGV[3] = TTL（秒）
// 返回 1 表示已计入；0 表示会超限、未计入（**不做任何写入**）。
const windowScript = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local amount = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local current = tonumber(redis.call('GET', key) or '0')
if current + amount > limit then
  return 0
end
redis.call('INCRBY', key, amount)
redis.call('EXPIRE', key, ttl)
return 1
`

// IncrementWindow 在给定窗口内累加计数；超限时返回 *ErrExceeded 且**不做任何写入**。
//
// key 应由 `ChannelRPM` / `ChannelTPM` 之类的构造器生成（其中包含窗口编号），
// ttl 应当略大于窗口长度，保证窗口结束后计数自动消失。
func (c *Controller) IncrementWindow(ctx context.Context, key string, limit, amount int64, ttl time.Duration) error {
	if err := c.available(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("capacity: window key is required")
	}
	if amount <= 0 {
		return fmt.Errorf("capacity: window amount must be positive (got %d)", amount)
	}
	if limit < 0 {
		return fmt.Errorf("capacity: window limit must not be negative (got %d)", limit)
	}
	if ttl <= 0 {
		return fmt.Errorf("capacity: window ttl must be positive (got %s)", ttl)
	}

	res, err := c.window.Run(ctx, c.rdb, []string{c.counterKey(key)},
		limit, amount, int64(ttl/time.Second)).Result()
	if err != nil {
		return err
	}
	if code, _ := res.(int64); code != 1 {
		return &ErrExceeded{Dimension: key}
	}
	return nil
}

// WindowTTL 返回给定窗口长度对应的计数存活时长（窗口长度 + 一倍余量）。
//
// 留余量是为了避免边界抖动：窗口刚切换时旧 key 立即消失会让"上一分钟"的统计不可查。
func WindowTTL(window time.Duration) time.Duration {
	if window <= 0 {
		window = time.Minute
	}
	return window * 2
}

// WindowCount 读取某窗口计数当前的累加值（运维与验收脚本用；不存在即 0）。
func (c *Controller) WindowCount(ctx context.Context, key string) (int64, error) {
	return c.Usage(ctx, key)
}

// adjustScript 原子地"带下限地调整"计数：结果**不会低于 0**。
//
// KEYS[1] = 窗口计数 key
// ARGV[1] = 调整量（可为负），ARGV[2] = TTL（秒）
// 返回调整后的值。
const adjustScript = `
local key = KEYS[1]
local delta = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local current = tonumber(redis.call('GET', key) or '0')
local next = current + delta
if next < 0 then
  next = 0
end
if next == 0 then
  redis.call('DEL', key)
  return 0
end
redis.call('SET', key, next)
redis.call('EXPIRE', key, ttl)
return next
`

// AdjustWindow 把窗口计数按 delta 调整（**可为负**），结果不会低于 0。
//
// # 用途：把"最坏情况预留"按实际用量退回去（T2.5）
//
// TPM 预留必须用**可执行的输出上限**（计划书 §9.2），但上限通常远高于实际输出。
// 若预留后不调整，渠道的每分钟 token 额度会被"最坏情况"长期占满，
// 结果是**明明没用到那么多也被限流**。
//
// 因此在拿到真实 usage 之后调用本函数把差额退回去：
//
//	AdjustWindow(ctx, key, actualTokens-reservedTokens, ttl)
//
// 下限 0 保证：即使发生重复调整（例如重试路径回填两次），
// 计数也不会变成负数而"凭空多出额度"。
func (c *Controller) AdjustWindow(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	if err := c.available(); err != nil {
		return 0, err
	}
	if key == "" {
		return 0, errors.New("capacity: window key is required")
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("capacity: window ttl must be positive (got %s)", ttl)
	}
	if delta == 0 {
		count, err := c.Usage(ctx, key)
		return count, err
	}
	res, err := c.adjust.Run(ctx, c.rdb, []string{c.counterKey(key)},
		delta, int64(ttl/time.Second)).Result()
	if err != nil {
		return 0, err
	}
	if count, ok := res.(int64); ok {
		return count, nil
	}
	return 0, nil
}
