package apigxmodel

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 领域仓储测试（SQLite，进程内，快）。
//
// 覆盖三条最容易被写错的规则：
//  1. **归属由服务端强制**——不能只靠调用方自律；查不到与不属于自己对外等价。
//  2. **软删除墓碑语义**——删除后业务键可被复用，且重复删除不改变墓碑值。
//  3. **Key 池筛选**——启停/冷却/到期是三件不同的事，任一不满足都要跳过该 Key。
func newRepoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "apigx_repo_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, Migrate(db))
	return db
}

// baseChannelSeq 为每次 seed 分配唯一的底座渠道 ID：
// base_channel_id 上有唯一索引，测试里若都用 0 会互相冲突。
var baseChannelSeq int64

func nextBaseChannelID() int64 { return atomic.AddInt64(&baseChannelSeq, 1) }

func seedGroupAndChannel(t *testing.T, db *gorm.DB, owner int64) (*ShareGroup, *Channel) {
	t.Helper()
	group := &ShareGroup{OwnerUserID: owner, Name: "小林公益站", DefaultVisibility: 1, PublishStatus: 1}
	require.NoError(t, CreateShareGroup(db, group))
	channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "主线路", Protocol: "openai",
		BaseURL: "http://stub", BaseChannelID: nextBaseChannelID(), HealthState: 0}
	require.NoError(t, CreateChannel(db, channel))
	return group, channel
}

func TestShareGroupOwnershipIsEnforcedServerSide(t *testing.T) {
	db := newRepoDB(t)
	const ownerA, ownerB int64 = 1001, 1002
	group, _ := seedGroupAndChannel(t, db, ownerA)

	// 所有者自己可以读到
	got, err := GetShareGroup(db, ownerA, group.ID)
	require.NoError(t, err)
	assert.Equal(t, "小林公益站", got.Name)

	// 别人读不到，且对外与「不存在」等价
	_, err = GetShareGroup(db, ownerB, group.ID)
	assert.ErrorIs(t, err, ErrNotFound, "不属于自己的对象必须与不存在等价（避免枚举）")

	// 别人的列表也不含它
	groups, err := ListShareGroups(db, ownerB, 50)
	require.NoError(t, err)
	assert.Empty(t, groups)

	// 别人不能删
	assert.ErrorIs(t, DeleteShareGroup(db, ownerB, group.ID), ErrNotFound)
	// 别人不能改健康状态
	assert.ErrorIs(t, UpdateChannelHealth(db, ownerB, 1, 1, "hacked"), ErrNotFound)
}

