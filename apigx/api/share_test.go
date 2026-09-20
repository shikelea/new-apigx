package apigxapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/apigx/capacity"
	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	apigxrouting "github.com/QuantumNous/new-api/apigx/routing"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 分享者接口测试（SQLite + httptest，无需外部依赖）。
//
// 用 `X-Test-User` 头模拟底座的 UserAuth（它只负责把 owner 写进上下文 "id"），
// 这样可以在同一个测试里切换身份，专门验证**归属隔离**。

const (
	testKeyHex  = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	plainSecret = "sk-upstream-PLAINTEXT-must-never-leak"
)

// baseChannelSeq 为每个用例分配唯一的底座渠道 ID（base_channel_id 上有唯一索引）。
var baseChannelSeq atomic.Int64

func nextBaseChannelID() int64 { return 100000 + baseChannelSeq.Add(1) }

type testEnv struct {
	db     *gorm.DB
	engine *gin.Engine
}

func newTestEnv(t *testing.T, withMasterKey bool) *testEnv {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_api_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrateTestEnv(t, db)
	return configureTestEnv(t, db, withMasterKey)
}

func migrateTestEnv(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, apigxmodel.Migrate(db))
	// 投影会调用底座的 channel.Update()（并同步 abilities），因此接口层测试也要有底座表，
	// MySQL 测试库的服务端默认引擎是 MyISAM，必须显式要求 InnoDB 才能验证事务回滚。
	migration := db
	if db.Dialector.Name() == "mysql" {
		migration = db.Set("gorm:table_options", "ENGINE=InnoDB")
	}
	require.NoError(t, migration.AutoMigrate(&basemodel.Channel{}, &basemodel.Ability{}))
}

func configureTestEnv(t *testing.T, db *gorm.DB, withMasterKey bool) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// 底座的全局 DB 必须指向同一个库（生产由启动路径设置）。
	previousDB := basemodel.DB
	basemodel.DB = db
	t.Cleanup(func() { basemodel.DB = previousDB })

	// 主密钥
	previous, had := os.LookupEnv(secretbox.KeyEnvVar)
	if withMasterKey {
		require.NoError(t, os.Setenv(secretbox.KeyEnvVar, testKeyHex))
	} else {
		require.NoError(t, os.Unsetenv(secretbox.KeyEnvVar))
	}
	secretbox.ResetKeyCacheForTest()
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(secretbox.KeyEnvVar, previous)
		} else {
			_ = os.Unsetenv(secretbox.KeyEnvVar)
		}
		secretbox.ResetKeyCacheForTest()
	})

	engine := gin.New()
	api := engine.Group("/api")
	api.Use(func(c *gin.Context) {
		// 默认给管理员角色：审核类动作（发布/驳回）要求平台侧身份
		c.Set("role", common.RoleAdminUser)
		if raw := c.GetHeader("X-Test-Role"); raw != "" {
			if role, convErr := strconv.Atoi(raw); convErr == nil {
				c.Set("role", role)
			}
		}
		if raw := c.GetHeader("X-Test-User"); raw != "" {
			if id, convErr := strconv.Atoi(raw); convErr == nil {
				c.Set("id", id)
			}
		}
	})
	Register(api.Group(""), db)
	return &testEnv{db: db, engine: engine}
}

func (e *testEnv) do(t *testing.T, method, path string, userID int64, body string) (*httptest.ResponseRecorder, map[string]any) {
	return e.doWithRole(t, method, path, userID, body, 0)
}

// doWithRole 允许指定角色（0 表示使用默认的管理员角色）。
func (e *testEnv) doWithRole(t *testing.T, method, path string, userID int64, body string, role int) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID > 0 {
		req.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
	}
	if role > 0 {
		req.Header.Set("X-Test-Role", strconv.Itoa(role))
	}
	rec := httptest.NewRecorder()
	e.engine.ServeHTTP(rec, req)

	var parsed map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	}
	return rec, parsed
}

