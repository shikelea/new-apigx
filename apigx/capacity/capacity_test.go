package capacity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 容量账本必须跑在**真实 Redis** 上验证：Lua 的原子性与 TTL 语义是 mock 无法证明的。
// 未配置时明确 SKIP 并打印原因，不允许当作通过。
//
//	APIGX_TEST_REDIS_ADDR=127.0.0.1:6379
func newTestController(t *testing.T) *Controller {
	t.Helper()
	addr := os.Getenv("APIGX_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 APIGX_TEST_REDIS_ADDR：容量账本未在真实 Redis 上验证（不允许当作通过）")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15}) // 用 15 号库隔离测试数据
	require.NoError(t, rdb.Ping(context.Background()).Err(), "连接 Redis 失败")
	t.Cleanup(func() { _ = rdb.Close() })

	// 每个用例独立前缀，互不污染。
	// 注意：计数带 TTL，上一次运行留下的键可能仍存活，因此必须**先清理再使用**，
	// 否则用例之间、以及多次运行之间会互相干扰（首次实测就踩到了这一点）。
	prefix := fmt.Sprintf("apigx:captest:%s:", t.Name())
	cleanup := func() {
		ctx := context.Background()
		keys, err := rdb.Keys(ctx, prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	return NewWithPrefix(rdb, prefix)
}

func TestReserveWithinLimits(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	dim := ChannelConcurrency(101)

	require.NoError(t, c.Reserve(ctx, "att-1", []Dimension{{Key: dim, Limit: 3, Amount: 1}}, time.Minute))
	require.NoError(t, c.Reserve(ctx, "att-2", []Dimension{{Key: dim, Limit: 3, Amount: 1}}, time.Minute))

	usage, err := c.Usage(ctx, dim)
	require.NoError(t, err)
	assert.EqualValues(t, 2, usage, "两次预留应占 2 个槽位")
}

// 多维度必须「全有或全无」：任一维度不足时，**已通过的维度也不能留下扣减**。
func TestReserveIsAllOrNothing(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	roomy := ChannelConcurrency(102)
	tight := UpstreamAccountConcurrency(202)

	// 先把 tight 占满
	require.NoError(t, c.Reserve(ctx, "att-fill", []Dimension{{Key: tight, Limit: 1, Amount: 1}}, time.Minute))

	err := c.Reserve(ctx, "att-2", []Dimension{
		{Key: roomy, Limit: 10, Amount: 1}, // 这一维本来有空间
		{Key: tight, Limit: 1, Amount: 1},  // 这一维已满
	}, time.Minute)

	var exceeded *ErrExceeded
	require.ErrorAs(t, err, &exceeded, "必须返回 *ErrExceeded 供调用方重选候选")
	assert.Contains(t, exceeded.Dimension, "upaccount:202", "应指出是哪一维不足")

	usage, usageErr := c.Usage(ctx, roomy)
	require.NoError(t, usageErr)
	assert.EqualValues(t, 0, usage, "未生效的维度绝不能留下扣减（这正是 Lua 先全查再全扣的意义）")
}

func TestReleaseIsIdempotentAndNeverNegative(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	dim := ChannelConcurrency(103)

	require.NoError(t, c.Reserve(ctx, "att-1", []Dimension{{Key: dim, Limit: 5, Amount: 2}}, time.Minute))
	require.NoError(t, c.Release(ctx, "att-1"))

	usage, err := c.Usage(ctx, dim)
	require.NoError(t, err)
	assert.EqualValues(t, 0, usage)

	// 再释放两次：不得变成负数，也不得报错
	require.NoError(t, c.Release(ctx, "att-1"))
	require.NoError(t, c.Release(ctx, "att-1"))
	usage, err = c.Usage(ctx, dim)
	require.NoError(t, err)
	assert.EqualValues(t, 0, usage, "重复释放不得把计数减成负数")
}

func TestLeaseExpiryRecoversCapacity(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	dim := ChannelConcurrency(104)

	require.NoError(t, c.Reserve(ctx, "att-1", []Dimension{{Key: dim, Limit: 1, Amount: 1}}, 1*time.Second))

	// 立刻再抢应当失败
	err := c.Reserve(ctx, "att-2", []Dimension{{Key: dim, Limit: 1, Amount: 1}}, time.Minute)
	require.Error(t, err, "槽位被占满时必须拒绝")

	// 等租约过期：进程崩溃后靠 TTL 自动回收，不会永久泄漏槽位
	time.Sleep(1200 * time.Millisecond)
	usage, usageErr := c.Usage(ctx, dim)
	require.NoError(t, usageErr)
	assert.EqualValues(t, 0, usage, "租约到期后计数应自动回落")
	require.NoError(t, c.Reserve(ctx, "att-2", []Dimension{{Key: dim, Limit: 1, Amount: 1}}, time.Minute))
}

func TestRenewExtendsLease(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	dim := ChannelConcurrency(105)

	require.NoError(t, c.Reserve(ctx, "att-1", []Dimension{{Key: dim, Limit: 1, Amount: 1}}, 2*time.Second))
	time.Sleep(1200 * time.Millisecond)
	ok, err := c.Renew(ctx, "att-1", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, ok, "租约仍存在时应能续租")

	time.Sleep(1500 * time.Millisecond) // 已超过原租约，但未超过续租后的
	usage, usageErr := c.Usage(ctx, dim)
	require.NoError(t, usageErr)
	assert.EqualValues(t, 1, usage, "续租生效后不应在原到期时间回落")

	// 释放后不能再续租
	require.NoError(t, c.Release(ctx, "att-1"))
	ok, err = c.Renew(ctx, "att-1", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "租约不存在时必须返回 false，调用方据此停止续租")
}

// 并发争抢是这一整套机制存在的理由：**limit 个槽位最多被 limit 个调用占用**。
// 「先读计数再发送」的写法在这里必然失败。
func TestConcurrentReserveNeverExceedsLimit(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	dim := ChannelConcurrency(106)
	const limit = 5
	const racers = 40

	var granted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := c.Reserve(ctx, fmt.Sprintf("att-%d", i), []Dimension{{Key: dim, Limit: limit, Amount: 1}}, time.Minute)
			if err == nil {
				granted.Add(1)
				return
			}
			var exceeded *ErrExceeded
			assert.ErrorAs(t, err, &exceeded)
		}(i)
	}
	wg.Wait()

	assert.EqualValues(t, limit, granted.Load(), "并发下必须恰好发放 limit 个槽位，不能超发")
	usage, err := c.Usage(ctx, dim)
	require.NoError(t, err)
	assert.EqualValues(t, limit, usage)
}