func TestSoftDeleteTombstoneAllowsKeyReuse(t *testing.T) {
	db := newRepoDB(t)
	const owner int64 = 2001
	group, channel := seedGroupAndChannel(t, db, owner)

	// 删除分组后：读不到、列表不含、重复删除仍返回 ErrNotFound（墓碑值不被改写）
	require.NoError(t, DeleteShareGroup(db, owner, group.ID))
	_, err := GetShareGroup(db, owner, group.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	groups, err := ListShareGroups(db, owner, 50)
	require.NoError(t, err)
	assert.Empty(t, groups)
	assert.ErrorIs(t, DeleteShareGroup(db, owner, group.ID), ErrNotFound, "重复删除不得成功")

	// 关键：删除后可以**新建同名分组**——这正是 alive_flag 墓碑设计要换来的能力
	recreated := &ShareGroup{OwnerUserID: owner, Name: "小林公益站", DefaultVisibility: 1, PublishStatus: 1}
	require.NoError(t, CreateShareGroup(db, recreated), "墓碑设计应允许同名重建")
	assert.NotEqual(t, group.ID, recreated.ID)

	// UpstreamKey 的唯一索引包含 alive_flag，因此同一个 StableKeyID 可被重新添加
	key1 := &UpstreamKey{ChannelID: channel.ID, StableKeyID: "stable-1", Name: "k1", SecretRef: "secret://k1", SecretFingerprint: "fp-k1"}
	require.NoError(t, CreateUpstreamKey(db, owner, key1))
	require.NoError(t, DeleteUpstreamKey(db, owner, key1.ID))

	reuse := &UpstreamKey{ChannelID: channel.ID, StableKeyID: "stable-1", Name: "k1-reimported", SecretRef: "secret://k1b", SecretFingerprint: "fp-k1b"}
	require.NoError(t, CreateUpstreamKey(db, owner, reuse), "软删除后同一 StableKeyID 应可重新添加")

	// 但存活期间重复的 StableKeyID 必须被拒绝
	dup := &UpstreamKey{ChannelID: channel.ID, StableKeyID: "stable-1", Name: "dup", SecretRef: "secret://dup", SecretFingerprint: "fp-dup"}
	require.Error(t, CreateUpstreamKey(db, owner, dup), "存活期间同一 StableKeyID 不得重复")
}

func TestUpstreamKeyOwnershipTravelsThroughChannel(t *testing.T) {
	db := newRepoDB(t)
	const ownerA, ownerB int64 = 3001, 3002
	_, channelA := seedGroupAndChannel(t, db, ownerA)
	_, channelB := seedGroupAndChannel(t, db, ownerB)

	keyA := &UpstreamKey{ChannelID: channelA.ID, StableKeyID: "a-1", Name: "A 的 Key", SecretRef: "secret://a1", SecretFingerprint: "fp-a1"}
	require.NoError(t, CreateUpstreamKey(db, ownerA, keyA))

	// B 拿 A 的 channel id 去操作：必须全部拒绝（Key 没有 owner 列，归属经渠道传递）
	_, err := ListUpstreamKeysByChannel(db, ownerB, channelA.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = ListUpstreamKeysByChannel(db, ownerA, channelB.ID)
	assert.ErrorIs(t, err, ErrNotFound, "也不能借自己的身份去查别人的渠道")

	// 用别人的渠道创建 Key 也不行
	intruder := &UpstreamKey{ChannelID: channelA.ID, StableKeyID: "x-1", Name: "偷渡", SecretRef: "secret://x", SecretFingerprint: "fp-x"}
	assert.ErrorIs(t, CreateUpstreamKey(db, ownerB, intruder), ErrNotFound)

	// 改状态同样被拒
	assert.ErrorIs(t, UpdateUpstreamKeyStatus(db, ownerB, keyA.ID, UpstreamKeyStatusDisabled), ErrNotFound)
	assert.ErrorIs(t, SetUpstreamKeyCooldown(db, ownerB, keyA.ID, common.GetTimestamp()+60), ErrNotFound)
}

// Key 池筛选：启用 / 未到期 / 不在冷却，三者必须同时满足。
func TestAvailableUpstreamKeysFiltersThreeConditions(t *testing.T) {
	db := newRepoDB(t)
	const owner int64 = 4001
	_, channel := seedGroupAndChannel(t, db, owner)
	now := common.GetTimestamp()

	mk := func(stableID string, status int, cooldownUntil, expiresAt int64) *UpstreamKey {
		k := &UpstreamKey{ChannelID: channel.ID, StableKeyID: stableID, Name: stableID, SecretRef: "secret://" + stableID,
			SecretFingerprint: "fp-" + stableID,
			Status:            status, CooldownUntil: cooldownUntil, ExpiresAt: expiresAt}
		require.NoError(t, CreateUpstreamKey(db, owner, k))
		return k
	}
	mk("ok", UpstreamKeyStatusEnabled, 0, 0)
	mk("disabled", UpstreamKeyStatusDisabled, 0, 0)
	mk("invalid", UpstreamKeyStatusInvalid, 0, 0)
	mk("cooling", UpstreamKeyStatusEnabled, now+300, 0)
	mk("expired", UpstreamKeyStatusEnabled, 0, now-1)
	mk("expires-later", UpstreamKeyStatusEnabled, 0, now+3600)

	available, err := AvailableUpstreamKeys(db, owner, channel.ID, now)
	require.NoError(t, err)

	names := make([]string, 0, len(available))
	for _, k := range available {
		names = append(names, k.Name)
	}
	assert.ElementsMatch(t, []string{"ok", "expires-later"}, names,
		"只有同时满足 启用+未到期+不在冷却 的 Key 才可用")

	// 排除原因要可枚举，便于分享者排查（而不是笼统地说「不可用」）
	all, err := ListUpstreamKeysByChannel(db, owner, channel.ID)
	require.NoError(t, err)
	assert.Len(t, all, 6, "筛选只影响 Available，不应删除任何行")
}

// 一个底座渠道只能被一个分享渠道引用（BaseChannelID 唯一）。
// 渠道必须挂在**自己的**分组下：ShareGroupID 来自请求体，不校验就等于让任何人
// 只要知道别人的分组 ID 就能挂进去。这是接口层测试发现越权后补的仓储层守门。
func TestCreateChannelRequiresOwnedShareGroup(t *testing.T) {
	db := newRepoDB(t)
	const ownerA, ownerB int64 = 8001, 8002
	groupA, _ := seedGroupAndChannel(t, db, ownerA)

	intruder := &Channel{OwnerUserID: ownerB, ShareGroupID: groupA.ID, Name: "偷渡渠道", BaseChannelID: nextBaseChannelID()}
	assert.ErrorIs(t, CreateChannel(db, intruder), ErrNotFound, "不得把自己的渠道挂到别人的分组下")

	// 不存在的分组同样被拒（与「不属于自己」不可区分）
	orphan := &Channel{OwnerUserID: ownerB, ShareGroupID: 999999, Name: "孤儿渠道", BaseChannelID: nextBaseChannelID()}
	assert.ErrorIs(t, CreateChannel(db, orphan), ErrNotFound)
}
func TestChannelBaseIDIsUnique(t *testing.T) {
	db := newRepoDB(t)
	const owner int64 = 5001
	group, channel := seedGroupAndChannel(t, db, owner)
	_ = channel

	dup := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "重复引用", BaseChannelID: channel.BaseChannelID}
	require.Error(t, CreateChannel(db, dup), "同一底座渠道不得被两个分享渠道引用")

	other := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "另一个", BaseChannelID: nextBaseChannelID()}
	require.NoError(t, CreateChannel(db, other))

	// 未绑定底座渠道必须被拒：该列有唯一索引，允许多条 0 会互相冲突
	unbound := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "未绑定"}
	require.ErrorIs(t, CreateChannel(db, unbound), ErrInvalidArgument)
}