func (e *testEnv) createGroup(t *testing.T, userID int64, name string) int64 {
	t.Helper()
	rec, body := e.do(t, http.MethodPost, "/api/share-groups", userID, `{"name":"`+name+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	id, ok := data["id"].(float64)
	require.True(t, ok, "创建分组应返回 id：%s", rec.Body.String())
	// 新建的**公开**分组初始为「待审核」（U-1 闸门：未发布的分组对外不可见、不可被路由）。
	// 夹具代表"平台已审核通过"的状态，否则所有依赖广场/路由的用例都会因为闸门而看不到数据。
	require.NoError(t, apigxmodel.SetShareGroupPublishStatus(e.db, int64(id), apigxmodel.ShareGroupPublishPublished))
	return int64(id)
}

func (e *testEnv) createChannel(t *testing.T, userID, groupID, baseChannelID int64) int64 {
	t.Helper()
	// 绑定既有底座渠道现在是平台级动作，并且服务端会验证该行真实存在。
	// 旧夹具只传一个虚构 ID；这里补齐底座事实，避免测试绕过生产契约。
	require.NoError(t, e.db.FirstOrCreate(&basemodel.Channel{
		Id: int(baseChannelID), Name: "测试底座渠道", Key: "fixture-key", Status: common.ChannelStatusEnabled,
	}, "id = ?", baseChannelID).Error)
	payload := `{"share_group_id":` + strconv.FormatInt(groupID, 10) +
		`,"base_channel_id":` + strconv.FormatInt(baseChannelID, 10) +
		`,"name":"主线路","protocol":"openai","base_url":"http://stub"}`
	rec, body := e.do(t, http.MethodPost, "/api/channels", userID, payload)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	id, ok := data["id"].(float64)
	require.True(t, ok)
	return int64(id)
}

func TestOwnerUsageSummaryUsesUsageEventsAndBoundsQueries(t *testing.T) {
	env := newTestEnv(t, true)
	const (
		owner int64 = 10901
		other int64 = 10902
		now   int64 = 1_800_200_000
	)
	groupID := env.createGroup(t, owner, "用量汇总组")
	channelID := env.createChannel(t, owner, groupID, nextBaseChannelID())
	key := &apigxmodel.UpstreamKey{
		ChannelID:   channelID,
		StableKeyID: "usage-api-key",
		Name:        "统计 Key",
		Status:      apigxmodel.UpstreamKeyStatusEnabled,
	}
	require.NoError(t, env.db.Create(key).Error)
	request := &apigxmodel.Request{RequestUID: "usage-api-request", ApiModelID: "usage-api-model", CreatedAt: now}
	require.NoError(t, env.db.Create(request).Error)
	require.NoError(t, env.db.Create(&apigxmodel.Attempt{
		AttemptUID:        "usage-api-attempt",
		RequestID:         request.ID,
		SelectedChannelID: channelID,
		UpstreamKeyID:     key.ID,
		State:             apigxmodel.AttemptStateSucceeded,
		HttpStatus:        http.StatusOK,
		StartedAt:         now,
		TtftMs:            75,
	}).Error)
	require.NoError(t, apigxmodel.RecordUsageEvent(env.db, &apigxmodel.UsageEvent{
		EventUID:      "usage-api-event",
		Kind:          apigxmodel.UsageEventKindUsage,
		OwnerUserID:   owner,
		ApiModelID:    "usage-api-model",
		ChannelID:     channelID,
		UpstreamKeyID: key.ID,
		Capability:    apigxmodel.CapabilityImageGeneration,
		Dimension:     apigxmodel.UsageDimensionImageCount,
		QuantityMilli: 2_000,
		UnitScale:     apigxmodel.UsageUnitScalePerImage,
		Trust:         apigxmodel.UsageTrustLocalEstimated,
		CostState:     apigxmodel.UsageCostStateReferenceEstimated,
		CostUsdMicro:  3_500,
		OccurredAt:    now,
		CreatedAt:     now,
	}))

	path := "/api/usage/summary?group_by=channel&start=" + strconv.FormatInt(now-10, 10) +
		"&end=" + strconv.FormatInt(now+10, 10) + "&page=1&page_size=1"
	rec, body := env.do(t, http.MethodGet, path, owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, float64(1), data["page"])
	assert.Equal(t, float64(1), data["page_size"])
	assert.Equal(t, false, data["has_more"])
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), item["request_count"])
	assert.Equal(t, float64(1), item["attempt_count"])
	assert.Equal(t, float64(3500), item["estimated_cost_usd_micro"])
	assert.Equal(t, float64(0), item["unpriced_event_count"])
	usage, ok := item["usage"].([]any)
	require.True(t, ok)
	require.Len(t, usage, 1)
	usageItem, ok := usage[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "image", usageItem["unit"])
	assert.Equal(t, float64(2000), usageItem["quantity_milli"])

	// 归属由登录身份强制，其他用户看不到 owner 的渠道、Key 或用量。
	rec, body = env.do(t, http.MethodGet, path, other, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	otherData, _ := body["data"].(map[string]any)
	otherItems, _ := otherData["items"].([]any)
	assert.Empty(t, otherItems)

	rec, _ = env.do(t, http.MethodGet, "/api/usage/summary?start=bad", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = env.do(t, http.MethodGet, "/api/usage/summary?start=1&end=9000000", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "单次超过 90 天必须拒绝")
}

func TestShareGroupLifecycle(t *testing.T) {
	env := newTestEnv(t, true)
	const user int64 = 1001

	// 本用例要观察的正是「新建默认值」，所以**直接调 API 建**，不走 createGroup 夹具
	// （夹具会把分组置为"平台已审核通过"，那样就看不到默认值了）。
	rec, body := env.do(t, http.MethodPost, "/api/share-groups", user, `{"name":"小林公益站"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	created, _ := body["data"].(map[string]any)
	idFloat, _ := created["id"].(float64)
	id := int64(idFloat)

	// 列表能看到
	rec, body = env.do(t, http.MethodGet, "/api/share-groups", user, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	require.Len(t, items, 1)

	// 详情
	rec, body = env.do(t, http.MethodGet, "/api/share-groups/"+strconv.FormatInt(id, 10), user, "")
	require.Equal(t, http.StatusOK, rec.Code)
	detail, _ := body["data"].(map[string]any)
	assert.Equal(t, "小林公益站", detail["name"])
	// 发布状态枚举：0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停。公开分组新建后进入**待审核**（计划书 §12），
	// 而不是草稿 —— 本用例的提示语一直是「默认待审核」，此前断言值 0 与枚举不符，属代码与用例一起写错，已按语义修正为 1。
	assert.Equal(t, float64(1), detail["publish_status"], "公开分组新建后默认待审核")

	// 删除后读不到
	rec, _ = env.do(t, http.MethodDelete, "/api/share-groups/"+strconv.FormatInt(id, 10), user, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodGet, "/api/share-groups/"+strconv.FormatInt(id, 10), user, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// 归属隔离：别人的资源一律 404（而不是 403）——403 会暴露「这个 ID 存在」。
func TestOwnershipIsolationReturnsNotFound(t *testing.T) {
	env := newTestEnv(t, true)
	const ownerA, ownerB int64 = 2001, 2002

	groupID := env.createGroup(t, ownerA, "A 的分组")
	channelID := env.createChannel(t, ownerA, groupID, 50001)

	for _, tc := range []struct {
		name, method, path string
	}{
		{"读分组", http.MethodGet, "/api/share-groups/" + strconv.FormatInt(groupID, 10)},
		{"删分组", http.MethodDelete, "/api/share-groups/" + strconv.FormatInt(groupID, 10)},
		{"读分组下的渠道", http.MethodGet, "/api/share-groups/" + strconv.FormatInt(groupID, 10) + "/channels"},
		{"读渠道", http.MethodGet, "/api/channels/" + strconv.FormatInt(channelID, 10)},
		{"删渠道", http.MethodDelete, "/api/channels/" + strconv.FormatInt(channelID, 10)},
		{"读渠道的 Key 池", http.MethodGet, "/api/channels/" + strconv.FormatInt(channelID, 10) + "/keys"},
		{"加 Key", http.MethodPost, "/api/channels/" + strconv.FormatInt(channelID, 10) + "/keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := ""
			if tc.method == http.MethodPost {
				body = `{"stable_key_id":"x","key":"sk-x"}`
			}
			rec, _ := env.do(t, tc.method, tc.path, ownerB, body)
			assert.Equal(t, http.StatusNotFound, rec.Code, "越权访问必须表现为 404：%s", rec.Body.String())
		})
	}

	// B 自己的列表不含 A 的分组
	rec, body := env.do(t, http.MethodGet, "/api/share-groups", ownerB, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	assert.Empty(t, items)
}

// 未登录（上下文没有 owner）必须被拒：归属只能来自服务端。
func TestRequiresAuthenticatedOwner(t *testing.T) {
	env := newTestEnv(t, true)
	rec, _ := env.do(t, http.MethodGet, "/api/share-groups", 0, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/share-groups", 0, `{"name":"x"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// 上游 Key：明文只在请求里出现一次，落库必须是密文信封，响应只给脱敏。
func TestUpstreamKeyIsEncryptedAndNeverEchoed(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 3001
	groupID := env.createGroup(t, owner, "分组")
	channelID := env.createChannel(t, owner, groupID, 50002)

	rec, body := env.do(t, http.MethodPost,
		"/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"stable_key_id":"stable-1","name":"主 Key","key":"`+plainSecret+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 响应：只给脱敏提示，不含明文
	assert.NotContains(t, rec.Body.String(), plainSecret, "响应不得回显明文")
	assert.NotContains(t, rec.Body.String(), "enc:v1:", "响应不得回显密文信封")
	data, _ := body["data"].(map[string]any)
	assert.Equal(t, "****"+plainSecret[len(plainSecret)-4:], data["masked"])

	// 落库：secret_ref 必须是信封，且不含明文
	var stored apigxmodel.UpstreamKey
	require.NoError(t, env.db.Where("channel_id = ?", channelID).First(&stored).Error)
	assert.True(t, secretbox.IsEnvelope(stored.SecretRef), "落库必须是加密信封：%s", stored.SecretRef)
	assert.NotContains(t, stored.SecretRef, plainSecret)

	// 而且能解回原文（证明不是「存了个没用的东西」）
	plain, err := secretbox.Decrypt(stored.SecretRef)
	require.NoError(t, err)
	assert.Equal(t, plainSecret, plain)

	// 列表接口同样不得输出信封或明文
	rec, _ = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), plainSecret)
	assert.NotContains(t, rec.Body.String(), "enc:v1:")
	assert.Contains(t, rec.Body.String(), "stable-1")
}

// 主密钥未配置时**必须拒绝创建**，不能退化成明文存储。
func TestUpstreamKeyCreationFailsClosedWithoutMasterKey(t *testing.T) {
	env := newTestEnv(t, false)
	const owner int64 = 4001
	groupID := env.createGroup(t, owner, "分组")
	channelID := env.createChannel(t, owner, groupID, 50003)

	rec, _ := env.do(t, http.MethodPost,
		"/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"stable_key_id":"stable-1","key":"sk-must-not-be-stored"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "缺主密钥必须失败关闭")

	// 库里不能有任何 Key 行
	var count int64
	require.NoError(t, env.db.Model(&apigxmodel.UpstreamKey{}).Count(&count).Error)
	assert.Zero(t, count, "失败时不得写入任何凭据")
}

func TestChannelRequiresBaseChannelAndOwnedGroup(t *testing.T) {
	env := newTestEnv(t, true)
	const ownerA, ownerB int64 = 5001, 5002
	groupA := env.createGroup(t, ownerA, "A 的分组")

	// 既没给 base_channel_id、也没给服务端目录中的 recipe_id → 400。
	// 注意：契约在支持「分享者自建渠道」后**有意变化** —— 校验从"必须有 base_channel_id"
	// 变成"要么绑定既有底座渠道，要么给全自建配方"；这里断言的是后者缺字段时的拒绝。
	rec, _ := env.do(t, http.MethodPost, "/api/channels", ownerA,
		`{"share_group_id":`+strconv.FormatInt(groupA, 10)+`,"name":"未绑定"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "channel recipe")

	// 配方不完整（给了上游地址但没给 Key）→ 仍然 400，不允许半成品渠道落地
	rec, _ = env.do(t, http.MethodPost, "/api/channels", ownerA,
		`{"share_group_id":`+strconv.FormatInt(groupA, 10)+`,"name":"半成品","recipe_id":"openai-compatible","base_url":"https://example.com"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "upstream_key")

	// 用别人的分组 → 404
	rec, _ = env.do(t, http.MethodPost, "/api/channels", ownerB,
		`{"share_group_id":`+strconv.FormatInt(groupA, 10)+`,"base_channel_id":60001,"name":"偷渡"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSelfServeChannelRecipeCatalog(t *testing.T) {
	env := newTestEnv(t, true)
	rec, body := env.do(t, http.MethodGet, "/api/channels/recipes", 5099, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 12)

	seen := make(map[string]map[string]any, len(items))
	for _, raw := range items {
		item, itemOK := raw.(map[string]any)
		require.True(t, itemOK)
		_, leaksBaseType := item["base_channel_type"]
		assert.False(t, leaksBaseType, "目录不能向普通用户暴露底座渠道类型")
		_, leaksPluginKey := item["task_plugin_key"]
		assert.False(t, leaksPluginKey, "目录不能向普通用户暴露底座任务插件身份")
		seen[item["id"].(string)] = item
	}
	newAPIRecipe := seen["new-api"]
	require.NotNil(t, newAPIRecipe)
	assert.Equal(t, "new-api", newAPIRecipe["protocol"])
	assert.Equal(t, true, newAPIRecipe["requires_base_url"])
	assert.Equal(t, true, newAPIRecipe["supports_model_fetch"])
	assert.ElementsMatch(t, []any{"chat", "image", "embedding"}, newAPIRecipe["capability_hints"])
	klingRecipe := seen["kling-video"]
	require.NotNil(t, klingRecipe)
	assert.Equal(t, true, klingRecipe["async_task"])
	assert.Equal(t, credentialFormatAccessSecretPairOrRelayKey, klingRecipe["credential_format"])
	assert.ElementsMatch(t, []any{"video"}, klingRecipe["capability_hints"])
}

func TestSelfServeChannelCreationIsAtomicAndRestrictsBaseBinding(t *testing.T) {
	t.Run("ordinary user provisions a complete channel", func(t *testing.T) {
		env := newTestEnv(t, true)
		const owner int64 = 5101
		groupID := env.createGroup(t, owner, "自建渠道组")
		secret := "sk-self-serve-complete"
		rec, body := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"自建线路","recipe_id":"new-api","protocol":"anthropic","base_url":"https://example.com/v1","upstream_key":"`+secret+`"}`,
			common.RoleCommonUser)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), secret)
		channelID := int64(body["data"].(map[string]any)["id"].(float64))

		shareChannel, err := apigxmodel.GetChannel(env.db, owner, channelID)
		require.NoError(t, err)
		assert.Equal(t, "new-api", shareChannel.Protocol, "协议必须由服务端配方解析，不能信任客户端 protocol")
		var baseChannel basemodel.Channel
		require.NoError(t, env.db.Where("id = ?", shareChannel.BaseChannelID).First(&baseChannel).Error)
		assert.Equal(t, common.ChannelStatusEnabled, baseChannel.Status)
		assert.Equal(t, constant.ChannelTypeNewAPI, baseChannel.Type)
		assert.Equal(t, secret, baseChannel.Key, "底座现有转发路径仍需要明文投影")
		assert.Equal(t, "https://example.com", baseChannel.GetBaseURL())

		var storedKey apigxmodel.UpstreamKey
		require.NoError(t, env.db.Where("channel_id = ?", channelID).First(&storedKey).Error)
		assert.True(t, secretbox.IsEnvelope(storedKey.SecretRef))
		assert.NotContains(t, storedKey.SecretRef, secret)
	})

	t.Run("unknown recipe writes nothing", func(t *testing.T) {
		env := newTestEnv(t, true)
		const owner int64 = 5105
		groupID := env.createGroup(t, owner, "未知配方组")
		rec, _ := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"未知线路","recipe_id":"channel-type-999","base_url":"https://example.com","upstream_key":"sk-unknown"}`,
			common.RoleCommonUser)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "unsupported channel recipe")
		for _, target := range []any{&basemodel.Channel{}, &apigxmodel.Channel{}, &apigxmodel.UpstreamKey{}} {
			var count int64
			require.NoError(t, env.db.Model(target).Count(&count).Error)
			assert.Zero(t, count)
		}
	})

	t.Run("task recipe pins the plugin and fetches declared models", func(t *testing.T) {
		env := newTestEnv(t, true)
		const owner int64 = 5106
		groupID := env.createGroup(t, owner, "视频渠道组")
		rec, body := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"可灵视频","recipe_id":"kling-video","base_url":"https://api.klingai.com","upstream_key":"access-key|secret-key"}`,
			common.RoleCommonUser)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		channelID := int64(body["data"].(map[string]any)["id"].(float64))

		shareChannel, err := apigxmodel.GetChannel(env.db, owner, channelID)
		require.NoError(t, err)
		assert.Equal(t, "kling-video", shareChannel.Protocol)
		var baseChannel basemodel.Channel
		require.NoError(t, env.db.Where("id = ?", shareChannel.BaseChannelID).First(&baseChannel).Error)
		assert.Equal(t, constant.ChannelTypeTaskPlugin, baseChannel.Type)
		assert.Equal(t, "kling", baseChannel.GetSetting().TaskPluginKey)

		fetchRec, fetchBody := env.doWithRole(t, http.MethodPost,
			"/api/channels/"+strconv.FormatInt(channelID, 10)+"/fetch-models", owner, `{}`, common.RoleCommonUser)
		require.Equal(t, http.StatusOK, fetchRec.Code, fetchRec.Body.String())
		fetchData, ok := fetchBody["data"].(map[string]any)
		require.True(t, ok)
		models, ok := fetchData["models"].([]any)
		require.True(t, ok)
		assert.Contains(t, models, "kling-v1")
		assert.Contains(t, models, "kling-v2-master")
	})

	t.Run("task recipe rejects malformed credential before writing", func(t *testing.T) {
		env := newTestEnv(t, true)
		const owner int64 = 5107
		groupID := env.createGroup(t, owner, "错误视频凭据组")
		rec, _ := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"错误可灵","recipe_id":"kling-video","base_url":"https://api.klingai.com","upstream_key":"missing-separator"}`,
			common.RoleCommonUser)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "accessKey|secretKey")
		for _, target := range []any{&basemodel.Channel{}, &apigxmodel.Channel{}, &apigxmodel.UpstreamKey{}} {
			var count int64
			require.NoError(t, env.db.Model(target).Count(&count).Error)
			assert.Zero(t, count)
		}
	})

	t.Run("ordinary user cannot bind a global base channel", func(t *testing.T) {
		env := newTestEnv(t, true)
		const owner int64 = 5102
		groupID := env.createGroup(t, owner, "禁止绑定组")
		baseID := nextBaseChannelID()
		require.NoError(t, env.db.Create(&basemodel.Channel{
			Id: int(baseID), Name: "管理员渠道", Key: "admin-secret", Status: common.ChannelStatusEnabled,
		}).Error)
		rec, _ := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"base_channel_id":`+strconv.FormatInt(baseID, 10)+`,"name":"越权绑定"}`,
			common.RoleCommonUser)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		var count int64
		require.NoError(t, env.db.Model(&apigxmodel.Channel{}).Count(&count).Error)
		assert.Zero(t, count)
	})

	t.Run("missing secret store writes nothing", func(t *testing.T) {
		env := newTestEnv(t, false)
		const owner int64 = 5103
		groupID := env.createGroup(t, owner, "缺主密钥组")
		rec, _ := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"失败线路","recipe_id":"openai-compatible","base_url":"https://example.com","upstream_key":"sk-no-store"}`,
			common.RoleCommonUser)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		for _, target := range []any{&basemodel.Channel{}, &apigxmodel.Channel{}, &apigxmodel.UpstreamKey{}} {
			var count int64
			require.NoError(t, env.db.Model(target).Count(&count).Error)
			assert.Zero(t, count)
		}
	})

	t.Run("initial key write failure rolls back both channel rows", func(t *testing.T) {
		env := newTestEnv(t, true)
		const owner int64 = 5104
		groupID := env.createGroup(t, owner, "故障注入组")
		callbackName := "b3:fail_initial_key"
		injected := errors.New("injected upstream key write failure")
		require.NoError(t, env.db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == (apigxmodel.UpstreamKey{}).TableName() {
				tx.AddError(injected)
			}
		}))
		t.Cleanup(func() { _ = env.db.Callback().Create().Remove(callbackName) })

		rec, _ := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
			`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"回滚线路","recipe_id":"openai-compatible","base_url":"https://example.com","upstream_key":"sk-rollback"}`,
			common.RoleCommonUser)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		for _, target := range []any{&basemodel.Channel{}, &apigxmodel.Channel{}, &apigxmodel.UpstreamKey{}} {
			var count int64
			require.NoError(t, env.db.Model(target).Count(&count).Error)
			assert.Zero(t, count, "%T must roll back", target)
		}
	})
}

