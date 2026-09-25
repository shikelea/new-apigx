package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// APIGX 每日预算守卫（T1.7）的单元测试。
//
// 需要真实 Redis：窗口计数的原子性（"先判断再累加"）无法用 mock 证明。
//
//	APIGX_TEST_REDIS_ADDR=127.0.0.1:6379
func setupBudgetGuard(t *testing.T) *gin.Context {
	t.Helper()
	addr := os.Getenv("APIGX_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 APIGX_TEST_REDIS_ADDR：预算守卫未在真实 Redis 上验证（不允许当作通过）")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	require.NoError(t, rdb.Ping(t.Context()).Err(), "连接 Redis 失败")
	previous := common.RDB
	common.RDB = rdb
	t.Cleanup(func() { common.RDB = previous; _ = rdb.Close() })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func withBudgetEnv(t *testing.T, micro string) {
	t.Helper()
	previous, had := os.LookupEnv("APIGX_DAILY_BUDGET_MICRO")
	require.NoError(t, os.Setenv("APIGX_DAILY_BUDGET_MICRO", micro))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("APIGX_DAILY_BUDGET_MICRO", previous)
		} else {
			_ = os.Unsetenv("APIGX_DAILY_BUDGET_MICRO")
		}
	})
}

// 预算按「累加金额」判断：累计不超限放行，超出即拒；且**计数不因请求结束而归还**。
func TestDailyBudgetAccumulatesAndRejects(t *testing.T) {
	c := setupBudgetGuard(t)
	withBudgetEnv(t, "100")
	const userID = 88001
	cleanup := func() {
		ctrl := capacity.New(common.RDB)
		require.NoError(t, ctrl.Drop(context.Background(),
			capacity.UserDailyBudgetMicro(userID, dailyBudgetWindow(time.Now()))))
	}
	cleanup()
	t.Cleanup(cleanup)

	relayInfo := &relaycommon.RelayInfo{UserId: userID}

	// 60 微美元：0+60 <= 100 → 放行
	require.Nil(t, CheckAPIGXDailyBudget(c, relayInfo, 60))

	// 再 60：60+60 > 100 → 拒绝（且返回 403，不当成上游故障）
	err := CheckAPIGXDailyBudget(c, relayInfo, 60)
	require.NotNil(t, err)
	assert.Equal(t, http.StatusForbidden, err.StatusCode)

	// 30：60+30 = 90 <= 100 → 放行，证明是**累加**而不是"单次金额超过上限才拒"
	require.Nil(t, CheckAPIGXDailyBudget(c, relayInfo, 30))

	// 再 30：90+30 > 100 → 拒绝
	assert.NotNil(t, CheckAPIGXDailyBudget(c, relayInfo, 30))
}

// 未配置预算（0）时不施加约束——未配置是待补的过渡状态，不是"零预算"。
func TestDailyBudgetDisabledWhenUnset(t *testing.T) {
	c := setupBudgetGuard(t)
	withBudgetEnv(t, "0")
	relayInfo := &relaycommon.RelayInfo{UserId: 88002}
	require.Nil(t, CheckAPIGXDailyBudget(c, relayInfo, 1_000_000))
}

// 预扣为 0 时也要记一次最小量：否则零成本请求会完全绕过预算统计。
func TestDailyBudgetCountsZeroCostRequests(t *testing.T) {
	c := setupBudgetGuard(t)
	withBudgetEnv(t, "2")
	const userID = 88003
	cleanup := func() {
		ctrl := capacity.New(common.RDB)
		require.NoError(t, ctrl.Drop(context.Background(),
			capacity.UserDailyBudgetMicro(userID, dailyBudgetWindow(time.Now()))))
	}
	cleanup()
	t.Cleanup(cleanup)
	relayInfo := &relaycommon.RelayInfo{UserId: userID}

	// 三次零成本请求：每次记 1，第三次应被拒（2 是上限）
	require.Nil(t, CheckAPIGXDailyBudget(c, relayInfo, 0))
	require.Nil(t, CheckAPIGXDailyBudget(c, relayInfo, 0))
	assert.NotNil(t, CheckAPIGXDailyBudget(c, relayInfo, 0), "零成本请求也必须计入每日预算")
}

// 账本不可用时必须**失败关闭**（拒绝），而不是放行。
func TestDailyBudgetFailsClosedWithoutRedis(t *testing.T) {
	c := setupBudgetGuard(t)
	withBudgetEnv(t, "100")
	previous := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previous })

	err := CheckAPIGXDailyBudget(c, &relaycommon.RelayInfo{UserId: 88004}, 10)
	require.NotNil(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
}

// 不同用户互不影响（预算是**使用者**维度）。
func TestDailyBudgetIsPerUser(t *testing.T) {
	c := setupBudgetGuard(t)
	withBudgetEnv(t, "10")
	first := &relaycommon.RelayInfo{UserId: 88005}
	second := &relaycommon.RelayInfo{UserId: 88006}
	cleanup := func() {
		ctrl := capacity.New(common.RDB)
		require.NoError(t, ctrl.Drop(context.Background(),
			capacity.UserDailyBudgetMicro(88005, dailyBudgetWindow(time.Now())),
			capacity.UserDailyBudgetMicro(88006, dailyBudgetWindow(time.Now()))))
	}
	cleanup()
	t.Cleanup(cleanup)

	require.Nil(t, CheckAPIGXDailyBudget(c, first, 10))
	require.Nil(t, CheckAPIGXDailyBudget(c, second, 10), "另一个使用者不应受前者的用量影响")
}
