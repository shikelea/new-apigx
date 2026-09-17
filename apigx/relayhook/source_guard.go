package relayhook

import (
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 平台 Key 的「允许来源」准入（T1.10 执行侧）。
//
// 语义：
//   - 平台 Key 没有配置偏好、或范围模式不是「允许来源」→ 放行（默认行为不变）；
//   - 选中的渠道**不是**本平台的分享渠道（例如管理员自建）→ 拒绝：限制了来源的 Key
//     只应当使用分享渠道，宁可拒绝也不要悄悄绕开限制；
//   - 渠道所属分享者**不在允许列表**时：
//   - `AllowFallback=true` → 返回**可重试**错误，交给底座既有的重试机制**重新选择**渠道
//     （复用底座的重选，不自己写候选循环，见 docs/stage0/upstream-diff.md D-016）；
//   - `AllowFallback=false` → 直接拒绝（不可回退）。
//
// 开关默认开启，但**只有配置了「允许来源」偏好的 Key 才会被影响**，因此对既有行为是惰性的。
var sourceGuardEnabled = common.GetEnvOrDefaultBool("APIGX_SOURCE_GUARD_ENABLED", true)

// SetSourceGuardEnabled 运行期开关（供测试与将来的后台配置使用）。
func SetSourceGuardEnabled(on bool) { sourceGuardEnabled = on }

// IsSourceGuardEnabled 报告当前是否开启来源准入。
func IsSourceGuardEnabled() bool { return sourceGuardEnabled }

const preferenceCacheTTL = 30 * time.Second

type cachedPreferences struct {
	key       *apigxmodel.APIKey
	found     bool
	expiresAt time.Time
}

var (
	prefCacheMu sync.RWMutex
	prefCache   = map[int64]cachedPreferences{} // key = 平台 Key（底座令牌）ID
)

// loadPreferences 读取某令牌的 APIGX 偏好（带短 TTL 缓存：准入在每个请求上都要判断）。
func loadPreferences(db *gorm.DB, userID, tokenID int64) (*apigxmodel.APIKey, bool) {
	if db == nil || tokenID <= 0 || userID <= 0 {
		return nil, false
	}
	now := time.Now()
	prefCacheMu.RLock()
	if cached, ok := prefCache[tokenID]; ok && now.Before(cached.expiresAt) {
		prefCacheMu.RUnlock()
		return cached.key, cached.found
	}
	prefCacheMu.RUnlock()

	key, err := apigxmodel.GetAPIKeyByToken(db, userID, tokenID)
	found := err == nil
	if !found && !errors.Is(err, apigxmodel.ErrNotFound) {
		common.SysError("relayhook: load platform key preferences failed: " + err.Error())
	}

	prefCacheMu.Lock()
	prefCache[tokenID] = cachedPreferences{key: key, found: found, expiresAt: now.Add(preferenceCacheTTL)}
	prefCacheMu.Unlock()
	return key, found
}

// InvalidatePreferenceCache 丢弃某令牌的偏好缓存（偏好变更后调用，否则最长一个 TTL 才生效）。
func InvalidatePreferenceCache(tokenID int64) {
	prefCacheMu.Lock()
	delete(prefCache, tokenID)
	prefCacheMu.Unlock()
}

// ResetPreferenceCacheForTest 清空全部偏好缓存（仅测试使用）。
func ResetPreferenceCacheForTest() {
	prefCacheMu.Lock()
	prefCache = map[int64]cachedPreferences{}
	prefCacheMu.Unlock()
}

// CheckSourceAllowed 判断当前请求所选渠道/模型是否被该平台 Key 的偏好允许。
//
// 依次判定三件事：
//  1. **允许模型**：Key 配了允许模型列表时，请求的模型必须在列表内；
//  2. **固定来源**：范围模式为「固定来源」时，选中的渠道必须正是被固定的那条供给所属渠道；
//  3. **允许来源**：Key 限定来源时，选中渠道的分享者必须在允许列表内。
//
// 返回 nil 表示放行；否则返回应当抛给底座重试/中止流程的错误。
// 「可回退」时返回**可重试**错误让底座重选渠道；不可回退时直接写出 403 并中止上下文。
func CheckSourceAllowed(c *gin.Context, channelID int, modelName string) *types.NewAPIError {
	if !sourceGuardEnabled || c == nil || channelID <= 0 {
		return nil
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if tokenID <= 0 || userID <= 0 {
		// 没有平台 Key 上下文（例如内部调用）：不施加限制
		return nil
	}

	db := channelpool.CurrentDB()
	key, found := loadPreferences(db, int64(userID), int64(tokenID))
	if !found || key == nil {
		return nil
	}

	// ① 允许模型
	if allowedModels := key.AllowedModels(); len(allowedModels) > 0 && modelName != "" {
		if !slices.Contains(allowedModels, modelName) {
			return denyOrRetry(c, key, "model_not_allowed",
				"this API key is not allowed to use the requested model")
		}
	}

	// ② 固定来源
	if key.ScopeMode == apigxmodel.ScopeModePinned {
		pinnedChannelID, ok := channelpool.ShareChannelIDByOffering(db, key.PinnedOfferingID)
		if !ok {
			common.SysLog("apigx source guard: pinned offering is not available")
			return denyOrRetry(c, key, "pinned_source_unavailable", "the pinned source is not available")
		}
		if selectedID, isOurChannel := channelpool.ShareChannelIDByBase(db, channelID); !isOurChannel || selectedID != pinnedChannelID {
			return denyOrRetry(c, key, "pinned_source_mismatch", "the selected source is not the pinned one")
		}
	}

	// ③ 允许来源
	if key.ScopeMode == apigxmodel.ScopeModeAllowedOwners {
		ownerID, isOurChannel := channelpool.ShareChannelOwnerByBase(db, channelID)
		if isOurChannel {
			if allowed, _ := key.AllowsOwner(ownerID); allowed {
				return nil
			}
		} else {
			// 不是分享渠道：限制来源的 Key 不得借道管理员自建渠道
			common.SysLog("apigx source guard: key restricts sources but selected channel is not a share channel")
		}
		return denyOrRetry(c, key, "source_not_allowed",
			"this API key is not allowed to use the selected source")
	}
	return nil
}

// denyOrRetry 按「是否允许回退」决定拒绝方式。
//
// 可回退：返回可重试错误（**不中止上下文**），让底座重新选择渠道；
// 不可回退：直接写出 403 并中止上下文——因为 `Distribute` 调用
// `SetupContextForSelectedChannel` 时**忽略返回值**，只返回错误阻止不了请求继续
// （容量钩子有效正是因为它直接 abort）。
func denyOrRetry(c *gin.Context, key *apigxmodel.APIKey, code string, message string) *types.NewAPIError {
	if key.AllowFallback {
		return types.NewErrorWithStatusCode(errors.New(message+", retry with another channel"),
			types.ErrorCodeChannelNoAvailableKey, http.StatusForbidden, types.ErrOptionWithNoRecordErrorLog())
	}
	abortJSON(c, http.StatusForbidden, code, message)
	return types.NewErrorWithStatusCode(errors.New(message),
		types.ErrorCodeChannelNoAvailableKey, http.StatusForbidden,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}