func TestRejectsInvalidInput(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	dim := ChannelConcurrency(107)

	require.Error(t, c.Reserve(ctx, "", []Dimension{{Key: dim, Limit: 1, Amount: 1}}, time.Minute), "attemptID 必填")
	require.Error(t, c.Reserve(ctx, "a", []Dimension{{Key: "", Limit: 1, Amount: 1}}, time.Minute), "维度 key 必填")
	require.Error(t, c.Reserve(ctx, "a", []Dimension{{Key: dim, Limit: 1, Amount: 0}}, time.Minute), "amount 必须为正")
	require.Error(t, c.Reserve(ctx, "a", []Dimension{
		{Key: dim, Limit: 1, Amount: 1},
		{Key: dim, Limit: 1, Amount: 1},
	}, time.Minute), "同一维度不得重复出现（否则会被重复扣减）")
}

// Redis 不可用时必须拒绝准入，而不是放行——计划书 §9.2：无法安全判断共享额度时停止新调用。
func TestReserveFailsClosedWithoutRedis(t *testing.T) {
	c := New(nil)
	err := c.Reserve(context.Background(), "att-1", []Dimension{{Key: ChannelConcurrency(108), Limit: 1, Amount: 1}}, time.Minute)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRedisUnavailable), "错误必须是 ErrRedisUnavailable 以便上层停止准入")
}

// ---- 窗口计数（RPM/TPM） ----

// 窗口计数只增不减：**请求结束不归还**，否则 RPM 永远数不到 2、限速形同虚设。
func TestIncrementWindowCountsUpToLimit(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	key := ChannelRPM(4242, MinuteWindow(time.Now()))

	require.NoError(t, c.IncrementWindow(ctx, key, 2, 1, WindowTTL(time.Minute)))
	require.NoError(t, c.IncrementWindow(ctx, key, 2, 1, WindowTTL(time.Minute)))

	// 第三次超限：必须返回 *ErrExceeded **且不写入**
	err := c.IncrementWindow(ctx, key, 2, 1, WindowTTL(time.Minute))
	var exceeded *ErrExceeded
	require.ErrorAs(t, err, &exceeded)
	assert.Contains(t, exceeded.Dimension, "rpm")

	count, err := c.WindowCount(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count, "超限的那次不得计入")
}

// 计数随窗口 TTL 自动过期：这是"窗口"语义的实现方式（不需要额外清理任务）。
func TestWindowCountExpires(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	key := ChannelRPM(4243, MinuteWindow(time.Now()))

	require.NoError(t, c.IncrementWindow(ctx, key, 5, 1, 1*time.Second))
	count, err := c.WindowCount(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)

	time.Sleep(1200 * time.Millisecond)
	count, err = c.WindowCount(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count, "窗口结束后计数必须自动消失")
}

// 参数校验：非法入参不得静默通过（否则限速会被配置错值悄悄关掉）。
func TestIncrementWindowRejectsInvalidInput(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()
	key := ChannelRPM(4244, MinuteWindow(time.Now()))

	require.Error(t, c.IncrementWindow(ctx, "", 1, 1, time.Minute))
	require.Error(t, c.IncrementWindow(ctx, key, 1, 0, time.Minute), "累加量必须为正")
	require.Error(t, c.IncrementWindow(ctx, key, -1, 1, time.Minute), "上限不得为负")
	require.Error(t, c.IncrementWindow(ctx, key, 1, 1, 0), "TTL 必须为正")
}

// 不同窗口使用不同的 key，因此跨窗口互不影响（按分钟分桶）。
func TestWindowKeysAreBucketedByMinute(t *testing.T) {
	now := time.Now()
	first := ChannelRPM(4245, MinuteWindow(now))
	second := ChannelRPM(4245, MinuteWindow(now.Add(2*time.Minute)))
	assert.NotEqual(t, first, second, "不同分钟必须落在不同的计数 key 上")
}
