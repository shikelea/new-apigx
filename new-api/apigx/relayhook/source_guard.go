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

// 分享资源 ACL 与平台 Key 来源范围的统一执行侧守卫。
//
// 每次请求都先按 owner、公开、有效 Grant 与资源发布状态得到可用 Offering，再与平台 Key
// 的模型白名单、固定 Offering 或允许分享者范围求交。普通 New API 管理员渠道仍沿用底座规则；
// 只有平台 Key 显式要求分享来源时才禁止借道它们。所有拒绝统一返回“无可用来源”，避免泄露
// 资源是否存在、是否私有、是否暂停以及授权为何失效。
var sourceGuardEnabled = common.GetEnvOrDefaultBool("APIGX_SOURCE_GUARD_ENABLED", true)

const requestSourceAccessContextKey = "apigx_request_source_access"

type requestSourceAccess struct {
	active             bool
	modelName          string
	key                *apigxmodel.APIKey
	managedBaseIDs     map[int]struct{}
	allowedBaseIDs     map[int]struct{}
	allowedOfferingIDs map[int64]struct{}
	requireShareSource bool
	denyAll            bool
}

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

type preferenceCacheKey struct {
	userID  int64
	tokenID int64
}

var (
	prefCacheMu sync.RWMutex
	prefCache   = map[preferenceCacheKey]cachedPreferences{}
)

// loadPreferences 读取某令牌的 APIGX 偏好（带短 TTL 缓存：准入在每个请求上都要判断）。
// 数据库错误必须向上传播且不能写入“未配置”缓存，否则数据库短暂故障恢复后会在 TTL 内
// 把受限平台 Key 当作默认 Key，形成来源范围放宽。
func loadPreferences(db *gorm.DB, userID, tokenID int64) (*apigxmodel.APIKey, bool, error) {
	if db == nil || tokenID <= 0 || userID <= 0 {
		return nil, false, nil
	}
	now := time.Now()
	cacheKey := preferenceCacheKey{userID: userID, tokenID: tokenID}
	prefCacheMu.RLock()
	if cached, ok := prefCache[cacheKey]; ok && now.Before(cached.expiresAt) {
		prefCacheMu.RUnlock()
		return cached.key, cached.found, nil
	}
	prefCacheMu.RUnlock()

	key, err := apigxmodel.GetAPIKeyByToken(db, userID, tokenID)
	if err != nil && !errors.Is(err, apigxmodel.ErrNotFound) {
		return nil, false, err
	}
	found := err == nil

	prefCacheMu.Lock()
	prefCache[cacheKey] = cachedPreferences{key: key, found: found, expiresAt: now.Add(preferenceCacheTTL)}
	prefCacheMu.Unlock()
	return key, found, nil
}

// InvalidatePreferenceCache 丢弃某令牌的偏好缓存（偏好变更后调用，否则最长一个 TTL 才生效）。
func InvalidatePreferenceCache(tokenID int64) {
	prefCacheMu.Lock()
	for key := range prefCache {
		if key.tokenID == tokenID {
			delete(prefCache, key)
		}
	}
	prefCacheMu.Unlock()
}

// ResetPreferenceCacheForTest 清空全部偏好缓存（仅测试使用）。
func ResetPreferenceCacheForTest() {
	prefCacheMu.Lock()
	prefCache = map[preferenceCacheKey]cachedPreferences{}
	prefCacheMu.Unlock()
}

func emptyRequestSourceAccess() requestSourceAccess {
	return requestSourceAccess{
		managedBaseIDs:     map[int]struct{}{},
		allowedBaseIDs:     map[int]struct{}{},
		allowedOfferingIDs: map[int64]struct{}{},
	}
}

