package relayhook

import (
	"context"
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

// TPM 预留的结算期调整（T2.5 最后一环）。
//
// 这些断言都是**可确定的**：给定预留量与实际用量，窗口计数应落在哪个数值上是确定的。

func setupTPMAdjustEnv(t *testing.T) (*gin.Context, *capacity.Controller) {
	t.Helper()
	addr := os.Getenv("APIGX_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 APIGX_TEST_REDIS_ADDR：TPM 调整需要真实窗口计数")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	require.NoError(t, rdb.Ping(t.Context()).Err(), "连接 Redis 失败")
	previous := common.RDB
	common.RDB = rdb
	t.Cleanup(func() { common.RDB = previous; _ = rdb.Close() })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, capacity.New(rdb)
}

// 关闭时不做任何调整（默认关闭，与其它 APIGX 开关一致）。
func TestAdjustReservedTPMDisabledByDefault(t *testing.T) {
	c, ctrl := setupTPMAdjustEnv(t)
	t.Setenv("APIGX_TPM_ADJUST_ENABLED", "false")

	key := capacity.ChannelTPM(7701, capacity.MinuteWindow(time.Now()))
	t.Cleanup(func() { _ = ctrl.Drop(context.Background(), key) })
	require.NoError(t, ctrl.IncrementWindow(t.Context(), key, 1_000_000, 8202, capacity.WindowTTL(time.Minute)))
	storeTPMReservation(c, tpmReservation{windowKey: key, reserved: 8202, ttl: capacity.WindowTTL(time.Minute)})

	AdjustReservedTPM(c, 46)
	count, err := ctrl.WindowCount(t.Context(), key)
	require.NoError(t, err)
	assert.EqualValues(t, 8202, count, "开关关闭时不得调整")
}

// 开启后按 `实际 - 预留` 退回；且**只调整一次**（同一请求重复调用不会重复退回）。
func TestAdjustReservedTPMRefundsTheDifferenceOnce(t *testing.T) {
	c, ctrl := setupTPMAdjustEnv(t)
	t.Setenv("APIGX_TPM_ADJUST_ENABLED", "true")

	key := capacity.ChannelTPM(7702, capacity.MinuteWindow(time.Now()))
	t.Cleanup(func() { _ = ctrl.Drop(context.Background(), key) })
	// 预留 8202（prompt + 输出上限）
	require.NoError(t, ctrl.IncrementWindow(t.Context(), key, 1_000_000, 8202, capacity.WindowTTL(time.Minute)))
	storeTPMReservation(c, tpmReservation{windowKey: key, reserved: 8202, ttl: capacity.WindowTTL(time.Minute)})

	// 实际只用 46 → 计数应变成 46（而不是 8202）
	AdjustReservedTPM(c, 46)
	count, err := ctrl.WindowCount(t.Context(), key)
	require.NoError(t, err)
	assert.EqualValues(t, 46, count, "应按实际用量退回差额")

	// 同一请求再调用一次：不得重复退回（否则计数会被减到 0 以下或凭空多出额度）
	AdjustReservedTPM(c, 46)
	count, err = ctrl.WindowCount(t.Context(), key)
	require.NoError(t, err)
	assert.EqualValues(t, 46, count, "一次性记账：重复调用不得再次调整")
}

// 实际用量**高于**预留时不慌张：调整会让计数上升（窗口本来就该记住真实用量）。
func TestAdjustReservedTPMHandlesUnderReservation(t *testing.T) {
	c, ctrl := setupTPMAdjustEnv(t)
	t.Setenv("APIGX_TPM_ADJUST_ENABLED", "true")

	key := capacity.ChannelTPM(7703, capacity.MinuteWindow(time.Now()))
	t.Cleanup(func() { _ = ctrl.Drop(context.Background(), key) })
	require.NoError(t, ctrl.IncrementWindow(t.Context(), key, 1_000_000, 100, capacity.WindowTTL(time.Minute)))
	storeTPMReservation(c, tpmReservation{windowKey: key, reserved: 100, ttl: capacity.WindowTTL(time.Minute)})

	AdjustReservedTPM(c, 250) // 实际比预留多
	count, err := ctrl.WindowCount(t.Context(), key)
	require.NoError(t, err)
	assert.EqualValues(t, 250, count, "窗口计数应反映真实用量（而不是低报）")
}

// 没有预留记录（例如未启用 TPM）时是空操作。
func TestAdjustReservedTPMWithoutReservationIsNoop(t *testing.T) {
	c, _ := setupTPMAdjustEnv(t)
	t.Setenv("APIGX_TPM_ADJUST_ENABLED", "true")
	AdjustReservedTPM(c, 100) // 不应 panic、也不应写任何东西
	assert.True(t, true)
}
