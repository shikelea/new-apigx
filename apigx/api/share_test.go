package apigxapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/secretbox"
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