// resolveSourceAccessForIdentity 计算指定用户与可选平台 Key 的最终来源范围。
// key=nil 用于历史任务内容等没有原令牌上下文、但仍需按任务所有者实时复验资源 ACL 的路径。
func resolveSourceAccessForIdentity(db *gorm.DB, userID int64, key *apigxmodel.APIKey, modelName string) (requestSourceAccess, error) {
	result := emptyRequestSourceAccess()
	if db == nil || userID <= 0 {
		return result, nil
	}
	platformKeyID := int64(0)
	if key != nil {
		result.key = key
		platformKeyID = key.ID
	}
	snapshot, err := apigxmodel.ResolveSourceAccessSnapshot(db, userID, platformKeyID, modelName, 0)
	if err != nil {
		return result, err
	}
	result.active = true
	result.modelName = modelName
	for _, baseID := range snapshot.ManagedBaseChannelIDs {
		result.managedBaseIDs[baseID] = struct{}{}
	}

	if key != nil {
		if allowedModels := key.AllowedModels(); len(allowedModels) > 0 && !slices.Contains(allowedModels, modelName) {
			result.denyAll = true
		}
		result.requireShareSource = key.ScopeMode == apigxmodel.ScopeModePinned ||
			key.ScopeMode == apigxmodel.ScopeModeAllowedOwners ||
			key.ScopeMode == apigxmodel.ScopeModeAccessKinds
	}

	for _, resource := range snapshot.AllowedResources {
		if result.denyAll {
			continue
		}
		if key != nil && key.ScopeMode == apigxmodel.ScopeModePinned && resource.OfferingID != key.PinnedOfferingID {
			continue
		}
		if key != nil && key.ScopeMode == apigxmodel.ScopeModeAllowedOwners {
			allowed, _ := key.AllowsOwner(resource.OwnerUserID)
			if !allowed {
				continue
			}
		}
		if key != nil && key.ScopeMode == apigxmodel.ScopeModeAccessKinds {
			kind := apigxmodel.PlazaAccessKindShared
			if resource.GroupVisibility == apigxmodel.ShareGroupVisibilityPublic {
				kind = apigxmodel.PlazaAccessKindPublic
			} else if resource.OwnerUserID == userID {
				kind = apigxmodel.PlazaAccessKindPrivate
			}
			if !key.AllowsAccessKind(kind) {
				continue
			}
		}
		result.allowedOfferingIDs[resource.OfferingID] = struct{}{}
		if resource.BaseChannelID > 0 {
			result.allowedBaseIDs[int(resource.BaseChannelID)] = struct{}{}
		}
	}
	return result, nil
}

// resolveRequestSourceAccess 先计算资源 ACL，再与平台 Key 的模型/固定供给/允许分享者范围求交。
// 该结果用于候选过滤；选中渠道后 CheckSourceAllowed 会重新读取一次，作为出站前最终守卫。
func resolveRequestSourceAccess(c *gin.Context, modelName string) (requestSourceAccess, error) {
	result := emptyRequestSourceAccess()
	if !sourceGuardEnabled || c == nil {
		return result, nil
	}
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if userID <= 0 || tokenID <= 0 {
		// 内部调用没有调用主体，保持 New API 原行为；对外路由均应由鉴权中间件填入二者。
		return result, nil
	}
	db := channelpool.CurrentDB()
	if db == nil {
		// 普通 New API 模式可以不初始化 APIGX；共享模式启动自检会强制要求数据库与本守卫。
		// 因此 nil 表示功能未启用，而不是一次运行中的数据库故障。
		return result, nil
	}
	key, found, err := loadPreferences(db, int64(userID), int64(tokenID))
	if err != nil {
		return handleSourceAccessDBError(err)
	}
	if !found {
		key = nil
	}
	access, err := resolveSourceAccessForIdentity(db, int64(userID), key, modelName)
	if err != nil {
		return handleSourceAccessDBError(err)
	}
	return access, nil
}

// handleSourceAccessDBError keeps an ordinary New API deployment compatible when no usable
// APIGX schema is attached (including lightweight test fixtures). Formal shared-resource mode
// is fail-closed: startup requires the feature set and every runtime database error is returned.
func handleSourceAccessDBError(err error) (requestSourceAccess, error) {
	if common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false) {
		return emptyRequestSourceAccess(), err
	}
	return emptyRequestSourceAccess(), nil
}