func TestRepositoriesRejectInvalidArguments(t *testing.T) {
	db := newRepoDB(t)

	assert.ErrorIs(t, CreateShareGroup(db, &ShareGroup{Name: "无主"}), ErrInvalidArgument)
	assert.ErrorIs(t, CreateShareGroup(db, &ShareGroup{OwnerUserID: 1}), ErrInvalidArgument)
	assert.ErrorIs(t, CreateShareGroup(db, nil), ErrInvalidArgument)

	group, channel := seedGroupAndChannel(t, db, 6001)
	assert.ErrorIs(t, CreateChannel(db, &Channel{OwnerUserID: 6001, ShareGroupID: group.ID}), ErrInvalidArgument, "渠道必须有名字")
	assert.ErrorIs(t, CreateChannel(db, &Channel{OwnerUserID: 6001, Name: "无分组"}), ErrInvalidArgument)

	// Key 缺 StableKeyID / SecretRef 都要拒绝：前者是轮询与统计的锚点，后者保证不存明文
	assert.ErrorIs(t, CreateUpstreamKey(db, 6001, &UpstreamKey{ChannelID: channel.ID, SecretRef: "s"}), ErrInvalidArgument)
	assert.ErrorIs(t, CreateUpstreamKey(db, 6001, &UpstreamKey{ChannelID: channel.ID, StableKeyID: "s1"}), ErrInvalidArgument)

	// 账户池：标签与去重哈希必填
	assert.ErrorIs(t, CreateUpstreamAccount(db, &UpstreamAccount{OwnerUserID: 6001}), ErrInvalidArgument)
	assert.ErrorIs(t, CreateUpstreamAccount(db, &UpstreamAccount{OwnerUserID: 6001, Provider: "openai", AccountLabel: "a"}), ErrInvalidArgument)
}

