package apigxapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	basemodel "github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	gin.SetMode(gin.TestMode)

	dsn := filepath.Join(t.TempDir(), "apigx_api_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))
	// 投影会调用底座的 channel.Update()（并同步 abilities），因此接口层测试也要有底座表，
	// 且底座的全局 DB 必须指向同一个库（生产由启动路径设置）。
	require.NoError(t, db.AutoMigrate(&basemodel.Channel{}, &basemodel.Ability{}))
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
	Register(api.Group(""), apigxmodel.DefaultDB())
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
	return int64(id)
}

func (e *testEnv) createChannel(t *testing.T, userID, groupID, baseChannelID int64) int64 {
	t.Helper()
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

func TestShareGroupLifecycle(t *testing.T) {
	env := newTestEnv(t, true)
	const user int64 = 1001

	id := env.createGroup(t, user, "小林公益站")

	// 列表能看到
	rec, body := env.do(t, http.MethodGet, "/api/share-groups", user, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	require.Len(t, items, 1)

	// 详情
	rec, body = env.do(t, http.MethodGet, "/api/share-groups/"+strconv.FormatInt(id, 10), user, "")
	require.Equal(t, http.StatusOK, rec.Code)
	detail, _ := body["data"].(map[string]any)
	assert.Equal(t, "小林公益站", detail["name"])
	assert.Equal(t, float64(0), detail["publish_status"], "新建分组默认待审核")

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

	// 缺 base_channel_id → 400（该列有唯一索引，未绑定会互相冲突）
	rec, _ := env.do(t, http.MethodPost, "/api/channels", ownerA,
		`{"share_group_id":`+strconv.FormatInt(groupA, 10)+`,"name":"未绑定"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "base_channel_id")

	// 用别人的分组 → 404
	rec, _ = env.do(t, http.MethodPost, "/api/channels", ownerB,
		`{"share_group_id":`+strconv.FormatInt(groupA, 10)+`,"base_channel_id":60001,"name":"偷渡"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
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

	// 正常创建
	rec, body := env.do(t, http.MethodPost, "/api/channels/"+strconv.FormatInt(channelID, 10)+"/keys", owner,
		`{"stable_key_id":"s1","name":"k1","key":"sk-abcdefgh"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	data, _ := body["data"].(map[string]any)
	keyID := int64(data["id"].(float64))

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

// ---- 广场与详情（T1.9） ----

func TestPlazaOnlyExposesPublishedAndPublic(t *testing.T) {
	env := newTestEnv(t, true)
	const owner int64 = 8501

	// 分组设为**公开**（可见性 2）；否则即便已发布也不该出现在广场
	group := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "公开组", DefaultVisibility: apigxmodel.ShareGroupVisibilityPublic, PublishStatus: 1}
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
	privateGroup := &apigxmodel.ShareGroup{OwnerUserID: owner, Name: "私有组", DefaultVisibility: apigxmodel.ShareGroupVisibilityPrivate, PublishStatus: 1}
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