// SourceAccessChannelFilter 返回应在亲和、随机选择、重试和 pin 校验前加入的请求级过滤器。
// allowList=true 时 ids 是唯一允许集合（平台 Key 显式限定分享来源）；否则 ids 是应排除的 APIGX 渠道。
func SourceAccessChannelFilter(c *gin.Context, modelName string) (ids []int, allowList bool, apply bool, err error) {
	access, err := resolveRequestSourceAccess(c, modelName)
	if err != nil || !access.active {
		return nil, false, false, err
	}
	c.Set(requestSourceAccessContextKey, access)
	if access.denyAll || access.requireShareSource {
		ids = make([]int, 0, len(access.allowedBaseIDs))
		for id := range access.allowedBaseIDs {
			ids = append(ids, id)
		}
		return ids, true, true, nil
	}
	for id := range access.managedBaseIDs {
		if _, allowed := access.allowedBaseIDs[id]; !allowed {
			ids = append(ids, id)
		}
	}
	return ids, false, len(ids) > 0, nil
}

// AllowedOfferingIDsForRequest 返回已经通过资源 ACL 与平台 Key 范围求交的供给集合。
// 路由策略必须在读取质量、价格与容量之前使用它，避免让无权资源进入评分阶段。
func AllowedOfferingIDsForRequest(c *gin.Context, modelName string) (map[int64]struct{}, bool, error) {
	if c != nil {
		if raw, ok := c.Get(requestSourceAccessContextKey); ok {
			if access, valid := raw.(requestSourceAccess); valid && access.active && access.modelName == modelName {
				return access.allowedOfferingIDs, true, nil
			}
		}
	}
	access, err := resolveRequestSourceAccess(c, modelName)
	if err != nil || !access.active {
		return nil, false, err
	}
	if c != nil {
		c.Set(requestSourceAccessContextKey, access)
	}
	return access.allowedOfferingIDs, true, nil
}

// CheckSourceAllowed 是选中渠道后的最终守卫。它与候选过滤共用同一决策，防止固定 channel ID、
// origin-task pin、WebSocket、失败重试或未来入口绕开候选阶段。
func CheckSourceAllowed(c *gin.Context, channelID int, modelName string) *types.NewAPIError {
	if !sourceGuardEnabled || c == nil || channelID <= 0 {
		return nil
	}
	access, err := resolveRequestSourceAccess(c, modelName)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeChannelNoAvailableKey,
			http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	if !access.active {
		return nil
	}
	return checkResolvedSourceAllowed(access, channelID)
}

// CheckSourceAllowedForUser 为没有原平台 Key 上下文的历史任务出站路径复验资源 ACL。
// 它只使用任务所属用户，不臆造原令牌范围；普通 New API 渠道继续放行。
func CheckSourceAllowedForUser(userID int64, channelID int, modelName string) *types.NewAPIError {
	if !sourceGuardEnabled || userID <= 0 || channelID <= 0 {
		return nil
	}
	db := channelpool.CurrentDB()
	if db == nil {
		return nil
	}
	access, err := resolveSourceAccessForIdentity(db, userID, nil, modelName)
	if err != nil {
		if !common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false) {
			return nil
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeChannelNoAvailableKey,
			http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return checkResolvedSourceAllowed(access, channelID)
}

func checkResolvedSourceAllowed(access requestSourceAccess, channelID int) *types.NewAPIError {
	if !access.active {
		return nil
	}
	_, managed := access.managedBaseIDs[channelID]
	_, allowed := access.allowedBaseIDs[channelID]
	if access.denyAll || (managed && !allowed) || (!managed && access.requireShareSource) {
		return denyOrRetry(access.key)
	}
	return nil
}

// denyOrRetry 按「是否允许回退」决定拒绝方式。
//
// 所有 ACL 拒绝都使用同一个对外结果，不区分资源不存在、未发布、私有、授权过期或 Key 范围不符。
func denyOrRetry(key *apigxmodel.APIKey) *types.NewAPIError {
	options := []types.NewAPIErrorOptions{types.ErrOptionWithNoRecordErrorLog()}
	if key != nil && !key.AllowFallback {
		options = append(options, types.ErrOptionWithSkipRetry())
	}
	return types.NewErrorWithStatusCode(errors.New("no available source for the requested model"),
		types.ErrorCodeChannelNoAvailableKey, http.StatusServiceUnavailable, options...)
}