func TestIsUpstreamKeyUsableBoundaries(t *testing.T) {
	now := int64(1_000_000)
	base := func() *UpstreamKey {
		return &UpstreamKey{Status: UpstreamKeyStatusEnabled}
	}

	assert.True(t, IsUpstreamKeyUsable(base(), now))
	assert.False(t, IsUpstreamKeyUsable(nil, now))

	expired := base()
	expired.ExpiresAt = now // 恰好等于当前时刻 → 视为已到期
	assert.False(t, IsUpstreamKeyUsable(expired, now))

	cooling := base()
	cooling.CooldownUntil = now + 1
	assert.False(t, IsUpstreamKeyUsable(cooling, now))

	cooldownOver := base()
	cooldownOver.CooldownUntil = now - 1
	assert.True(t, IsUpstreamKeyUsable(cooldownOver, now))

	disabled := base()
	disabled.Status = UpstreamKeyStatusDisabled
	assert.False(t, IsUpstreamKeyUsable(disabled, now))

	invalid := base()
	invalid.Status = UpstreamKeyStatusInvalid
	assert.False(t, IsUpstreamKeyUsable(invalid, now), "鉴权失效的 Key 必须跳过")
}

func TestGetUpstreamAccountScopesByOwner(t *testing.T) {
	db := newRepoDB(t)
	account := &UpstreamAccount{OwnerUserID: 7001, Provider: "openai", AccountLabel: "主账户", AccountKeyHash: UpstreamAccountUniqueHash(7001, "openai", "主账户")}
	require.NoError(t, CreateUpstreamAccount(db, account))

	got, err := GetUpstreamAccount(db, 7001, account.ID)
	require.NoError(t, err)
	assert.Equal(t, "主账户", got.AccountLabel)
	assert.Equal(t, CircuitStateClosed, got.CircuitState)

	_, err = GetUpstreamAccount(db, 7002, account.ID)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// ---- 批量导入解析（选择策略交给底座 model.Channel.GetNextEnabledKey） ----

func TestParseImportedKeys(t *testing.T) {
	raw := "# 注释行\n" +
		"sk-aaaaaaaaaaaa\n" +
		"\n" +
		"  sk-bbbbbbbbbbbb  \n" +
		"sk-aaaaaaaaaaaa\n" + // 批内重复
		"short\n" + // 非法（太短）
		"sk-cccccccccccc\r\n"

	result, err := ParseImportedKeys(raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"sk-aaaaaaaaaaaa", "sk-bbbbbbbbbbbb", "sk-cccccccccccc"}, result.Accepted,
		"应忽略注释与空行、去掉首尾空白、批内去重（保留首次出现）")
	assert.Equal(t, []int{5}, result.DuplicateLines)
	assert.Equal(t, []int{6}, result.InvalidLines)
}

func TestParseImportedKeysRejectsOversizedBatch(t *testing.T) {
	var b strings.Builder
	for i := range MaxImportBatch + 1 {
		fmt.Fprintf(&b, "sk-key-%06d\n", i)
	}
	_, err := ParseImportedKeys(b.String())
	assert.ErrorIs(t, err, ErrImportRejected, "超批量上限必须整体拒绝")
}

// ---- 容量策略读取（四态透传） ----