func TestDeleteChannelFailsClosedAndIsIdempotent(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, stranger int64 = 5201, 5202
	groupID := env.createGroup(t, owner, "删除生命周期组")
	rec, body := env.doWithRole(t, http.MethodPost, "/api/channels", owner,
		`{"share_group_id":`+strconv.FormatInt(groupID, 10)+`,"name":"待删除线路","recipe_id":"openai-compatible","base_url":"https://example.com","upstream_key":"sk-delete"}`,
		common.RoleCommonUser)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	channelID := int64(body["data"].(map[string]any)["id"].(float64))
	shareChannel, err := apigxmodel.GetChannel(env.db, owner, channelID)
	require.NoError(t, err)
	baseID := int(shareChannel.BaseChannelID)
	require.NoError(t, env.db.Model(&basemodel.Channel{}).Where("id = ?", baseID).Updates(map[string]any{
		"models": "delete-model", "channel_info": basemodel.ChannelInfo{IsMultiKey: true, MultiKeySize: 1},
	}).Error)
	require.NoError(t, env.db.Create(&basemodel.Ability{
		Group: "default", Model: "delete-model", ChannelId: baseID, Enabled: true,
	}).Error)

	// 故障注入在最后的 APIGX 软删除：请求会失败，但底座已先停用并清空投影。
	callbackName := "b3:fail_apigx_channel_retire"
	injected := errors.New("injected apigx channel retire failure")
	require.NoError(t, env.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == (apigxmodel.Channel{}).TableName() {
			tx.AddError(injected)
		}
	}))
	rec, _ = env.doWithRole(t, http.MethodDelete, "/api/channels/"+strconv.FormatInt(channelID, 10), owner, "", common.RoleCommonUser)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var baseChannel basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", baseID).First(&baseChannel).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, baseChannel.Status)
	assert.Empty(t, baseChannel.Key)
	assert.Empty(t, baseChannel.Models)
	assert.False(t, baseChannel.ChannelInfo.IsMultiKey)
	var abilityCount int64
	require.NoError(t, env.db.Model(&basemodel.Ability{}).Where("channel_id = ?", baseID).Count(&abilityCount).Error)
	assert.Zero(t, abilityCount)
	_, err = apigxmodel.GetChannel(env.db, owner, channelID)
	assert.NoError(t, err, "领域软删除失败时保持可重试，但底座必须已失败关闭")

	require.NoError(t, env.db.Callback().Update().Remove(callbackName))
	rec, _ = env.doWithRole(t, http.MethodDelete, "/api/channels/"+strconv.FormatInt(channelID, 10), owner, "", common.RoleCommonUser)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err = apigxmodel.GetChannel(env.db, owner, channelID)
	assert.ErrorIs(t, err, apigxmodel.ErrNotFound)

	// 墓碑保留 base_channel_id，重复删除仍能安全重放所有收尾步骤。
	rec, _ = env.doWithRole(t, http.MethodDelete, "/api/channels/"+strconv.FormatInt(channelID, 10), owner, "", common.RoleCommonUser)
	assert.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.doWithRole(t, http.MethodDelete, "/api/channels/"+strconv.FormatInt(channelID, 10), stranger, "", common.RoleCommonUser)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func assertOfferingPublishProjectionAtomic(t *testing.T, env *testEnv, owner, reviewer, channelID int64, modelID string) {
	t.Helper()
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"`+modelID+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/submit", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, _ = env.do(t, http.MethodPut, "/api/price-rules", reviewer,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	callbackName := "b3:fail_ability_projection:" + env.db.Dialector.Name()
	require.NoError(t, env.db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "abilities" {
			tx.AddError(errors.New("injected ability projection failure"))
		}
	}))
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/publish",
		reviewer, `{"reason_code":"matrix_approval"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NoError(t, env.db.Callback().Create().Remove(callbackName))

	var offering apigxmodel.ModelOffering
	require.NoError(t, env.db.Where("id = ?", offeringID).First(&offering).Error)
	assert.Equal(t, apigxmodel.OfferingStatusPending, offering.Status)
	var auditCount int64
	require.NoError(t, env.db.Model(&apigxmodel.AuditEvent{}).
		Where("target_type = ? AND target_id = ?", "model_offering", offeringID).Count(&auditCount).Error)
	assert.Zero(t, auditCount)
	shareChannel, err := apigxmodel.GetChannel(env.db, owner, channelID)
	require.NoError(t, err)
	var base basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", shareChannel.BaseChannelID).First(&base).Error)
	assert.NotContains(t, base.Models, modelID)
	var abilityCount int64
	require.NoError(t, env.db.Model(&basemodel.Ability{}).
		Where("channel_id = ? AND model = ?", shareChannel.BaseChannelID, modelID).Count(&abilityCount).Error)
	assert.Zero(t, abilityCount)

	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/publish",
		reviewer, `{"reason_code":"matrix_approval"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, env.db.Where("id = ?", offeringID).First(&offering).Error)
	assert.Equal(t, apigxmodel.OfferingStatusPublished, offering.Status)
	require.NoError(t, env.db.Where("id = ?", shareChannel.BaseChannelID).First(&base).Error)
	assert.Contains(t, base.Models, modelID)
	require.NoError(t, env.db.Model(&basemodel.Ability{}).
		Where("channel_id = ? AND model = ? AND enabled = ?", shareChannel.BaseChannelID, modelID, true).Count(&abilityCount).Error)
	assert.EqualValues(t, 1, abilityCount)
}

func TestSelfServeChannelLifecycleOnConfiguredDialects(t *testing.T) {
	tests := []struct {
		name string
		open func(*testing.T) *gorm.DB
	}{
		{name: "sqlite", open: func(t *testing.T) *gorm.DB {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "self-serve-lifecycle.db")), &gorm.Config{})
			require.NoError(t, err)
			return db
		}},
		{name: "postgres", open: func(t *testing.T) *gorm.DB {
			dsn := os.Getenv("APIGX_TEST_PG_DSN")
			if dsn == "" {
				t.Skip("未设置 APIGX_TEST_PG_DSN：PostgreSQL 自建渠道生命周期未执行")
			}
			db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			return db
		}},
		{name: "mysql", open: func(t *testing.T) *gorm.DB {
			dsn := os.Getenv("APIGX_TEST_MYSQL_DSN")
			if dsn == "" {
				t.Skip("未设置 APIGX_TEST_MYSQL_DSN：MySQL 自建渠道生命周期未执行")
			}
			db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			return db
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.open(t)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			migrateTestEnv(t, db)

			rollbackFixture := errors.New("rollback self-serve lifecycle fixture")
			err = db.Transaction(func(tx *gorm.DB) error {
				env := configureTestEnv(t, tx, true)
				const owner int64 = 9_830_001
				groupID := env.createGroup(t, owner, "三库生命周期组")
				createBody := `{"share_group_id":` + strconv.FormatInt(groupID, 10) +
					`,"name":"三库自建线路","recipe_id":"openai-compatible","base_url":"https://example.com/v1","upstream_key":"sk-matrix-success"}`
				rec, body := env.doWithRole(t, http.MethodPost, "/api/channels", owner, createBody, common.RoleCommonUser)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				channelID := int64(body["data"].(map[string]any)["id"].(float64))
				shareChannel, getErr := apigxmodel.GetChannel(tx, owner, channelID)
				require.NoError(t, getErr)
				baseID := shareChannel.BaseChannelID
				assertOfferingPublishProjectionAtomic(t, env, owner, owner+99, channelID, "b3-matrix-publish-"+tc.name)

				taskBody := `{"share_group_id":` + strconv.FormatInt(groupID, 10) +
					`,"name":"三库视频线路","recipe_id":"kling-video","base_url":"https://api.klingai.com","upstream_key":"matrix-access|matrix-secret"}`
				rec, body = env.doWithRole(t, http.MethodPost, "/api/channels", owner, taskBody, common.RoleCommonUser)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				taskChannelID := int64(body["data"].(map[string]any)["id"].(float64))
				taskChannel, getErr := apigxmodel.GetChannel(tx, owner, taskChannelID)
				require.NoError(t, getErr)
				var taskBase basemodel.Channel
				require.NoError(t, tx.Where("id = ?", taskChannel.BaseChannelID).First(&taskBase).Error)
				assert.Equal(t, constant.ChannelTypeTaskPlugin, taskBase.Type)
				assert.Equal(t, "kling", taskBase.GetSetting().TaskPluginKey)

				var baseBefore, channelBefore, keyBefore int64
				require.NoError(t, tx.Model(&basemodel.Channel{}).Count(&baseBefore).Error)
				require.NoError(t, tx.Model(&apigxmodel.Channel{}).Count(&channelBefore).Error)
				require.NoError(t, tx.Model(&apigxmodel.UpstreamKey{}).Count(&keyBefore).Error)

				callbackName := "b3:matrix_fail_initial_key:" + tc.name
				require.NoError(t, tx.Callback().Create().Before("gorm:create").Register(callbackName, func(callbackTx *gorm.DB) {
					if callbackTx.Statement.Schema != nil && callbackTx.Statement.Schema.Table == (apigxmodel.UpstreamKey{}).TableName() {
						callbackTx.AddError(errors.New("injected matrix upstream key failure"))
					}
				}))
				failedBody := `{"share_group_id":` + strconv.FormatInt(groupID, 10) +
					`,"name":"三库回滚线路","recipe_id":"openai-compatible","base_url":"https://example.com","upstream_key":"sk-matrix-rollback"}`
				rec, _ = env.doWithRole(t, http.MethodPost, "/api/channels", owner, failedBody, common.RoleCommonUser)
				assert.Equal(t, http.StatusInternalServerError, rec.Code)
				require.NoError(t, tx.Callback().Create().Remove(callbackName))

				var baseAfter, channelAfter, keyAfter int64
				require.NoError(t, tx.Model(&basemodel.Channel{}).Count(&baseAfter).Error)
				require.NoError(t, tx.Model(&apigxmodel.Channel{}).Count(&channelAfter).Error)
				require.NoError(t, tx.Model(&apigxmodel.UpstreamKey{}).Count(&keyAfter).Error)
				assert.Equal(t, baseBefore, baseAfter)
				assert.Equal(t, channelBefore, channelAfter)
				assert.Equal(t, keyBefore, keyAfter)

				require.NoError(t, tx.Model(&basemodel.Channel{}).Where("id = ?", baseID).
					Update("channel_info", basemodel.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}).Error)
				rec, _ = env.doWithRole(t, http.MethodDelete, "/api/channels/"+strconv.FormatInt(channelID, 10), owner, "", common.RoleCommonUser)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				rec, _ = env.doWithRole(t, http.MethodDelete, "/api/channels/"+strconv.FormatInt(channelID, 10), owner, "", common.RoleCommonUser)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

				var retired basemodel.Channel
				require.NoError(t, tx.Where("id = ?", baseID).First(&retired).Error)
				assert.Equal(t, common.ChannelStatusManuallyDisabled, retired.Status)
				assert.Empty(t, retired.Key)
				assert.Empty(t, retired.Models)
				assert.False(t, retired.ChannelInfo.IsMultiKey)
				var abilityCount int64
				require.NoError(t, tx.Model(&basemodel.Ability{}).Where("channel_id = ?", baseID).Count(&abilityCount).Error)
				assert.Zero(t, abilityCount)
				_, getErr = apigxmodel.GetChannel(tx, owner, channelID)
				assert.ErrorIs(t, getErr, apigxmodel.ErrNotFound)
				return rollbackFixture
			})
			require.ErrorIs(t, err, rollbackFixture)
		})
	}
}

