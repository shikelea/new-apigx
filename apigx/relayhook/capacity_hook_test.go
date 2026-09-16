package relayhook

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 容量接线测试。
//
// 需要真实 Redis（Lua 原子性无法用 mock 证明）：
//
//	APIGX_TEST_REDIS_ADDR=127.0.0.1:6379
//
// 结构约定：
//   - setupRedis(t) 每个用例**只调用一次**：它替换 common.RDB 并注册关闭。
//     如果在同一用例里多次替换客户端，先注册的清理会最后执行，届时客户端已关闭
//     （首次实测就踩到了这个：清理阶段的 Drop 报 "client is closed"）。
//   - newCtx() 只造 gin 上下文，可多次调用，不碰连接。
func setupRedis(t *testing.T) {
	t.Helper()
	addr := os.Getenv("APIGX_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 APIGX_TEST_REDIS_ADDR：容量接线未在真实 Redis 上验证（不允许当作通过）")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	require.NoError(t, rdb.Ping(t.Context()).Err(), "连接 Redis 失败")
	previous := common.RDB
	common.RDB = rdb
	// 清理顺序：先恢复指针（最后注册先执行），再关闭客户端
	t.Cleanup(func() { _ = rdb.Close() })
	t.Cleanup(func() { common.RDB = previous })
	gin.SetMode(gin.TestMode)
}

func newCtx() *gin.Context {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// cleanupChannel 清掉用例用的渠道计数；必须在 common.RDB 仍可用时调用，
// 因此放在用例主体的最后（而不是最早注册的 Cleanup 里）。
func cleanupChannel(t *testing.T, channelID int) {
	t.Helper()
	require.NoError(t, capacity.New(common.RDB).Drop(t.Context(), capacity.ChannelConcurrency(int64(channelID))))
}

// 默认关闭时不得施加任何约束（这就是「默认不改变既有链路」的保证）。
func TestAcquireIsNoopWhenDisabled(t *testing.T) {
	setupRedis(t)
	t.Cleanup(func() { Configure(false, 0) })
	Configure(false, 1)

	c := newCtx()
	lease, ok := Acquire(c, 90001)
	assert.True(t, ok)
	assert.Nil(t, lease, "关闭时不应产生租约")
	assert.False(t, c.IsAborted())
}

// 未配置上限（0）时不施加约束——过渡行为，接入 capacity_policies 后必须变成显式四态。
func TestAcquireIsNoopWhenLimitUnset(t *testing.T) {
	setupRedis(t)
	t.Cleanup(func() { Configure(false, 0) })
	Configure(true, 0)

	lease, ok := Acquire(newCtx(), 90002)
	assert.True(t, ok)
	assert.Nil(t, lease)
}

// 启用且配置了上限：第一个请求拿到租约，第二个被 429 拒绝并带 Retry-After。
func TestAcquireRejectsWhenFull(t *testing.T) {
	setupRedis(t)
	const channelID = 90003
	t.Cleanup(func() { Configure(false, 0) })
	Configure(true, 1)

	c1 := newCtx()
	lease1, ok := Acquire(c1, channelID)
	require.True(t, ok)
	require.NotNil(t, lease1, "第一个请求应拿到租约")

	c2 := newCtx()
	lease2, ok := Acquire(c2, channelID)
	assert.False(t, ok, "满载时必须拒绝")
	assert.Nil(t, lease2)
	assert.True(t, c2.IsAborted())
	assert.Equal(t, http.StatusTooManyRequests, c2.Writer.Status())
	assert.NotEmpty(t, c2.Writer.Header().Get("Retry-After"), "429 应带 Retry-After")

	// 释放后应能重新获取
	lease1.Release()
	c3 := newCtx()
	lease3, ok := Acquire(c3, channelID)
	assert.True(t, ok)
	require.NotNil(t, lease3)
	lease3.Release()

	cleanupChannel(t, channelID)
}

// 重复释放必须幂等：计数回到 0 且不会变负。
func TestReleaseIsIdempotent(t *testing.T) {
	setupRedis(t)
	const channelID = 90004
	t.Cleanup(func() { Configure(false, 0) })
	Configure(true, 2)

	lease, ok := Acquire(newCtx(), channelID)
	require.True(t, ok)
	require.NotNil(t, lease)

	lease.Release()
	lease.Release()
	lease.Release()

	usage, err := capacity.New(common.RDB).Usage(t.Context(), capacity.ChannelConcurrency(int64(channelID)))
	require.NoError(t, err)
	assert.EqualValues(t, 0, usage, "重复释放不得把计数减成负数")

	cleanupChannel(t, channelID)
}

// Redis 不可用时必须拒绝准入（503），而不是放行。
func TestAcquireFailsClosedWhenLedgerUnavailable(t *testing.T) {
	setupRedis(t)
	t.Cleanup(func() { Configure(false, 0) })
	Configure(true, 1)

	previous := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previous })

	c := newCtx()
	lease, ok := Acquire(c, 90005)
	assert.False(t, ok, "账本不可用时必须拒绝新准入")
	assert.Nil(t, lease)
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
}

// 长流续租：租约超过原到期时间后，若续租有效则仍持有槽位。
//
// TTL 必须用**整秒**且明显大于续租间隔（ttl/3）：TTL 以秒为单位下发，
// 亚秒值会被截断，导致租约比续租间隔还短——首次实测踩到过这个。
func TestRenewKeepsLeaseAlive(t *testing.T) {
	setupRedis(t)
	const channelID = 90006
	t.Cleanup(func() { Configure(false, 0) })
	Configure(true, 1)

	previousTTL := leaseTTL
	leaseTTL = 3 * time.Second // 续租间隔 = 1s
	t.Cleanup(func() { leaseTTL = previousTTL })

	lease, ok := Acquire(newCtx(), channelID)
	require.True(t, ok)
	require.NotNil(t, lease)

	time.Sleep(4500 * time.Millisecond) // 超过原租约 1.5 倍
	usage, err := capacity.New(common.RDB).Usage(t.Context(), capacity.ChannelConcurrency(int64(channelID)))
	require.NoError(t, err)
	assert.EqualValues(t, 1, usage, "续租应让长流继续持有槽位")

	lease.Release()
	cleanupChannel(t, channelID)
}
