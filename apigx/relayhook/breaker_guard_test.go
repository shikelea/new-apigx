package relayhook

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 熔断的实时生效（T2.8 生效路径）测试。
//
// 这一层只做"挡不挡"的判断，判定逻辑在 `apigx/routing`（有自己的单元测试）。
// 这里验证的是接进准入点后的行为：打开的供给被挡住且**可重试**（换渠道而不是判死）、
// 平台拒绝不触发、开关可关、判定有短 TTL 缓存。

type breakerTestEnv struct {
	db            *gorm.DB
	baseChannelID int
	shareChannel  *apigxmodel.Channel
	offeringID    int64
	seq           atomic.Int64
}

var breakerSeq atomic.Int64

func newBreakerTestEnv(t *testing.T) *breakerTestEnv {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_breaker_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))

	env := &breakerTestEnv{db: db, baseChannelID: int(800000 + breakerSeq.Add(1))}
	const owner int64 = 991001

	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "熔断组", PublicID: "bg-" + t.Name()}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	channel := &apigxmodel.Channel{
		PublicID: "bc-" + t.Name(), OwnerUserID: owner, ShareGroupID: group.ID,
		Name: "熔断渠道", BaseChannelID: int64(env.baseChannelID),
	}
	require.NoError(t, apigxmodel.CreateChannel(db, channel))
	env.shareChannel = channel

	offering := &apigxmodel.ModelOffering{ChannelID: channel.ID, UpstreamModelID: "breaker-model"}
	require.NoError(t, apigxmodel.CreateModelOffering(db, owner, offering))
	env.offeringID = offering.ID
	return env
}

func (e *breakerTestEnv) newContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// recordFailures 写入 n 条**上游失败**样本（同一分钟桶）。
func (e *breakerTestEnv) recordFailures(t *testing.T, n int) {
	t.Helper()
	now := common.GetTimestamp()
	for range n {
		require.NoError(t, apigxmodel.RecordQualitySample(e.db, apigxmodel.QualitySample{
			OfferingID: e.offeringID, BucketTs: now - now%60,
			Outcome: apigxmodel.QualityOutcomeUpstream, LatencyMs: 100,
		}))
	}
}

// recordPlatformRejects 写入 n 条**平台拒绝**样本（我们不希望它触发熔断）。
func (e *breakerTestEnv) recordPlatformRejects(t *testing.T, n int) {
	t.Helper()
	now := common.GetTimestamp()
	for range n {
		require.NoError(t, apigxmodel.RecordQualitySample(e.db, apigxmodel.QualitySample{
			OfferingID: e.offeringID, BucketTs: now - now%60,
			Outcome: apigxmodel.QualityOutcomeRejected, LatencyMs: 20,
		}))
	}
}

// 熔断打开的供给必须被挡住，让底座换一条渠道；平台拒绝不触发；开关可关。
func TestBreakerGuardBlocksOpenOffering(t *testing.T) {
	env := newBreakerTestEnv(t)
	t.Setenv("APIGX_BREAKER_ENABLED", "true")
	InvalidateBreakerCache()
	t.Cleanup(InvalidateBreakerCache)

	c := env.newContext(t)

	// ① 还没有样本：不熔断（样本不足不给结论）
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"))

	// ② 制造"上游持续失败"：20 次上游失败、无成功
	env.recordFailures(t, 20)
	InvalidateBreakerCache()
	err := CheckBreakerAllows(c, env.baseChannelID, "breaker-model")
	require.NotNil(t, err, "熔断打开时必须挡住该供给")
	assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	// **可重试**：交给底座换渠道，而不是把请求判死
	assert.False(t, types.IsSkipRetryError(err), "熔断应当可重试（换一条渠道），而不是终止请求")

	// ④ 开关关闭时不生效
	t.Setenv("APIGX_BREAKER_ENABLED", "false")
	InvalidateBreakerCache()
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"), "开关关闭时不生效")
}

// **归因红线**：平台自身的拒绝（准入/容量/配额）与用户取消**不得触发熔断**。
//
// 注意必须用**干净的环境**验证这一点：如果先写了上游失败再补平台拒绝，
// 那些失败仍在同一个分钟桶里、失败率自然还是高的——那验证不了归因是否正确
// （初版就是这么写的，读起来像在测归因，实际在测"旧失败还在不在"）。
func TestBreakerIgnoresPlatformRejections(t *testing.T) {
	env := newBreakerTestEnv(t)
	t.Setenv("APIGX_BREAKER_ENABLED", "true")
	InvalidateBreakerCache()
	t.Cleanup(InvalidateBreakerCache)
	c := env.newContext(t)

	env.recordPlatformRejects(t, 40)
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"),
		"只有平台拒绝时绝不允许熔断：那会把健康渠道误判为上游故障")

	// 补上真正的上游失败后才熔断（证明阈值本身是生效的，上一步不是因为"从来没生效"）
	env.recordFailures(t, 20)
	InvalidateBreakerCache()
	assert.NotNil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"))
}

// 判定带短 TTL 缓存（否则每次尝试都要聚合一次统计桶）。
func TestBreakerVerdictIsCached(t *testing.T) {
	env := newBreakerTestEnv(t)
	t.Setenv("APIGX_BREAKER_ENABLED", "true")
	InvalidateBreakerCache()
	t.Cleanup(InvalidateBreakerCache)
	c := env.newContext(t)

	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"))

	// 缓存已记录"关闭"：即使立刻写入大量失败，TTL 内仍按缓存放行
	env.recordFailures(t, 20)
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"),
		"TTL 内应命中缓存（熔断状态按分钟窗口变化，30 秒滞后可接受）")

	// 失效后立即反映真实状态
	InvalidateBreakerCache()
	assert.NotNil(t, CheckBreakerAllows(c, env.baseChannelID, "breaker-model"))

	// TTL 到期的行为由缓存有效期决定；这里顺带确认缓存确实有时间上限
	assert.Positive(t, breakerCacheTTL, "缓存必须有 TTL")
	assert.LessOrEqual(t, breakerCacheTTL, 60*time.Second, "TTL 不应过长（熔断要能较快反映真实状态）")
}

// 平台自建渠道（没有对应供给）不参与熔断。
func TestBreakerGuardSkipsNonShareChannels(t *testing.T) {
	env := newBreakerTestEnv(t)
	t.Setenv("APIGX_BREAKER_ENABLED", "true")
	c := env.newContext(t)
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID+999, "breaker-model"), "非分享渠道不熔断")
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, "no-such-model"), "没有供给的模型不熔断")
	assert.Nil(t, CheckBreakerAllows(c, env.baseChannelID, ""), "缺模型名不熔断")
}
