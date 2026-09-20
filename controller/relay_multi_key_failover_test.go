package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRelayExhaustsCurrentKeyPoolBeforeChangingAuthorizedChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	usedKeys := make([]string, 0, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		usedKeys = append(usedKeys, key)
		w.Header().Set("Content-Type", "application/json")
		if key == "key-a" || key == "key-b" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limited"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-g2","object":"chat.completion","created":1,"model":"g2-failover-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	database := setupRelayMultiKeyDatabase(t)
	first := relayMultiKeyChannel(7101, "primary", upstream.URL, "key-a\nkey-b", []string{"stable-a", "stable-b"})
	second := relayMultiKeyChannel(7102, "fallback", upstream.URL, "key-c", []string{"stable-c"})
	unauthorized := relayMultiKeyChannel(7103, "unauthorized", upstream.URL, "key-z", []string{"stable-z"})
	for _, channel := range []*model.Channel{first, second, unauthorized} {
		require.NoError(t, database.Create(channel).Error)
		priority := int64(0)
		require.NoError(t, database.Create(&model.Ability{
			Group: "default", Model: "g2-failover-model", ChannelId: channel.Id,
			Enabled: true, Priority: &priority, Weight: 10,
		}).Error)
	}
	model.InitChannelCache()

	recorder := httptest.NewRecorder()
	c := newRelayMultiKeyContext(t, recorder, first, []int{first.Id, second.Id})

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, []string{"key-a", "key-b", "key-c"}, usedKeys)
	assert.NotContains(t, usedKeys, "key-z")
}

func TestRelayRetriesAnotherKeyAfterUnauthorizedWithNoGlobalRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	usedKeys := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		usedKeys = append(usedKeys, key)
		w.Header().Set("Content-Type", "application/json")
		if key == "key-a" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key","type":"authentication_error","code":"invalid_api_key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-g2","object":"chat.completion","created":1,"model":"g2-failover-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	database := setupRelayMultiKeyDatabase(t)
	common.RetryTimes = 0
	channel := relayMultiKeyChannel(7201, "same-channel", upstream.URL, "key-a\nkey-b", []string{"stable-a", "stable-b"})
	require.NoError(t, database.Create(channel).Error)
	require.NoError(t, database.Create(relayMultiKeyAbility(channel.Id)).Error)
	model.InitChannelCache()

	recorder := httptest.NewRecorder()
	c := newRelayMultiKeyContext(t, recorder, channel, nil)
	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, []string{"key-a", "key-b"}, usedKeys)
}

func TestRelayDoesNotRotateKeyAfterBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	usedKeys := make([]string, 0, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usedKeys = append(usedKeys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid request","type":"invalid_request_error","code":"invalid_request"}}`))
	}))
	t.Cleanup(upstream.Close)

	database := setupRelayMultiKeyDatabase(t)
	channel := relayMultiKeyChannel(7301, "bad-request", upstream.URL, "key-a\nkey-b", []string{"stable-a", "stable-b"})
	require.NoError(t, database.Create(channel).Error)
	require.NoError(t, database.Create(relayMultiKeyAbility(channel.Id)).Error)
	model.InitChannelCache()

	recorder := httptest.NewRecorder()
	c := newRelayMultiKeyContext(t, recorder, channel, nil)
	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	assert.Equal(t, []string{"key-a"}, usedKeys)
}

func TestRelayDoesNotRetryAfterStreamingResponseStarted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	usedKeys := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-api-key")
		usedKeys = append(usedKeys, key)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-g3\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"g2-failover-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"))
		_, _ = w.Write([]byte("data: {not-json}\n\n"))
	}))
	t.Cleanup(upstream.Close)

	database := setupRelayMultiKeyDatabase(t)
	channel := relayMultiKeyChannel(7401, "streaming", upstream.URL, "key-a\nkey-b", []string{"stable-a", "stable-b"})
	channel.Type = constant.ChannelTypeAnthropic
	require.NoError(t, database.Create(channel).Error)
	require.NoError(t, database.Create(relayMultiKeyAbility(channel.Id)).Error)
	model.InitChannelCache()

	recorder := httptest.NewRecorder()
	c := newRelayMultiKeyContext(t, recorder, channel, nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"g2-failover-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, []string{"key-a"}, usedKeys)
	assert.Contains(t, recorder.Body.String(), "partial")
	assert.NotContains(t, recorder.Body.String(), `"error":`)
}

func newRelayMultiKeyContext(t *testing.T, recorder *httptest.ResponseRecorder, channel *model.Channel, allowedChannelIDs []int) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"g2-failover-model","messages":[{"role":"user","content":"hello"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyUserId, 1)
	common.SetContextKey(c, constant.ContextKeyUserQuota, 1_000_000)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "g2-failover-model")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, true)
	common.SetContextKey(c, constant.ContextKeyUserSetting, kitdto.UserSetting{AcceptUnsetRatioModel: true})
	if len(allowedChannelIDs) > 0 {
		service.GetChannelConstraints(c).AddFilter(taskdto.ChannelFilter{
			Kind: taskdto.FilterAllowedChannelIds, AllowedChannelIds: allowedChannelIDs,
		})
	}
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, "g2-failover-model"))
	return c
}

func setupRelayMultiKeyDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousRedis := common.RedisEnabled
	previousRetries := common.RetryTimes
	previousErrorLog := constant.ErrorLogEnabled
	previousConsumeLog := common.LogConsumeEnabled
	previousCountToken := constant.CountToken
	previousStreamingTimeout := constant.StreamingTimeout
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.UserSubscription{}))
	require.NoError(t, database.Create(&model.User{
		Id: 1, Username: "g2-relay-user", Group: "default", Quota: 1_000_000,
		Status: common.UserStatusEnabled,
	}).Error)
	model.DB, model.LOG_DB = database, database
	common.MemoryCacheEnabled = true
	common.RedisEnabled = false
	common.RetryTimes = 1
	constant.ErrorLogEnabled = false
	common.LogConsumeEnabled = false
	constant.CountToken = false
	constant.StreamingTimeout = 30
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":0}`))
	t.Setenv("APIGX_SHARED_MODE", "false")
	t.Setenv("APIGX_KEY_STATS_ENABLED", "false")
	t.Setenv("APIGX_QUALITY_ENABLED", "false")

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.RedisEnabled = previousRedis
		common.RetryTimes = previousRetries
		constant.ErrorLogEnabled = previousErrorLog
		common.LogConsumeEnabled = previousConsumeLog
		constant.CountToken = previousCountToken
		constant.StreamingTimeout = previousStreamingTimeout
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		require.NoError(t, sqlDB.Close())
	})
	return database
}

func relayMultiKeyChannel(id int, name, baseURL, keys string, stableIDs []string) *model.Channel {
	autoBan := 0
	return &model.Channel{
		Id: id, Name: name, Type: constant.ChannelTypeOpenAI, Key: keys,
		Status: common.ChannelStatusEnabled, BaseURL: &baseURL, Models: "g2-failover-model",
		Group: "default", AutoBan: &autoBan,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeySize: len(stableIDs),
			MultiKeyMode: constant.MultiKeyModePolling, MultiKeyStableIDs: stableIDs,
		},
	}
}

func relayMultiKeyAbility(channelID int) *model.Ability {
	priority := int64(0)
	return &model.Ability{
		Group: "default", Model: "g2-failover-model", ChannelId: channelID,
		Enabled: true, Priority: &priority, Weight: 10,
	}
}