func TestUpstreamKeyStatusAndValidation(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 6001
	groupID := env.createGroup(t, owner, "分组")
	channelID := env.createChannel(t, owner, groupID, 50004)

	// 缺 key → 400
	rec, _ := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"stable_key_id":"s1"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"key":"sk-invalid-weight","weight":0}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 正常创建
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"stable_key_id":"s1","name":"k1","key":"sk-abcdefgh"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ := body["data"].(map[string]any)
	keyID := int64(data["id"].(float64))
	assert.NotEmpty(t, data["stable_key_id"])

	// 权重只接受 1-1000，且 owner 隔离仍由服务端校验。
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/weight", owner, `{"weight":7}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/weight", owner, `{"weight":0}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/weight", owner+1, `{"weight":9}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// 非法状态 → 400；缺字段 → 400
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/status", owner, `{"status":99}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/status", owner, `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 合法状态切换 → 200，且列表里的 usable 随之改变
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/status", owner, `{"status":0}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"usable":false`)

	// 冷却：设置未来时间 → usable=false；过去时间 → usable=true
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/cooldown", owner, `{"cooldown_until":99999999999}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodPatch, "/api/keys/"+strconv.FormatInt(keyID, 10)+"/status", owner, `{"status":1}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner, "")
	assert.Contains(t, rec.Body.String(), `"usable":false`, "冷却期内不可用")

	// 非法 id → 400
	rec, _ = env.do(t, http.MethodDelete, "/api/keys/abc", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeleteUpstreamKeyTwice(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 7001
	groupID := env.createGroup(t, owner, "分组")
	channelID := env.createChannel(t, owner, groupID, 50005)

	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"stable_key_id":"s1","key":"sk-abcdefgh"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	keyID := int64(body["data"].(map[string]any)["id"].(float64))

	rec, _ = env.do(t, http.MethodDelete, "/api/keys/"+strconv.FormatInt(keyID, 10), owner, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	// 第二次删除：墓碑已存在 → 404（而不是静默成功）
	rec, _ = env.do(t, http.MethodDelete, "/api/keys/"+strconv.FormatInt(keyID, 10), owner, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ---- 应用 Key 池（投影触发接口） ----

func TestApplyChannelPoolProjectsKeysIntoBaseChannel(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8101

	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座影子渠道", Key: "placeholder", Status: common.ChannelStatusEnabled,
	}).Error)

	groupID := env.createGroup(t, owner, "应用测试组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	for _, spec := range []struct{ stable, secret string }{
		{"s1", "sk-apply-one-aaaaa"},
		{"s2", "sk-apply-two-bbbbb"},
	} {
		rec, _ := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
			`{"stable_key_id":"`+spec.stable+`","name":"`+spec.stable+`","key":"`+spec.secret+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/apply", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	assert.Equal(t, float64(2), data["applied_keys"])
	// 响应绝不能带出任何凭据信息
	assert.NotContains(t, rec.Body.String(), "sk-apply")
	assert.NotContains(t, rec.Body.String(), "enc:v1:")

	var base basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", int(baseChannelID)).First(&base).Error)
	assert.True(t, base.ChannelInfo.IsMultiKey)
	assert.Equal(t, 2, base.ChannelInfo.MultiKeySize)
	keys := base.GetKeys()
	require.Len(t, keys, 2)
	assert.NotContains(t, keys, "placeholder", "底座原有的占位 Key 必须被投影结果替换")
}

// 池内没有可用凭据时必须明确拒绝（409）：这会让渠道不再接收新请求，分享者必须知道。
func TestApplyChannelPoolRejectsWhenNoUsableKey(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8102

	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)

	groupID := env.createGroup(t, owner, "空池组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	rec, _ := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/apply", owner, "")
	assert.Equal(t, http.StatusConflict, rec.Code, "空池必须返回 409 而不是静默成功")
	assert.Contains(t, rec.Body.String(), "no_usable_key")
}

// 越权应用别人的渠道必须 404（与不存在等价）。
func TestApplyChannelPoolIsOwnerScoped(t *testing.T) {
	env := newTestEnv(t, true)
	const ownerA, ownerB int64 = 8103, 8104

	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)

	groupA := env.createGroup(t, ownerA, "A 的组")
	channelA := env.createChannel(t, ownerA, groupA, baseChannelID)

	rec, _ := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelA, 10)+"/apply", ownerB, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// 未登录同样被拒
	rec, _ = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelA, 10)+"/apply", 0, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// ---- 渠道容量策略接口 ----

func TestSetChannelCapacityPolicy(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8201
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "策略组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	// 明确值：创建策略并挂到渠道上
	rec, body := env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner,
		`{"concurrency_mode":4,"concurrency_value":3}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	policyID := int64(data["id"].(float64))

	var stored apigxmodel.CapacityPolicy
	require.NoError(t, env.db.Where("id = ?", policyID).First(&stored).Error)
	assert.EqualValues(t, 4, stored.MaxConcurrencyMode)
	assert.EqualValues(t, 3, stored.MaxConcurrency)
	assert.EqualValues(t, 1, stored.PolicyVersion)
	firstChain := channelpool.ChannelConcurrencyChain(env.db, channelID, 0, 0, 0)
	firstResolution, err := capacity.Resolve(firstChain...)
	require.NoError(t, err)
	assert.EqualValues(t, 3, firstResolution.Value)

	// 再次设置：应更新同一条（不新建），版本号递增
	rec, body = env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner,
		`{"concurrency_mode":4,"concurrency_value":9}`)
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, float64(policyID), data["id"], "同一渠道只保留一条策略")

	var count int64
	require.NoError(t, env.db.Model(&apigxmodel.CapacityPolicy{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, env.db.Where("id = ?", policyID).First(&stored).Error)
	assert.EqualValues(t, 9, stored.MaxConcurrency)
	assert.EqualValues(t, 2, stored.PolicyVersion)
	secondChain := channelpool.ChannelConcurrencyChain(env.db, channelID, 0, 0, 0)
	secondResolution, err := capacity.Resolve(secondChain...)
	require.NoError(t, err)
	assert.EqualValues(t, 9, secondResolution.Value, "策略更新后准入缓存必须立即失效")

	// 四态非法组合必须被拒（明确值要求 > 0）
	rec, _ = env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner,
		`{"concurrency_mode":4,"concurrency_value":0}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "非法四态组合不得落库")
	// 未配置模式但给了值，同样非法
	rec, _ = env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner,
		`{"concurrency_mode":0,"concurrency_value":5}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	// 缺 mode
	rec, _ = env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner, `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 越权：别人的渠道一律 404
	rec, _ = env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", 8202,
		`{"concurrency_mode":4,"concurrency_value":1}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ---- 平台 Key 偏好接口（T1.10） ----

func TestPlatformKeyPreferencesAPI(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, other int64 = 8301, 8302
	const tokenID int64 = 4321

	// 未配置偏好时读取：返回默认模式而不是错误
	rec, body := env.do(t, http.MethodGet, "/api/platform-keys/4321/preferences", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	assert.Equal(t, false, data["configured"])
	assert.Equal(t, float64(0), data["scope_mode"], "未配置 = 默认自动路由")

	// 设置「允许来源 + 允许回退」
	rec, body = env.do(t, http.MethodPut, "/api/platform-keys/4321/preferences", owner,
		`{"scope_mode":1,"allowed_owner_user_ids":[55,77],"allow_fallback":true,"allowed_model_ids":["m-b","m-a"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, float64(1), data["scope_mode"])
	assert.Equal(t, true, data["allow_fallback"])
	owners, _ := data["allowed_owner_user_ids"].([]any)
	assert.Len(t, owners, 2)
	models, _ := data["allowed_model_ids"].([]any)
	assert.Equal(t, []any{"m-a", "m-b"}, models, "允许模型应去重排序")

	// 回读一致
	rec, body = env.do(t, http.MethodGet, "/api/platform-keys/4321/preferences", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, true, data["configured"])

	// 「允许来源」模式但列表为空必须被拒
	rec, _ = env.do(t, http.MethodPut, "/api/platform-keys/4321/preferences", owner, `{"scope_mode":1,"allowed_owner_user_ids":[]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "空允许列表不得落库")

	// 固定来源模式缺 pinned 必须被拒
	rec, _ = env.do(t, http.MethodPut, "/api/platform-keys/4321/preferences", owner, `{"scope_mode":2}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 模型广场使用 public_id 固定来源，浏览器无需接触或手填内部供给 ID。
	group := &apigxmodel.ShareGroup{
		OwnerUserID: other, Name: "可选来源", DefaultVisibility: apigxmodel.ShareGroupVisibilityPublic,
		PublishStatus: apigxmodel.ShareGroupPublishPublished,
	}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "公开来源底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	channel := &apigxmodel.Channel{
		OwnerUserID: other, ShareGroupID: group.ID, BaseChannelID: baseChannelID,
		Name: "来源渠道", Protocol: "openai", BaseURL: "https://source.example",
	}
	require.NoError(t, apigxmodel.CreateChannel(env.db, channel))
	offering := &apigxmodel.ModelOffering{ChannelID: channel.ID, UpstreamModelID: "public-pin-model"}
	require.NoError(t, apigxmodel.CreateModelOffering(env.db, other, offering))
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, other, offering.ID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, other, offering.ID))
	rec, body = env.do(t, http.MethodPut, "/api/platform-keys/4321/preferences", owner,
		`{"scope_mode":2,"pinned_offering_public_id":"`+offering.PublicID+`","allow_fallback":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, offering.PublicID, data["pinned_offering_public_id"])
	assert.Equal(t, float64(offering.ID), data["pinned_offering_id"])

	rec, body = env.do(t, http.MethodGet, "/api/platform-keys/4321/preferences", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, offering.PublicID, data["pinned_offering_public_id"])

	// 未知模式被拒
	rec, _ = env.do(t, http.MethodPut, "/api/platform-keys/4321/preferences", owner, `{"scope_mode":99}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 归属隔离：他人读取只能看到「未配置」，且不能写入所有者之外的偏好
	rec, body = env.do(t, http.MethodGet, "/api/platform-keys/4321/preferences", other, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, false, data["configured"], "他人的偏好对当前用户不可见")
	require.NoError(t, nil)

	// 越权写入：会写成 other 自己的偏好（各用户独立），不影响 owner 的记录
	rec, _ = env.do(t, http.MethodPut, "/api/platform-keys/4321/preferences", other, `{"scope_mode":0}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var count int64
	require.NoError(t, env.db.Model(&apigxmodel.APIKey{}).Where("user_id = ?", owner).Count(&count).Error)
	assert.EqualValues(t, 1, count, "owner 的偏好记录不受他人操作影响")
	_ = tokenID
}

// ---- 模型供给与发布流程接口（T1.8） ----

func TestOfferingLifecycleThroughAPI(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, other int64 = 8401, 8402
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "供给组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	// 手工添加：新供给是草稿，不对外可见
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"gpt-x","capabilities":{"streaming":true}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	offeringID := int64(data["id"].(float64))
	assert.Equal(t, float64(0), data["status"], "新供给必须是草稿")

	// 重复添加同一模型 → 409（唯一约束应映射为冲突而不是 500）
	rec, _ = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"gpt-x"}`)
	assert.Equal(t, http.StatusConflict, rec.Code, "重复供给应返回 409")

	// 列表
	rec, body = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	items, _ := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)

	// 归属隔离：他人读不到、也不能提交
	rec, _ = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", other, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/submit", other, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// 提交审核：草稿 → 待审核
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/submit", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// **非管理员不得发布**（否则「待审核」形同虚设）
	// **先配置价格再发布**：与产品规则一致 —— 底座在价格未配置时判定"模型暂时无法使用"，
	// 因此供给发布前必须已有被接受的价格（本用例顺带覆盖该闸门）。
	rec, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.Equal(t, http.StatusOK, rec.Code, "配置价格应当成功")

	rec, _ = env.doWithRole(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/publish", owner, "", common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, rec.Code, "普通用户不得自行发布")
	assert.Contains(t, rec.Body.String(), "review_required")

	// 管理员发布 → 已发布
	rec, body = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/publish", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, float64(2), data["status"])

	// 能力更新递增修订号
	rec, _ = env.do(t, http.MethodPatch, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/capabilities", owner,
		`{"capabilities":{"streaming":true,"tools":true}}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec, body = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner, "")
	items, _ = body["data"].(map[string]any)["items"].([]any)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, float64(2), first["revision"])
	caps, _ := first["capabilities"].(map[string]any)
	assert.Equal(t, true, caps["tools"])

	// 已发布不能被"提交"退回待审核
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/submit", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 删除后同一模型可重新发布
	rec, _ = env.do(t, http.MethodDelete, "/api/offerings/"+strconv.FormatInt(offeringID, 10), owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"gpt-x"}`)
	assert.Equal(t, http.StatusOK, rec.Code, "软删除后应可重新发布")
}