// 读取层**不解释**四态：原样把「模式 + 值」交给 capacity.Resolve，避免两处各有一套规则。
func TestCapacityPolicyReadsRawFourState(t *testing.T) {
	db := newRepoDB(t)
	const owner int64 = 9901

	policy := &CapacityPolicy{
		OwnerUserID: owner, ScopeType: CapacityScopeChannel, ScopeID: 555,
		PolicyVersion: 1,
		// 并发：明确值 7；RPM：明确不限；TPM：未配置
		MaxConcurrencyMode: 4, MaxConcurrency: 7,
		RpmMode: 3, RpmLimit: 0,
		TpmMode: 0, TpmLimit: 0,
	}
	require.NoError(t, db.Create(policy).Error)

	got, err := GetCapacityPolicy(db, policy.ID)
	require.NoError(t, err)
	mode, value := got.Concurrency()
	assert.Equal(t, 4, mode, "明确值必须原样读出")
	assert.EqualValues(t, 7, value)

	rpmMode, rpmValue := got.RPM()
	assert.Equal(t, 3, rpmMode, "明确不限必须原样读出，不能被折叠成 0")
	assert.Zero(t, rpmValue)

	tpmMode, _ := got.TPM()
	assert.Zero(t, tpmMode, "未配置就是未配置，不得被解释成不限")

	// 按范围查
	byScope, err := FindCapacityPolicyByScope(db, owner, CapacityScopeChannel, 555)
	require.NoError(t, err)
	assert.Equal(t, policy.ID, byScope.ID)

	// 不同所有者查不到（归属隔离）
	_, err = FindCapacityPolicyByScope(db, 9902, CapacityScopeChannel, 555)
	assert.ErrorIs(t, err, ErrNotFound)

	// 不存在的策略
	_, err = GetCapacityPolicy(db, 999999)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = GetCapacityPolicy(db, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

// ---- 平台 Key 的 APIGX 偏好（T1.10） ----

func TestAPIKeyPreferencesUpsertAndValidation(t *testing.T) {
	db := newRepoDB(t)
	const user, token int64 = 12001, 900

	// 首次：默认模式
	key, err := UpsertAPIKeyPreferences(db, user, token, nil)
	require.NoError(t, err)
	assert.Equal(t, ScopeModeDefault, key.ScopeMode)
	assert.Empty(t, key.AllowedOwnerIDs(), "默认模式不限制来源")
	allowed, fallback := key.AllowsOwner(12345)
	assert.True(t, allowed, "默认模式允许任何来源")
	assert.False(t, fallback)

	// 允许来源模式：列表为空必须被拒（避免「配了模式却没配列表」的空转配置）
	_, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.ScopeMode = ScopeModeAllowedOwners
		return nil
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidArgument)

	// 写入允许来源（去重）+ 允许回退
	key, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.ScopeMode = ScopeModeAllowedOwners
		k.AllowFallback = true
		return k.SetAllowedOwnerIDs([]int64{77, 55, 77})
	})
	require.NoError(t, err)
	assert.Equal(t, []int64{55, 77}, key.AllowedOwnerIDs(), "允许来源应去重排序")

	// 回退语义：列表内允许；列表外不允许但**可回退**
	if allowed, _ := key.AllowsOwner(77); !allowed {
		assert.Fail(t, "列表内的来源应被允许")
	}
	allowed, fallback = key.AllowsOwner(99)
	assert.False(t, allowed)
	assert.True(t, fallback, "AllowFallback=true 时调用方应可让底座重选其它来源")

	// 关闭回退后，列表外来源必须直接拒绝
	key, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.AllowFallback = false
		return nil
	})
	require.NoError(t, err)
	allowed, fallback = key.AllowsOwner(99)
	assert.False(t, allowed)
	assert.False(t, fallback, "AllowFallback=false 时不得回退")

	// 固定来源模式：必须给出 pinned_offering_id
	_, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.ScopeMode = ScopeModePinned
		return nil
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
	key, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.ScopeMode = ScopeModePinned
		k.PinnedOfferingID = 4242
		return nil
	})
	require.NoError(t, err)
	assert.EqualValues(t, 4242, key.PinnedOfferingID)

	// 未知模式必须被拒
	_, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.ScopeMode = 99
		return nil
	})
	assert.ErrorIs(t, err, ErrInvalidArgument)

	// 允许模型列表：去重排序 + 持久化后可读回
	key, err = UpsertAPIKeyPreferences(db, user, token, func(k *APIKey) error {
		k.ScopeMode = ScopeModeDefault
		k.AllowedModelIDs = `["b","a","a"]`
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, key.AllowedModels())

	// 归属隔离：另一个用户看不到该偏好
	_, err = GetAPIKeyByToken(db, 12002, token)
	assert.ErrorIs(t, err, ErrNotFound)
}