func TestReviewerCanReviewCrossOwnerOfferingsAndWritesAudit(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, reviewer int64 = 8451, 8499
	require.NoError(t, env.db.AutoMigrate(&basemodel.User{}))
	require.NoError(t, env.db.Create(&basemodel.User{
		Id: int(owner), Username: "review-owner", DisplayName: "共享者甲", Password: "not-used-in-test",
	}).Error)
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "跨 owner 审核底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "待审核组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	createPending := func(modelID string) int64 {
		t.Helper()
		rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
			`{"upstream_model_id":"`+modelID+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		id := int64(body["data"].(map[string]any)["id"].(float64))
		rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(id, 10)+"/submit", owner, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return id
	}

	publishID := createPending("review-publish")
	rec, _ := env.do(t, http.MethodPut, "/api/price-rules", reviewer,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(publishID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 普通用户不能读取平台待审核队列；管理员能看到其他 owner 的资源。
	rec, _ = env.doWithRole(t, http.MethodGet, "/api/reviews/offerings", owner, "", common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec, body := env.do(t, http.MethodGet, "/api/reviews/offerings?page=1&page_size=10", reviewer, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	queue := body["data"].(map[string]any)
	items := queue["items"].([]any)
	require.Len(t, items, 1)
	first := items[0].(map[string]any)
	assert.Equal(t, float64(owner), first["owner_user_id"])
	assert.Equal(t, "共享者甲", first["owner_display_name"])
	assert.Equal(t, "review-publish", first["upstream_model_id"])
	assert.Equal(t, true, first["price_configured"])
	assert.NotContains(t, rec.Body.String(), plainSecret)

	// 分享者即使知道 ID，也不能用普通用户角色自行通过审核。
	rec, _ = env.doWithRole(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(publishID, 10)+"/publish",
		owner, `{"reason_code":"self_approval"}`, common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// 另一位管理员可发布 owner 的供给，并得到 owner 与前后状态；不需要伪装成资源 owner。
	rec, body = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(publishID, 10)+"/publish",
		reviewer, `{"reason_code":"manual_approval"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := body["data"].(map[string]any)
	assert.Equal(t, float64(owner), data["owner_user_id"])
	assert.Equal(t, float64(apigxmodel.OfferingStatusPending), data["from_status"])
	assert.Equal(t, float64(apigxmodel.OfferingStatusPublished), data["status"])

	// 管理资源选择器请求全部状态，已发布供给也必须返回。
	rec, body = env.do(t, http.MethodGet, "/api/reviews/offerings?status=all&page=1&page_size=10", reviewer, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	allItems := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, allItems, 1)
	assert.Equal(t, float64(apigxmodel.OfferingStatusPublished), allItems[0].(map[string]any)["status"])

	// 重复审核不能成功，也不能产生第二条审计事件。
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(publishID, 10)+"/publish", reviewer, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var publishEvents []apigxmodel.AuditEvent
	require.NoError(t, env.db.Where("target_type = ? AND target_id = ?", "model_offering", publishID).Find(&publishEvents).Error)
	require.Len(t, publishEvents, 1)
	assert.Equal(t, reviewer, publishEvents[0].ActorUserID)
	assert.Equal(t, "offering_publish", publishEvents[0].Action)
	assert.Contains(t, publishEvents[0].ChangeSummary, `"owner_user_id":8451`)
	assert.Contains(t, publishEvents[0].ChangeSummary, `"reason_code":"manual_approval"`)

	rejectID := createPending("review-reject")
	// 理由只接受脱敏码；自由文本在进入不可变审计表前被拒绝，状态保持待审核。
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(rejectID, 10)+"/reject",
		reviewer, `{"reason_code":"contains a secret"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var rejectOffering apigxmodel.ModelOffering
	require.NoError(t, env.db.Where("id = ?", rejectID).First(&rejectOffering).Error)
	assert.Equal(t, apigxmodel.OfferingStatusPending, rejectOffering.Status)

	rec, body = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(rejectID, 10)+"/reject",
		reviewer, `{"reason_code":"unsupported_claim"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(owner), body["data"].(map[string]any)["owner_user_id"])
	var rejectEvents []apigxmodel.AuditEvent
	require.NoError(t, env.db.Where("target_type = ? AND target_id = ?", "model_offering", rejectID).Find(&rejectEvents).Error)
	require.Len(t, rejectEvents, 1)
	assert.Equal(t, "offering_reject", rejectEvents[0].Action)
	assert.Contains(t, rejectEvents[0].ChangeSummary, `"reason_code":"unsupported_claim"`)
}

// ---- 广场与详情（T1.9） ----

func TestPlazaOnlyExposesPublishedAndPublic(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8501

	// 分组设为**公开**（可见性 2）；否则即便已发布也不该出现在广场
	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "公开组", DefaultVisibility: apigxmodel.ShareGroupVisibilityPublic, PublishStatus: apigxmodel.ShareGroupPublishPublished}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	channelID := env.createChannel(t, owner, group.ID, baseChannelID)

	// 草稿供给：不应出现在广场
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"m-plaza","capabilities":{"streaming":true,"vision":true,"tools":true}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))
	publicID := body["data"].(map[string]any)["public_id"].(string)

	rec, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, body["data"].(map[string]any)["items"], "草稿不得出现在广场")
	rec, _ = env.do(t, http.MethodGet, "/api/plaza/"+publicID, owner, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "未发布供给的详情对广场不存在")

	// 提交 + 发布后才可见
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, offeringID))
	// **先配置价格再发布**：与产品规则一致 —— 底座在价格未配置时判定"模型暂时无法使用"，
	// 因此供给发布前必须已有被接受的价格（本用例顺带覆盖该闸门）。
	rec, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.Equal(t, http.StatusOK, rec.Code, "配置价格应当成功")

	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(offeringID, 10)+"/publish", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	items, _ := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	item, _ := items[0].(map[string]any)
	assert.Equal(t, "m-plaza", item["model"])
	assert.NotEmpty(t, item["channel_public_id"], "广场必须给出对外可辨识的来源标识")

	// **只启用已完成能力**：只声明了流式的供给，能力里就只有流式；
	// vision/tools 属"声明了但平台尚不具备"，单独列出而不是混进 capabilities
	// 能力字段是结构化布尔值（无 omitempty），因此断言的是**取值**而不是键是否存在：
	// 平台具备的为 true，尚不具备的为 false——不冒充可用。
	caps, _ := item["capabilities"].(map[string]any)
	assert.Equal(t, true, caps["streaming"], "流式由底座中继链路直接支持，已具备")
	assert.Equal(t, false, caps["vision"], "视觉尚未按供给验证，不得对外声称具备")
	assert.Equal(t, false, caps["tools"], "工具调用尚未按供给验证")
	unverified, _ := item["unverified_capabilities"].([]any)
	assert.ElementsMatch(t, []any{"vision", "tools"}, unverified, "声明但未具备的能力应如实单列")

	// 详情可见
	rec, body = env.do(t, http.MethodGet, "/api/plaza/"+publicID, owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	detail, _ := body["data"].(map[string]any)
	assert.Equal(t, "m-plaza", detail["model"])

	// 私有分组：即便已发布也不出现在广场
	privateGroup := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "私有组", DefaultVisibility: apigxmodel.ShareGroupVisibilityPrivate, PublishStatus: apigxmodel.ShareGroupPublishPublished}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, privateGroup))
	privateChannelID := env.createChannel(t, owner, privateGroup.ID, nextBaseChannelID())
	rec, body = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(privateChannelID, 10)+"/offerings", owner, `{"upstream_model_id":"m-private"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	privateOfferingID := int64(body["data"].(map[string]any)["id"].(float64))
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, privateOfferingID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, owner, privateOfferingID))

	rec, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	items, _ = body["data"].(map[string]any)["items"].([]any)
	assert.Len(t, items, 1, "私有分组的已发布供给不得出现在广场")

	// 未登录不得访问广场
	rec, _ = env.do(t, http.MethodGet, "/api/plaza", 0, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// **回归测试**：更新已有策略时，**每个维度都必须落库**。
//
// 早期实现的更新分支只写了并发与版本号，导致"改 RPM/TPM 静默不生效"：
// 内存对象改了、库里没改。表现很迷惑——新建策略（走创建分支）时一切正常，
// 只有更新已有策略才失效，因此必须专门测更新路径。
func TestCapacityPolicyUpdatePersistsAllDimensions(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8601
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "策略更新组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	// 首次：创建（三个维度都给值）
	rec, body := env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner,
		`{"concurrency_mode":4,"concurrency_value":5,"rpm_mode":4,"rpm_limit":60,"tpm_mode":4,"tpm_limit":9000}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	policyID := int64(body["data"].(map[string]any)["id"].(float64))

	// 再次：**更新**为完全不同的值（含"明确不限"）
	rec, _ = env.do(t, http.MethodPut, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/capacity-policy", owner,
		`{"concurrency_mode":3,"rpm_mode":4,"rpm_limit":7,"tpm_mode":4,"tpm_limit":12345}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var stored apigxmodel.CapacityPolicy
	require.NoError(t, env.db.Where("id = ?", policyID).First(&stored).Error)
	assert.EqualValues(t, 3, stored.MaxConcurrencyMode, "并发维度应更新为「明确不限」")
	// 下面两条是本回归测试的核心：更新分支漏列会让它们保持旧值
	assert.EqualValues(t, 7, stored.RpmLimit, "RPM 上限必须随更新落库")
	assert.EqualValues(t, 4, stored.RpmMode)
	assert.EqualValues(t, 12345, stored.TpmLimit, "TPM 上限必须随更新落库")
	assert.EqualValues(t, 4, stored.TpmMode)
	assert.EqualValues(t, 2, stored.PolicyVersion, "版本号应递增")
}

// ---- 可解释路由结果接口（T2.9） ----

func TestRouteExplainEndpoint(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8701
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "解释组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	// 两条已发布供给 + 一条仍是草稿（草稿不得出现在候选里）
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"explain-x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	publishedID := int64(body["data"].(map[string]any)["id"].(float64))
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, publishedID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, owner, publishedID))

	rec, body = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"explain-draft"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	// 先直接验证仓储：已发布供给应能被按模型名查到（先分清是仓储还是装配的问题）
	offerings, repoErr := apigxmodel.ListPublishedOfferingsByModel(env.db, "explain-x")
	require.NoError(t, repoErr)
	require.Len(t, offerings, 1, "仓储应能按模型名查到已发布供给")
	// 缺 model 参数 → 400
	rec, _ = env.do(t, http.MethodGet, "/api/route-explain", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 未认证 → 401
	rec, _ = env.do(t, http.MethodGet, "/api/route-explain?model=explain-x", 0, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// 未知策略 → 400（不静默退回 auto）
	rec, _ = env.do(t, http.MethodGet, "/api/route-explain?model=explain-x&strategy=nonsense", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// 正常解释：草稿供给不在候选里，且输出只有对外标识
	rec, body = env.do(t, http.MethodGet, "/api/route-explain?model=explain-x", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	candidates, _ := data["candidates"].([]any)
	require.Len(t, candidates, 1, "只有已发布供给进入候选（草稿不得出现）")
	first, _ := candidates[0].(map[string]any)
	assert.NotEmpty(t, first["offering_public_id"])
	assert.NotEmpty(t, first["channel_public_id"])
	assert.Equal(t, "explain-x", first["model_name"])

	// **泄露扫描**：响应不得含内部 ID / 凭据 / 底座渠道号
	// **泄露扫描**：断言的是**内部标识的结构**与**有区分度的内部 ID**，
	// 而不是"某个短数字没出现"——`owner_user_id=8701` 本身就含 "1"，
	// 那种断言看着严格、实则毫无意义（初版就是这么写的）。
	raw := rec.Body.String()
	for _, forbidden := range []string{
		`"channel_id"`, `"offering_id"`, `"base_channel_id"`, `"share_channel_id"`,
		`"secret"`, `"upstream_key"`, "sk-", "enc:v1:",
		strconv.FormatInt(baseChannelID, 10), // 底座渠道号（有区分度的长数字）
	} {
		assert.NotContains(t, raw, forbidden, "解释结果不得包含内部标识：%s", forbidden)
	}

	// 没有质量样本时如实标记（不伪装成"质量良好"）
	assert.Equal(t, true, first["insufficient"], "无样本必须显式标出样本不足")
	assert.Equal(t, false, first["price_known"], "没有价格快照时必须标为价格未知")
}

// ---- 应用路由排名（T2.7 生效路径） ----

func TestRouteApplyEndpoint(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8801
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "应用组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"apply-x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, offeringID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, owner, offeringID))

	// ① 普通用户不得应用（跨分享者的排序是平台侧动作）
	rec, _ = env.doWithRole(t, http.MethodPost, "/api/route-apply", owner,
		`{"model":"apply-x","dry_run":false}`, common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, rec.Code, "分享者不得自行改写跨分享者的路由权重")

	// ② dry_run（默认）：只预览，不写库
	rec, body = env.do(t, http.MethodPost, "/api/route-apply", owner, `{"model":"apply-x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	assert.Equal(t, true, data["dry_run"])
	assert.EqualValues(t, 0, data["updated"], "dry_run 不得写库")

	var baseBefore basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", baseChannelID).First(&baseBefore).Error)
	assert.Equal(t, 0, baseBefore.GetWeight(), "dry_run 之后权重应保持原样")

	// ③ 显式 dry_run=false：写入权重
	rec, body = env.do(t, http.MethodPost, "/api/route-apply", owner, `{"model":"apply-x","dry_run":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, false, data["dry_run"])
	assert.EqualValues(t, 1, data["updated"], "应更新 1 条底座渠道")

	var baseAfter basemodel.Channel
	require.NoError(t, env.db.Where("id = ?", baseChannelID).First(&baseAfter).Error)
	assert.Positive(t, baseAfter.GetWeight(), "投影后应写入权重")
	assert.Zero(t, *baseAfter.Priority, "默认投影**不得**改动 priority（避免抢占平台/他人优先级）")

	// ④ 缺 model → 400；未知策略 → 400
	rec, _ = env.do(t, http.MethodPost, "/api/route-apply", owner, `{"dry_run":true}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/route-apply", owner, `{"model":"apply-x","strategy":"bogus"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---- 价格：人工覆盖 / 单渠道同步 / 未知不为零（T2.1、T2.2 验收） ----

func TestPriceRuleEndpointsEnforceAcceptanceInvariants(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8901
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "价格组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"price-x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))

	// ① **价格未知 ≠ 零**：没有任何快照时，接口如实说"未知"，而不是给 0
	rec, body = env.do(t, http.MethodGet,
		"/api/price-rules?scope_type=0&scope_id="+strconv.FormatInt(offeringID, 10), owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ := body["data"].(map[string]any)
	assert.Equal(t, false, data["price_known"], "没有价格快照时必须标为未知")
	assert.Equal(t, "no_accepted_snapshot", data["reason"])

	// ② 普通用户不得设置价格（价格决定计费，属平台侧动作）
	rec, _ = env.doWithRole(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":2}`,
		common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// ③ 平台设置**人工覆盖**（并锁定）
	rec, body = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":2,"output_usd_per_million":8,"lock":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec, body = env.do(t, http.MethodGet,
		"/api/price-rules?scope_type=0&scope_id="+strconv.FormatInt(offeringID, 10), owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, true, data["price_known"])
	effective, _ := data["effective"].(map[string]any)
	assert.Equal(t, true, effective["manual_override"])
	assert.Equal(t, true, effective["locked"])
	dimensions, _ := effective["dimensions"].([]any)
	require.Len(t, dimensions, 2, "输入与输出两个维度")
	first, _ := dimensions[0].(map[string]any)
	assert.EqualValues(t, 2_000_000, first["usd_unit_price_micro"], "2 USD/1M tokens = 2000000 微美元/1M")

	// ④ **人工覆盖不被同步改写**：同一作用域发起同步 → 必须被跳过且不产生新版本
	rec, body = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/price-sync", owner,
		`{"source":5,"readings":[{"upstream_model_id":"price-x","input_usd_per_million":1,"output_usd_per_million":1}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.EqualValues(t, 0, data["applied"], "人工覆盖的价格不得被同步改写")
	assert.Equal(t, "manual_override", data["notes"].(map[string]any)["price-x"])

	rec, body = env.do(t, http.MethodGet,
		"/api/price-rules?scope_type=0&scope_id="+strconv.FormatInt(offeringID, 10), owner, "")
	data, _ = body["data"].(map[string]any)
	effective, _ = data["effective"].(map[string]any)
	assert.EqualValues(t, 1, effective["version"], "被挡下的同步不得产生新版本")
	versions, _ := data["versions"].([]any)
	require.Len(t, versions, 1, "版本历史里只有人工那一条")

	// ⑤ 未锁定的作用域可以同步，且**单渠道同步不污染他人**
	otherBase := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(otherBase), Name: "底座2", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	otherChannelID := env.createChannel(t, owner, groupID, otherBase)

	rec, body = env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(otherChannelID, 10)+"/price-sync", owner,
		`{"source":5,"readings":[{"upstream_model_id":"price-sync-x","input_usd_per_million":3}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.EqualValues(t, 1, data["applied"], "没有人工覆盖时应正常落库（落到渠道作用域）")

	// 另一个渠道（第一条渠道）**不受影响**
	rec, body = env.do(t, http.MethodGet,
		"/api/price-rules?scope_type=2&scope_id="+strconv.FormatInt(channelID, 10), owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, false, data["price_known"], "别的渠道不得因为这次同步而有价格")

	// 而同步过的渠道确实拿到了价格
	rec, body = env.do(t, http.MethodGet,
		"/api/price-rules?scope_type=2&scope_id="+strconv.FormatInt(otherChannelID, 10), owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, true, data["price_known"])

	// ⑥ 离谱价格被拒（把"每 1K"当"每 1M"这类口径错误不该进计费）
	rec, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":5000000}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestChannelPriceMatchPreviewAndConfirmUseServerCandidates(t *testing.T) {
	oldModels := ratio_setting.ModelRatio2JSONString()
	oldCompletions := ratio_setting.CompletionRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"apigx-api-match-flat":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"apigx-api-match-flat":4}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldModels))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(oldCompletions))
	})
	env := newTestEnv(t, true)
	const owner, other int64 = 8951, 8952
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{
		Id: int(baseChannelID), Name: "价格匹配底座", Key: "x", Status: common.ChannelStatusEnabled,
	}).Error)
	groupID := env.createGroup(t, owner, "价格匹配组")
	channelID := env.createChannel(t, owner, groupID, baseChannelID)

	createOffering := func(modelID string) int64 {
		t.Helper()
		rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
			`{"upstream_model_id":"`+modelID+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return int64(body["data"].(map[string]any)["id"].(float64))
	}
	flatOffering := createOffering("apigx-api-match-flat")
	expressionOffering := createOffering("gpt-6-astra")
	_ = createOffering("not-a-priced-model")

	path := "/api/channels/" + strconv.FormatInt(channelID, 10) + "/price-match/preview"
	rec, body := env.doWithRole(t, http.MethodPost, path, owner, "", common.RoleCommonUser)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := body["data"].(map[string]any)
	candidates := data["candidates"].([]any)
	require.Len(t, candidates, 3)
	var flatToken string
	for _, raw := range candidates {
		candidate := raw.(map[string]any)
		offeringID := int64(candidate["offering_id"].(float64))
		switch offeringID {
		case flatOffering:
			assert.Equal(t, "matched", candidate["status"])
			assert.Equal(t, true, candidate["confirmable"])
			flatToken, _ = candidate["candidate_token"].(string)
		case expressionOffering:
			assert.Equal(t, "expression_requires_manual", candidate["status"])
			assert.Equal(t, false, candidate["confirmable"])
			assert.NotEmpty(t, candidate["expression_hash"])
		}
	}
	require.NotEmpty(t, flatToken)

	confirmPath := "/api/channels/" + strconv.FormatInt(channelID, 10) + "/price-match/confirm"
	confirmBody := `{"selections":[{"offering_id":` + strconv.FormatInt(flatOffering, 10) +
		`,"candidate_token":"` + flatToken + `"}]}`
	rec, body = env.doWithRole(t, http.MethodPost, confirmPath, owner, confirmBody, common.RoleCommonUser)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.EqualValues(t, 1, body["data"].(map[string]any)["applied"])

	rec, _ = env.doWithRole(t, http.MethodPost, path, other, "", common.RoleCommonUser)
	assert.Equal(t, http.StatusNotFound, rec.Code, "他人不得预览渠道的价格候选")
	rec, _ = env.doWithRole(t, http.MethodPost, confirmPath, other, confirmBody, common.RoleCommonUser)
	assert.Equal(t, http.StatusNotFound, rec.Code, "他人不得确认渠道的价格候选")
}

// 能力矩阵：**支持范围必须可机读**，且"未取得真实凭据"的项不得标成 supported
// （计划书：尚未支持的维度不能在界面宣称已生效）。
func TestCapabilityMatrixIsHonest(t *testing.T) {
	env := newTestEnv(t, false)
	const owner int64 = 9001

	rec, body := env.do(t, http.MethodGet, "/api/capabilities", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	matrix, _ := data["matrix"].([]any)
	require.NotEmpty(t, matrix, "能力矩阵不能为空")

	byCap := map[string]map[string]any{}
	for _, item := range matrix {
		entry, _ := item.(map[string]any)
		name, _ := entry["capability"].(string)
		require.NotEmpty(t, name)
		require.Contains(t, []string{"supported", "unsupported"}, entry["status"],
			"状态取值必须受控（三类），否则界面无法据它决定怎么显示")
		require.NotEmpty(t, entry["evidence"], "%s 必须给出证据说明，否则界面只能靠猜", name)
		byCap[name] = entry
	}

	// 六类必须齐全（缺一类就意味着界面会漏掉一个能力）
	for _, name := range []string{"chat", "image", "embedding", "rerank", "audio", "video"} {
		assert.Contains(t, byCap, name, "六类能力都要在矩阵里")
	}

	// 明确不支持项必须在矩阵里出现（否则界面会显示成"可用"）
	limits, _ := byCap["video"]["limitations"].([]any)
	joined := ""
	for _, l := range limits {
		joined += l.(string) + " "
	}
	assert.Contains(t, joined, "不支持取消", "视频不支持取消必须写进矩阵，界面据此显示能力限制")

}

// 任务保留策略：默认值与非法配置的行为必须明确（计划书 §4 要求展示保留期限）。
func TestTaskRetentionPolicy(t *testing.T) {
	env := newTestEnv(t, false)
	const owner int64 = 9101

	t.Setenv("APIGX_TASK_RETENTION_DAYS", "")
	rec, body := env.do(t, http.MethodGet, "/api/task-retention", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := body["data"].(map[string]any)
	assert.Equal(t, float64(taskRetentionDefaultDays), data["days"], "未配置时应给出默认值")
	assert.Equal(t, "default", data["source"])

	t.Setenv("APIGX_TASK_RETENTION_DAYS", "30")
	rec, body = env.do(t, http.MethodGet, "/api/task-retention", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = body["data"].(map[string]any)
	assert.Equal(t, float64(30), data["days"])
	assert.Equal(t, "env", data["source"])

	// 非法配置必须报错，而不是悄悄回退成默认值（否则运维会以为配置生效了）
	t.Setenv("APIGX_TASK_RETENTION_DAYS", "abc")
	rec, _ = env.do(t, http.MethodGet, "/api/task-retention", owner, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// 分组的展示名与简介：计划书 §4 要求"分享名称、介绍"，且说明必须能在列表里读到。
func TestShareGroupNameAndDescription(t *testing.T) {
	env := newTestEnv(t, false)
	const owner int64 = 9201

	rec, body := env.do(t, http.MethodPost, "/api/share-groups", owner, `{"name":"初始分组","default_visibility":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	groupID := int64(body["data"].(map[string]any)["id"].(float64))

	// 改名 + 写简介
	rec, _ = env.do(t, http.MethodPatch, "/api/share-groups/"+strconv.FormatInt(groupID, 10), owner,
		`{"name":"改过的名字","description":"这是一段分享说明"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 列表里必须能读到（否则前端"分享说明"就无从显示）
	rec, body = env.do(t, http.MethodGet, "/api/share-groups", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, _ := body["data"].(map[string]any)["items"].([]any)
	require.NotEmpty(t, items)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "改过的名字", first["name"])
	assert.Equal(t, "这是一段分享说明", first["description"], "简介必须在列表里返回")

	// 非法名称必须被拒（空名 / 超长），而不是写进去
	rec, _ = env.do(t, http.MethodPatch, "/api/share-groups/"+strconv.FormatInt(groupID, 10), owner, `{"name":"   "}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = env.do(t, http.MethodPatch, "/api/share-groups/"+strconv.FormatInt(groupID, 10), owner,
		`{"name":"`+strings.Repeat("字", 65)+`"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// 广场必须带**质量摘要**（计划书 §4），且**样本不足时要如实标注**而不是编一个数字。
func TestPlazaCarriesQualitySummary(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 9301

	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "质量组", DefaultVisibility: apigxmodel.ShareGroupVisibilityPublic, PublishStatus: apigxmodel.ShareGroupPublishPublished}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座质量", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	channelID := env.createChannel(t, owner, group.ID, baseChannelID)

	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"quality-model"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))

	// 先配价再发布（与产品规则一致：未配置价格不得发布）
	rec, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, offeringID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, owner, offeringID))

	rec, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, _ := body["data"].(map[string]any)["items"].([]any)
	require.NotEmpty(t, items, "已发布且分组公开的供给应当出现在广场")
	first, _ := items[0].(map[string]any)

	// 关键字段必须在（没有样本时也要在，只是 insufficient=true）
	require.Contains(t, first, "reliability_lower_bound_ppm")
	require.Contains(t, first, "sample_count")
	assert.Equal(t, true, first["insufficient"], "没有样本时必须标为样本不足，而不是给出一个看起来可信的数字")
	assert.Equal(t, float64(0), first["reliability_lower_bound_ppm"])
}

// 对外标准模型 ID 与介绍：**只有映射已确认时**广场才显示（§8.2）。未确认时显示上游名，
// 绝不把"候选"当结论 —— 否则客户端会按一个没人确认过的标准 ID 去调用。
func TestPlazaCanonicalModelOnlyAfterConfirmation(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 9401

	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "目录组", DefaultVisibility: apigxmodel.ShareGroupVisibilityPublic, PublishStatus: apigxmodel.ShareGroupPublishPublished}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座目录", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	channelID := env.createChannel(t, owner, group.ID, baseChannelID)

	// 建供给会自动登记"候选"别名（见 repo_offering.go）
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"upstream-name-x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))
	_, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, offeringID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, owner, offeringID))

	// 候选阶段：标准 ID 与介绍必须为空
	_, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	items, _ := body["data"].(map[string]any)["items"].([]any)
	require.NotEmpty(t, items)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "", first["canonical_model_id"], "未确认的映射不得对外给出标准模型 ID")
	assert.Equal(t, "", first["model_description"])

	// 确认映射后：标准 ID 与介绍出现
	canonical := apigxmodel.CanonicalModel{APIModelID: "standard-model-1", Description: "这是标准模型的介绍", ModalityType: 1, Status: 1}
	require.NoError(t, env.db.Create(&canonical).Error)
	require.NoError(t, env.db.Model(&apigxmodel.ModelAlias{}).
		Where("normalized_model_id = ?", "upstream-name-x").
		Updates(map[string]any{"relation": apigxmodel.AliasRelationConfirmed,
			"confirmation_state": apigxmodel.AliasConfirmApproved, "target_canonical_model_id": canonical.ID}).Error)

	_, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	items, _ = body["data"].(map[string]any)["items"].([]any)
	first, _ = items[0].(map[string]any)
	assert.Equal(t, "standard-model-1", first["canonical_model_id"])
	assert.Equal(t, "这是标准模型的介绍", first["model_description"])
}

// 别名确认工作流：计划书 §4「批量匹配、人工覆盖」。重点是**单向推进**与"确认后才对外生效"。
func TestAliasConfirmationWorkflow(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 9501

	// 造三条候选：两条同名（大小写/空白不同）、一条不同名
	require.NoError(t, apigxmodel.RecordAliasCandidate(env.db, apigxmodel.AliasScopePlatform, 0, 0, "Vendor-Model-A"))
	require.NoError(t, apigxmodel.RecordAliasCandidate(env.db, apigxmodel.AliasScopePlatform, 0, 0, "  vendor-model-a "))
	require.NoError(t, apigxmodel.RecordAliasCandidate(env.db, apigxmodel.AliasScopePlatform, 0, 0, "vendor-model-b"))

	rec, body := env.do(t, http.MethodGet, "/api/model-catalog/candidates", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, _ := body["data"].(map[string]any)["items"].([]any)
	// 登记了三个**名字**，但其中两个归一化后同名 => 只会有 2 条候选（幂等生效）
	require.Len(t, items, 2, "同名候选应被归一化去重")
	// 按**身份**取行，不按下标 —— 列表按 id 倒序，用下标会取错（我踩过）
	var candidateA, candidateB apigxmodel.ModelAlias
	require.NoError(t, env.db.Where("normalized_model_id = ?", "vendor-model-a").First(&candidateA).Error)
	require.NoError(t, env.db.Where("normalized_model_id = ?", "vendor-model-b").First(&candidateB).Error)
	id := candidateA.ID

	// 确认：**顺手创建标准模型**
	rec, body = env.do(t, http.MethodPost, "/api/model-catalog/candidates/"+strconv.FormatInt(id, 10)+"/confirm", owner,
		`{"api_model_id":"vendor-model-a","description":"介绍A","modality_type":1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 重复确认必须 404（单向：已经确认过的不再改动）
	rec, _ = env.do(t, http.MethodPost, "/api/model-catalog/candidates/"+strconv.FormatInt(id, 10)+"/confirm", owner,
		`{"api_model_id":"vendor-model-a"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "重复确认应被拒（单向推进）")

	// 已确认的不能再被拒绝
	rec, _ = env.do(t, http.MethodPost, "/api/model-catalog/candidates/"+strconv.FormatInt(id, 10)+"/reject", owner, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "已确认的候选不得被拒绝")

	// 另一条候选：拒绝后不能再确认
	rec, _ = env.do(t, http.MethodPost, "/api/model-catalog/candidates/"+strconv.FormatInt(candidateB.ID, 10)+"/reject", owner, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/model-catalog/candidates/"+strconv.FormatInt(candidateB.ID, 10)+"/confirm", owner,
		`{"api_model_id":"vendor-model-b"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "已拒绝的候选不得再被确认")

	// 批量匹配：把所有**归一化后同名**的待确认候选一次确认（此处同名两条已被归一化为一条，故为 0 或 1）
	require.NoError(t, apigxmodel.RecordAliasCandidate(env.db, apigxmodel.AliasScopePlatform, 0, 0, "bulk-name-x"))
	rec, body = env.do(t, http.MethodPost, "/api/model-catalog/bulk-confirm", owner,
		`{"normalized_model_id":"bulk-name-x","api_model_id":"bulk-name-x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	affected := body["data"].(map[string]any)["confirmed"].(float64)
	assert.EqualValues(t, 1, affected, "批量匹配应确认同名候选")

	// 批量确认后，解析就通了（只认已确认）
	canonical, okModel, err := apigxmodel.ResolveCanonicalModel(env.db, "BULK-NAME-X")
	require.NoError(t, err)
	require.True(t, okModel)
	assert.Equal(t, "bulk-name-x", canonical.APIModelID)
}

// 授权必须**真正生效**：受限（非公开）分组对"受邀者"可见，对陌生人**不可见**。
//
// 这是"邀请兑换"有没有意义的判据 —— 只把授权写进表、广场仍只放行公开分组，等于没做。
// 列表与详情**两条路径都覆盖**（计划书要求两者判据一致）。
func TestPlazaHonoursGrantsForLimitedGroups(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, invited, stranger int64 = 9601, 9602, 9603

	// 受限分组（1=受限；广场默认只放行公开=2）
	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "受限组", DefaultVisibility: apigxmodel.ShareGroupVisibilityLimited, PublishStatus: apigxmodel.ShareGroupPublishPublished}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))
	baseChannelID := nextBaseChannelID()
	require.NoError(t, env.db.Create(&basemodel.Channel{Id: int(baseChannelID), Name: "底座受限", Key: "x", Status: common.ChannelStatusEnabled}).Error)
	channelID := env.createChannel(t, owner, group.ID, baseChannelID)

	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"limited-model"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	offeringID := int64(body["data"].(map[string]any)["id"].(float64))
	publicID := body["data"].(map[string]any)["public_id"].(string)

	// 先配价再发布（产品规则：未配价不得发布）
	_, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(offeringID, 10)+`,"input_usd_per_million":1,"output_usd_per_million":2}`)
	require.NoError(t, apigxmodel.SubmitModelOffering(env.db, owner, offeringID))
	require.NoError(t, apigxmodel.PublishModelOffering(env.db, owner, offeringID))

	// ① 未授权时：陌生人看不到（列表空），详情 404
	_, body = env.do(t, http.MethodGet, "/api/plaza", stranger, "")
	assert.Empty(t, body["data"].(map[string]any)["items"], "受限分组未授权前不得出现在陌生人的广场")
	rec, _ = env.do(t, http.MethodGet, "/api/plaza/"+publicID, stranger, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "受限供给的详情对未授权者不存在")

	// ② 所有者**也不**在广场看到它 —— 广场是"公开 or 受邀"的发现面，
	//    分享者看自己的东西走「我的分享」页（既有用例 TestPlazaOnlyExposesPublishedAndPublic
	//    对"私有组"就是这条口径；此处对"受限组"保持一致）。
	_, body = env.do(t, http.MethodGet, "/api/plaza", owner, "")
	items, _ := body["data"].(map[string]any)["items"].([]any)
	assert.Empty(t, items, "广场只放行公开或受邀，所有者应去「我的分享」看自己的受限供给")

	// ③ 授予访问权（指定用户，§4）：受邀者可见
	grant := apigxmodel.Grant{
		SubjectType: apigxmodel.GrantSubjectUser, SubjectID: invited,
		ResourceType: apigxmodel.GrantResourceGroup, ResourceID: group.ID,
		ScopeKind: apigxmodel.GrantScopeAssigned, GrantedByUserID: owner,
	}
	require.NoError(t, env.db.Create(&grant).Error)

	_, body = env.do(t, http.MethodGet, "/api/plaza", invited, "")
	items, _ = body["data"].(map[string]any)["items"].([]any)
	require.NotEmpty(t, items, "被授权者应当看得见受限分组的供给")
	rec, _ = env.do(t, http.MethodGet, "/api/plaza/"+publicID, invited, "")
	assert.Equal(t, http.StatusOK, rec.Code, "被授权者的详情应当可见")

	// ④ 陌生人仍不可见（授权是**按主体**的，不是"一旦有人被授权就公开"）
	_, body = env.do(t, http.MethodGet, "/api/plaza", stranger, "")
	assert.Empty(t, body["data"].(map[string]any)["items"], "授权只对受邀者生效，陌生人仍不可见")
	rec, _ = env.do(t, http.MethodGet, "/api/plaza/"+publicID, stranger, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// ⑤ 授权**到期**后不可见（撤销与到期）
	require.NoError(t, env.db.Model(&apigxmodel.Grant{}).Where("id = ?", grant.ID).Update("expires_at", 1000).Error)
	_, body = env.do(t, http.MethodGet, "/api/plaza", invited, "")
	assert.Empty(t, body["data"].(map[string]any)["items"], "授权到期后不应再可见")
}

// 访问控制接口的**契约与越权防护**（扩展既有测试文件，不新开文件）。
//
// 覆盖：发放邀请（明文只回一次）→ 访客兑换 → 获得访问权；以及
// **非所有者**对他人资源发邀请/授权/查授权列表一律 404（不确认"存在但不属于你"）。
func TestAccessInviteAndGrantAPIContract(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, guest, stranger int64 = 9801, 9802, 9803

	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "邀请组", DefaultVisibility: apigxmodel.ShareGroupVisibilityLimited, PublishStatus: apigxmodel.ShareGroupPublishPublished}
	require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))

	// ① 发放邀请：明文只在这里返回一次
	rec, body := env.do(t, http.MethodPost, "/api/access/invites", owner,
		`{"target_scope_type":1,"target_scope_id":`+strconv.FormatInt(group.ID, 10)+`,"max_uses":1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	token := body["data"].(map[string]any)["token"].(string)
	require.NotEmpty(t, token)

	// 库里只有哈希（明文不入库）
	var stored apigxmodel.Invite
	require.NoError(t, env.db.Where("created_by_user_id = ?", owner).First(&stored).Error)
	assert.NotEqual(t, token, stored.TokenHash, "邀请码明文不得入库")

	// 列表接口**不回**明文/哈希
	_, body = env.do(t, http.MethodGet, "/api/access/invites", owner, "")
	list := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, list, 1)
	first := list[0].(map[string]any)
	assert.NotContains(t, first, "token")
	assert.NotContains(t, first, "token_hash")

	// ② **越权防护**：别人不能对他人资源发邀请，也不能查他人资源上的授权
	rec, _ = env.do(t, http.MethodPost, "/api/access/invites", stranger,
		`{"target_scope_type":1,"target_scope_id":`+strconv.FormatInt(group.ID, 10)+`,"max_uses":1}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "非所有者发邀请必须 404")
	rec, _ = env.do(t, http.MethodGet, "/api/access/grants?resource_type=0&resource_id="+strconv.FormatInt(group.ID, 10), stranger, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "非所有者查授权列表必须 404")
	rec, _ = env.do(t, http.MethodPost, "/api/access/grants", stranger,
		`{"resource_type":0,"resource_id":`+strconv.FormatInt(group.ID, 10)+`,"target_user_id":`+strconv.FormatInt(guest, 10)+`}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "非所有者授权必须 404")

	// ③ 兑换前：访客无访问权；兑换后有
	canBefore, err := apigxmodel.SubjectCanAccessResource(env.db, apigxmodel.GrantSubjectUser, guest, apigxmodel.GrantResourceGroup, group.ID, 0)
	require.NoError(t, err)
	assert.False(t, canBefore)

	rec, body = env.do(t, http.MethodPost, "/api/access/invites/redeem", guest, `{"token":"`+token+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.EqualValues(t, apigxmodel.GrantScopeRedeemed, body["data"].(map[string]any)["scope_kind"])

	canAfter, err := apigxmodel.SubjectCanAccessResource(env.db, apigxmodel.GrantSubjectUser, guest, apigxmodel.GrantResourceGroup, group.ID, 0)
	require.NoError(t, err)
	assert.True(t, canAfter, "兑换后应当有访问权")

	// ④ 单次邀请：第二个人再兑换必须 404（且与"不存在"同一结果）
	rec, _ = env.do(t, http.MethodPost, "/api/access/invites/redeem", stranger, `{"token":"`+token+`"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "已用尽的单次邀请必须被拒")

	// ⑤ 指定用户授权 + 撤销
	rec, body = env.do(t, http.MethodPost, "/api/access/grants", owner,
		`{"resource_type":0,"resource_id":`+strconv.FormatInt(group.ID, 10)+`,"target_user_id":`+strconv.FormatInt(stranger, 10)+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	grantID := int64(body["data"].(map[string]any)["id"].(float64))

	rec, _ = env.do(t, http.MethodDelete, "/api/access/grants/"+strconv.FormatInt(grantID, 10), owner, "")
	assert.Equal(t, http.StatusOK, rec.Code)

	canStranger, err := apigxmodel.SubjectCanAccessResource(env.db, apigxmodel.GrantSubjectUser, stranger, apigxmodel.GrantResourceGroup, group.ID, 0)
	require.NoError(t, err)
	assert.False(t, canStranger, "撤销后不应再有访问权")

	// ⑥ 撤销邀请可连带撤销由它产生的授权
	inviteID := stored.ID
	rec, _ = env.do(t, http.MethodPost, "/api/access/invites/"+strconv.FormatInt(inviteID, 10)+"/revoke?revoke_grants=true", owner, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	canGuest, err := apigxmodel.SubjectCanAccessResource(env.db, apigxmodel.GrantSubjectUser, guest, apigxmodel.GrantResourceGroup, group.ID, 0)
	require.NoError(t, err)
	assert.False(t, canGuest, "连带撤销后访客不应再有访问权")
}

// TestPlazaAggregatesSameModelFromTwoSharers 覆盖阶段 4 验收表第 1 行：
// 「相同标准模型来自两位分享者 ⇒ 广场聚合，来源和分享名称分别可见」。
//
// 断言的是**可观察事实**：同一模型在广场上出现两条来源，且两条的分享分组名不同、分属两位分享者。
func TestPlazaAggregatesSameModelFromTwoSharers(t *testing.T) {

	env := newTestEnv(t, true)
	const sharerA, sharerB int64 = 7701, 7702
	const sharedModel = "glm-5.3-flash"

	// 造数方式**逐行对齐**既有的可过用例（TestPlazaOnlyExposesPublishedAndPublic）：
	// 分组用仓储直建（公开 + 已发布）、底座渠道用 nextBaseChannelID()，
	// 因为 API 夹具走的路径与它不一致，曾导致广场取不到数据。
	mk := func(owner int64, name string) (int64, int64) {
		group := &apigxmodel.ShareGroup{
			OwnerUserID:       owner,
			Name:              name,
			DefaultVisibility: apigxmodel.ShareGroupVisibilityPublic,
			PublishStatus:     apigxmodel.ShareGroupPublishPublished,
		}
		require.NoError(t, apigxmodel.CreateShareGroup(env.db, group))
		baseID := nextBaseChannelID()
		require.NoError(t, env.db.Create(&basemodel.Channel{
			Id: int(baseID), Name: "底座", Key: "x", Status: common.ChannelStatusEnabled,
		}).Error)
		chID := env.createChannel(t, owner, group.ID, baseID)
		publishTestOffering(t, env, owner, chID, sharedModel)
		return group.ID, chID
	}
	mk(sharerA, "A 的公益站")
	mk(sharerB, "B 的公益站")

	// 用**第三方**浏览者：广场判据刻意不含"本人所有"（分享者看自己的东西走「我的分享」）
	const bystander int64 = 7799
	_, body := env.do(t, http.MethodGet, "/api/plaza", bystander, "")
	items, _ := body["data"].(map[string]any)["items"].([]any)

	var sameModel []map[string]any
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["model"] == sharedModel {
			sameModel = append(sameModel, item)
		}
	}
	require.Len(t, sameModel, 2, "同一模型应有两项来源")

	names := map[string]bool{}
	for _, item := range sameModel {
		name, _ := item["group_name"].(string)
		names[name] = true
	}
	assert.True(t, names["A 的公益站"], "应能看到 A 的分享名称")
	assert.True(t, names["B 的公益站"], "应能看到 B 的分享名称")
}

// TestSuspendedGroupStopsServing 覆盖阶段 4 验收表第 25 行：
// 「分享者暂停与撤销授权 ⇒ 新请求及时停止」。
//
// 这里用**分组暂停**（U-1 引入的发布状态闸门）验证"新请求不再选到该渠道"，
// 因为暂停的直接后果就是供给不再进入路由候选。
func TestSuspendedGroupStopsServing(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 7801
	const model = "suspended-model"

	groupID := env.createGroup(t, owner, "会被暂停的分组")
	channelID := env.createChannel(t, owner, groupID, 70003)
	publishTestOffering(t, env, owner, channelID, model)

	// 暂停前：能装配出候选（至少一条，且未被排除为"未发布"）
	before, _, err := apigxrouting.AssembleCandidates(env.db, nil, model, apigxrouting.AssembleOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, before, "暂停前应有候选")

	require.NoError(t, apigxmodel.SetShareGroupPublishStatus(env.db, groupID, apigxmodel.ShareGroupPublishSuspended))

	after, _, err := apigxrouting.AssembleCandidates(env.db, nil, model, apigxrouting.AssembleOptions{})
	require.NoError(t, err)
	assert.Empty(t, after, "分组暂停后不应再有候选（新请求及时停止）")
}

// publishTestOffering 造一条"平台已审核通过"的供给（新建→提交→发布）。
func publishTestOffering(t *testing.T, env *testEnv, owner, channelID int64, model string) {
	t.Helper()
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/offerings", owner,
		`{"upstream_model_id":"`+model+`","capabilities":{"streaming":true,"vision":true,"tools":true}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	id, ok := body["data"].(map[string]any)["id"].(float64)
	require.True(t, ok, "建供给应返回 id：%s", rec.Body.String())

	// 先配价（未配价不可发布 —— 计划书 §5 的硬要求，测试里也必须遵守）
	rec, _ = env.do(t, http.MethodPut, "/api/price-rules", owner,
		`{"scope_type":0,"scope_id":`+strconv.FormatInt(int64(id), 10)+`,"input_usd_per_million":2,"output_usd_per_million":8,"lock":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(int64(id), 10)+"/submit", owner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = env.do(t, http.MethodPost, "/api/offerings/"+strconv.FormatInt(int64(id), 10)+"/publish", owner, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestKeyPoolDetailsAreOwnerOnly 覆盖阶段 4 验收表第 20 行：
// 「一个渠道配置多把 Key、指定该渠道调用 ⇒ 遵守其池策略，**使用者仍只能看到分享来源和授权结果**」。
//
// 可验证且最关键的一半：Key 池细节（有哪些 Key、权重、状态）**只对所有者可见**，
// 非所有者读该渠道的 Key 池一律 404（不确认存在）。
func TestKeyPoolDetailsAreOwnerOnly(t *testing.T) {
	env := newTestEnv(t, true)
	const owner, stranger int64 = 8801, 8802

	groupID := env.createGroup(t, owner, "池细节分组")
	channelID := env.createChannel(t, owner, groupID, 71001)

	// 所有者能读
	rec, _ := env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner, "")
	assert.Equal(t, http.StatusOK, rec.Code, "所有者应能读取自己渠道的 Key 池")

	// 陌生人（使用者视角）不能读
	rec, _ = env.do(t, http.MethodGet, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", stranger, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "非所有者读取 Key 池必须 404（不确认存在）")
}
