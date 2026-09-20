// 本文件对应 `docs/stage0/schema-draft.md` §5.1–§5.13，字段与约束依据该文档，
// 不得自行增删字段。
//
// 包名与硬约束（三库可移植、类型别名 → GORM tag 对照表、四态枚举、墓碑软删除模板）
// 见同包的 `base.go` 包注释；本文件不再重复，字段 tag 一律按该对照表书写。
//
// 本文件只描述表结构，不含 SQL、不建库级外键约束（§6.1）、不写 CHECK（§3.3），
// 所有跨表一致性校验落在 Go 服务端。
package apigxmodel

import (
	"gorm.io/gorm"
)

// UserProfile 承载平台独有的用户属性，与底座 `users`（`new-api/model/user.go:79-115`）一对一。
// 不新建用户表，不复制身份、密码、邮箱、会话（CODE_STYLE.md 第 9 条「不另起第二套用户系统」）。
//
// schema-draft §5.1
type UserProfile struct {
	// UserID 与底座 users.id 同值；主键即底座用户 ID，因此不自增。
	UserID int64 `gorm:"primaryKey;autoIncrement:false"`
	// TrustLevel 信任级别：0=待审核 / 1=普通 / 2=可信分享者 / 3=管理员指定。
	TrustLevel int `gorm:"type:int;not null"`
	// SharingEnabled 是否允许发布分享资源（审核策略开关）。
	SharingEnabled bool `gorm:"not null"`
	// DefaultRouteMode Key 未指定时的默认路由偏好：pinned/latency/throughput/reliability/auto。
	DefaultRouteMode string `gorm:"size:16;not null;default:''"`
	// DefaultAllowFallback 默认是否允许「指定来源失败后自动回退」（§9.1）。
	DefaultAllowFallback bool `gorm:"not null"`
	// BudgetLimitMode 用户级每日 USD 估算预算的模式（四态，§3.3）。
	BudgetLimitMode int `gorm:"type:int;not null"`
	// BudgetLimitMicro 用户级每日预算额度，micro-USD；仅 mode=4 时有效。
	BudgetLimitMicro int64 `gorm:"bigint;not null;default:0"`
	// FreezeState 冻结状态：0=正常 / 1=管理员封禁 / 2=风控冻结；与底座 users.status 并存，不替代它。
	FreezeState int `gorm:"type:int;not null"`
	// FrozenAt 冻结时间，UTC epoch 秒；0=未冻结。
	FrozenAt int64 `gorm:"bigint;not null;default:0"`
	// FrozenReason 冻结原因（脱敏，不含凭据）。
	FrozenReason string `gorm:"size:255;not null;default:''"`
	// CreatedAt 创建时间，UTC epoch 秒。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// AliveFlag 软删除墓碑列：0=存活，软删除时置为本行 ID（§3.5）。
	AliveFlag int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间；用户删除后本行保留 user_id 供历史引用，不物理删。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_userprofile_del"`
}

// TableName 返回表名。
func (UserProfile) TableName() string { return TablePrefix + "user_profiles" }

// APIKey 是平台 API Key 的权威记录：持有者、可访问范围、默认策略、额度与到期时间。
//
// schema-draft §5.2
type APIKey struct {
	// ID 稳定内部 ID（Grant、请求归属、轮询索引都引用它）。
	ID int64 `gorm:"primaryKey"`
	// UserID 持有者，底座 users.id。
	UserID int64 `gorm:"bigint;not null;default:0"`
	// TokenID 底座 tokens.id（影子令牌）；唯一，永不复用。
	TokenID int64 `gorm:"bigint;not null;default:0"`
	// Name 展示名（可重名，不作授权依据，CODE_STYLE.md 第 1 条）。
	Name string `gorm:"size:64;not null;default:''"`
	// KeyPrefix 明文前 8 位，仅供界面识别；不含完整凭据。
	KeyPrefix string `gorm:"size:12;not null;default:''"`
	// KeyHash SHA-256(secret) 十六进制，不可恢复；天然不含 '-'（§2.4）。
	KeyHash string `gorm:"type:char(64);uniqueIndex:idx_apigx_apikey_hash"`
	// ScopeMode 可访问范围模式：0=全部已授权供给 / 1=白名单 / 2=固定单一来源（硬限制）。
	ScopeMode int `gorm:"type:int;not null"`
	// PinnedOfferingID scope_mode=2 时必填（非 0），指向 apigx_model_offerings.id。
	PinnedOfferingID int64 `gorm:"bigint;not null;default:0"`
	// AllowedModelIDs JSON 数组：允许的 apigx_canonical_models.id（scope_mode=1 用）；TEXT 列 + common.Marshal。
	AllowedModelIDs string `gorm:"type:text"`
	// AllowedOwnerUserIDs JSON 数组：允许的分享者（可信分享者限定，§10.3）。
	AllowedOwnerUserIDs string `gorm:"type:text"`
	// AllowedAccessKinds JSON 数组：public/private/shared。用于使用者可理解的资源范围。
	AllowedAccessKinds string `gorm:"type:text"`
	// AllowFallback 是否允许回退；硬限制，请求参数不能放宽（§9.1）。
	AllowFallback bool `gorm:"not null"`
	// DefaultRouteMode 默认路由偏好（偏好，不是硬限制）。
	DefaultRouteMode string `gorm:"size:16;not null;default:''"`
	// RateLimitMode Key 级 RPM 限制模式（四态，§3.3）。
	RateLimitMode int `gorm:"type:int;not null"`
	// RateLimitRPM Key 级每分钟请求数上限；仅 mode=4 时有效。
	RateLimitRPM int64 `gorm:"bigint;not null;default:0"`
	// BudgetLimitMode Key 级每日 USD 预算模式（四态，§3.3）。
	BudgetLimitMode int `gorm:"type:int;not null"`
	// BudgetLimitMicro Key 级每日预算额度，micro-USD；仅 mode=4 时有效。
	BudgetLimitMicro int64 `gorm:"bigint;not null;default:0"`
	// Status 状态：0=待激活 / 1=启用 / 2=禁用 / 3=已撤销 / 4=已过期。
	Status int `gorm:"type:int;not null"`
	// ExpiresAt 到期时间，UTC epoch 秒；0=不过期。
	ExpiresAt int64 `gorm:"bigint;not null;default:0"`
	// LastUsedAt 最近使用时间，UTC epoch 秒；0=未使用。
	LastUsedAt int64 `gorm:"bigint;not null;default:0"`
	// RevokedAt 撤销时间，UTC epoch 秒；0=未撤销。
	RevokedAt int64 `gorm:"bigint;not null;default:0"`
	// RevokedByUserID 执行撤销的用户，指向底座 users.id。
	RevokedByUserID int64 `gorm:"bigint;not null;default:0"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间；撤销（revoked_at）与软删除是两件事，撤销后行保留用于历史归属。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_apikey_del"`
}

// TableName 返回表名。
func (APIKey) TableName() string { return TablePrefix + "api_keys" }

// ShareGroup 是「分享者 + 分组名称 + 简介 + 默认策略 + 发布状态」。
// 分组允许重名（CODE_STYLE.md 第 1 条），因此名称不是唯一键。
//
// schema-draft §5.3
type ShareGroup struct {
	// ID 稳定内部 ID（授权与历史归属一律用它）。
	ID int64 `gorm:"primaryKey"`
	// PublicID 对外稳定标识（展示、URL、日志用），永不复用。
	PublicID string `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_sharegroup_pub"`
	// OwnerUserID 分享者，底座 users.id；第一版不可变更（§7）。
	OwnerUserID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_sharegroup_owner_status,priority:1"`
	// Name 展示名，可重名；更名不影响任何授权。
	Name string `gorm:"size:64;not null;default:''"`
	// Description 简介（不可信内容，渲染前转义）。
	Description string `gorm:"type:text"`
	// DefaultVisibility 默认可见性：0=私有 / 1=受限 / 2=公开。
	DefaultVisibility int `gorm:"type:int;not null;index:idx_apigx_sharegroup_pubvis,priority:2"`
	// DefaultPolicyID 该分组的默认策略行，指向 apigx_access_policies.id。
	DefaultPolicyID int64 `gorm:"bigint;not null;default:0"`
	// PublishStatus 发布状态：0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停 / 4=已退役。
	PublishStatus int `gorm:"type:int;not null;index:idx_apigx_sharegroup_owner_status,priority:2;index:idx_apigx_sharegroup_pubvis,priority:1"`
	// AliveFlag 软删除墓碑列：0=存活，软删除时置为本行 ID（§3.5）。
	AliveFlag int64 `gorm:"bigint;not null;default:0"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒；名称与简介的编辑只更新这里。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间；退役用软删除，保留被引用 ID（§7）。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_sharegroup_del"`
}

// TableName 返回表名。
func (ShareGroup) TableName() string { return TablePrefix + "share_groups" }

// Channel 是分享渠道：owner、分组、协议、Base URL、Key 池策略、并发/RPM/TPM/任务上限、
// 共享限额池、健康状态（计划书 §7 第 3 行）。
//
// schema-draft §5.4
type Channel struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// PublicID 对外稳定标识。
	PublicID string `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_channel_pub"`
	// OwnerUserID 分享者，底座 users.id；不可变（同 §5.3）。
	OwnerUserID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_channel_owner_health,priority:1"`
	// ShareGroupID 所属分享分组，apigx_share_groups.id。
	ShareGroupID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_channel_group_health,priority:1"`
	// Name 展示名。
	Name string `gorm:"size:64;not null;default:''"`
	// BaseChannelID 底座 channels.id；唯一的影子渠道行。
	BaseChannelID int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_channel_base"`
	// Protocol 客户端协议族（openai / anthropic / gemini / …，取值对齐底座 constant 包）。
	Protocol string `gorm:"size:32;not null;default:''"`
	// BaseURL 上游 Base URL；不可信输入，出站前做 SSRF 校验（§10.3）。
	BaseURL string `gorm:"size:512;not null;default:''"`
	// AuthTarget 认证目标（域名/路径），与 base_url 一同参与「资源修订」判定。
	AuthTarget string `gorm:"size:256;not null;default:''"`
	// KeyPoolMode 渠道内选 Key 策略：0=加权轮询 / 1=加权随机；使用者不可见（§9.2）。
	KeyPoolMode int `gorm:"type:int;not null"`
	// CapacityPolicyID 渠道级容量策略，apigx_capacity_policies.id（scope_type=2）。
	CapacityPolicyID int64 `gorm:"bigint;not null;default:0"`
	// DefaultUpstreamAccountID 该渠道默认归属的上游账户池，apigx_upstream_accounts.id。
	DefaultUpstreamAccountID int64 `gorm:"bigint;not null;default:0"`
	// HealthState 健康状态：0=未知 / 1=健康 / 2=降级 / 3=不健康 / 4=冷却中。
	HealthState int `gorm:"type:int;not null;index:idx_apigx_channel_group_health,priority:2;index:idx_apigx_channel_owner_health,priority:2;index:idx_apigx_channel_health_rev,priority:1"`
	// HealthReason 脱敏原因码（不含地址细节与凭据，§9.1）。
	HealthReason string `gorm:"size:128;not null;default:''"`
	// HealthUpdatedAt 健康状态更新时间，UTC epoch 秒。
	HealthUpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// Revision 资源修订号，初始 1；地址或模型归属变更时递增。
	Revision int64 `gorm:"bigint;not null;default:0;index:idx_apigx_channel_health_rev,priority:2"`
	// VerifiedAt 最近一次连通性验证时间，UTC epoch 秒。
	VerifiedAt int64 `gorm:"bigint;not null;default:0"`
	// AliveFlag 软删除墓碑列：0=存活，软删除时置为本行 ID（§3.5）。
	AliveFlag int64 `gorm:"bigint;not null;default:0"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间；退役用软删除，保留被请求/任务/价格/计量记录引用的稳定 ID。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_channel_del"`
}

// TableName 返回表名。
func (Channel) TableName() string { return TablePrefix + "channels" }

// UpstreamAccount 是上游账户池（派生表，服务 §9.2 共享限额）：
// 同一上游账户下的多条渠道可共享额度与熔断范围，避免改名或多 Key 绕过同一限额。
//
// schema-draft §5.5
type UpstreamAccount struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// OwnerUserID 分享者（账户归属），底座 users.id。
	OwnerUserID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_upaccount_owner_prov,priority:1"`
	// Provider 厂商/服务商标识。
	Provider string `gorm:"size:32;not null;default:'';index:idx_apigx_upaccount_owner_prov,priority:2"`
	// AccountLabel 脱敏展示标识（不含凭据、不含完整账号）。
	AccountLabel string `gorm:"size:64;not null;default:''"`
	// AccountKeyHash 由分享者声明的关联标识派生的摘要，用于去重；绝不由凭据明文派生。
	AccountKeyHash string `gorm:"type:char(64);uniqueIndex:idx_apigx_upaccount_uniq,priority:3"`
	// CapacityPolicyID 该账户池的共享限额，apigx_capacity_policies.id（scope_type=4）。
	CapacityPolicyID int64 `gorm:"bigint;not null;default:0"`
	// CircuitState 熔断状态：0=关闭 / 1=半开 / 2=打开（范围按账户池共享，§9.5）。
	CircuitState int `gorm:"type:int;not null"`
	// CircuitOpenedAt 熔断打开时间，UTC epoch 秒；0=未打开。
	CircuitOpenedAt int64 `gorm:"bigint;not null;default:0"`
	// CreatedAt 创建时间，UTC epoch 秒。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_upaccount_del"`
}

// TableName 返回表名。
func (UpstreamAccount) TableName() string { return TablePrefix + "upstream_accounts" }

// UpstreamKey 是渠道下的上游 Key：「渠道、稳定 ID、名称、secret_ref 与凭据版本、
// 启停/冷却/到期、权重、独立限额、上游账户池及价格配置」（计划书 §7 第 4 行）。
//
// schema-draft §5.6
type UpstreamKey struct {
	// ID 稳定内部 ID（apigx_attempts / apigx_async_tasks 引用它，永不变）。
	ID int64 `gorm:"primaryKey"`
	// ChannelID 所属分享渠道，apigx_channels.id。
	ChannelID int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_upkey_stable,priority:1;uniqueIndex:idx_apigx_upkey_fingerprint,priority:1;index:idx_apigx_upkey_pool,priority:1"`
	// StableKeyID 渠道内稳定标识：轮询/权重/统计都不依赖导入行号（CODE_STYLE.md 第 1 条）。
	StableKeyID string `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_upkey_stable,priority:2"`
	// Name 展示名。
	Name string `gorm:"size:64;not null;default:''"`
	// SecretRef 指向受保护凭据存储的引用，不含明文；主密钥与数据库分离（§10.3）。
	SecretRef string `gorm:"size:255;not null;default:''"`
	// SecretFingerprint 是凭据的 HMAC 指纹，仅用于查重（见 apigx/secretbox.Fingerprint）：
	// AES-GCM 用随机 nonce，同一明文每次密文都不同，重复导入只能靠指纹发现。
	// 与 channel_id、alive_flag 组成唯一索引：存活期间同渠道内不得重复，软删除后可重新导入。
	SecretFingerprint string `gorm:"type:char(64);not null;default:'';uniqueIndex:idx_apigx_upkey_fingerprint,priority:2"`
	// CredentialVersion 凭据版本号，轮换时递增（不新建行）。
	CredentialVersion int64 `gorm:"bigint;not null;default:0"`
	// CredentialStartedAt 当前凭据版本启用时间，UTC epoch 秒。
	CredentialStartedAt int64 `gorm:"bigint;not null;default:0"`
	// Status 状态：0=启用 / 1=禁用 / 2=冷却 / 3=已过期 / 4=鉴权失效 / 5=已撤销。
	Status int `gorm:"type:int;not null;index:idx_apigx_upkey_pool,priority:2;index:idx_apigx_upkey_account_status,priority:2;index:idx_apigx_upkey_cooldown,priority:1"`
	// Weight 权重（仅用于选 Key，与渠道权重不混用，§9.3）。
	Weight int64 `gorm:"bigint;not null;default:0;index:idx_apigx_upkey_pool,priority:3"`
	// CooldownUntil 冷却截止时间，UTC epoch 秒；0=未冷却。
	CooldownUntil int64 `gorm:"bigint;not null;default:0;index:idx_apigx_upkey_cooldown,priority:2"`
	// ExpiresAt 到期时间，UTC epoch 秒；0=不过期。
	ExpiresAt int64 `gorm:"bigint;not null;default:0"`
	// UpstreamAccountID 上游账户池归属，apigx_upstream_accounts.id；0=未归属。
	UpstreamAccountID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_upkey_account_status,priority:1"`
	// CapacityPolicyID Key 独立限额，apigx_capacity_policies.id（scope_type=3，四态）。
	CapacityPolicyID int64 `gorm:"bigint;not null;default:0"`
	// PriceRuleID Key 价格配置，apigx_price_rules.id；0=继承供给/渠道。
	PriceRuleID int64 `gorm:"bigint;not null;default:0"`
	// DiscoveryState 发现状态：0=未发现 / 1=已发现 / 2=发现失败 / 3=手工录入（§8.1）。
	DiscoveryState int `gorm:"type:int;not null"`
	// LastOKAt 最近一次成功时间，UTC epoch 秒；0=无记录。
	LastOKAt int64 `gorm:"bigint;not null;default:0"`
	// LastErrorAt 最近一次失败时间，UTC epoch 秒；0=无记录。
	LastErrorAt int64 `gorm:"bigint;not null;default:0"`
	// LastErrorCode 脱敏错误码。
	LastErrorCode string `gorm:"size:64;not null;default:''"`
	// AliveFlag 软删除墓碑列：0=存活，软删除时置为本行 ID（§3.5）。
	AliveFlag int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_upkey_stable,priority:3;uniqueIndex:idx_apigx_upkey_fingerprint,priority:3"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒；凭据轮换只更新它。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间；Key 退役用软删除 + status，绝不物理删除（§7）。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_upkey_del"`
}

// TableName 返回表名。
func (UpstreamKey) TableName() string { return TablePrefix + "upstream_keys" }

// UpstreamKeyCredential 保存每一版上游凭据的受保护引用。当前版本仍镜像在 UpstreamKey，
// 历史版本只允许已绑定的异步任务按 key_id + credential_version 读取，轮换或软删除 Key 不清理。
type UpstreamKeyCredential struct {
	ID                int64  `gorm:"primaryKey"`
	UpstreamKeyID     int64  `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_upkey_credential_version,priority:1"`
	CredentialVersion int64  `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_upkey_credential_version,priority:2"`
	SecretRef         string `gorm:"size:255;not null;default:''"`
	SecretFingerprint string `gorm:"type:char(64);not null;default:''"`
	StartedAt         int64  `gorm:"bigint;not null;default:0"`
	RetiredAt         int64  `gorm:"bigint;not null;default:0"`
	CreatedAt         int64  `gorm:"bigint;not null;default:0"`
}

func (UpstreamKeyCredential) TableName() string { return TablePrefix + "upstream_key_credentials" }

// KeyModelCapability 记录「Key 支持的上游模型和能力、发现/验证状态及时间」，
// 是「分享者声明 / 目录发现 / 连通性验证」三类证据的唯一落点（§8.1）。
// 刻意不直接挂 canonical_model_id：本表记录的是「上游自称什么」，归一由 apigx_model_aliases 负责。
//
// schema-draft §5.7
type KeyModelCapability struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// UpstreamKeyID 所属上游 Key，apigx_upstream_keys.id。
	UpstreamKeyID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_keycap_key_cap,priority:1"`
	// CapabilityKeyHash SHA-256(upstream_model_id + "|" + capability)；进唯一索引用，绕开长字符串前缀限制（§3.5）。
	CapabilityKeyHash string `gorm:"type:char(64);uniqueIndex:idx_apigx_keycap_uniq"`
	// UpstreamModelID 上游模型 ID（不可信输入，存原文）。
	UpstreamModelID string `gorm:"size:191;not null;default:''"`
	// Capability 能力枚举：1=对话 / 2=向量 / 3=图片生成 / 4=图片编辑 / 5=语音识别 / 6=语音生成 / 7=重排 / 8=视频。
	Capability int `gorm:"type:int;not null;index:idx_apigx_keycap_key_cap,priority:2;index:idx_apigx_keycap_evi_cap,priority:2"`
	// DiscoverySource 发现来源：0=手工输入 / 1=目录接口 / 2=主动探测。
	DiscoverySource int `gorm:"type:int;not null"`
	// EvidenceState 证据状态：0=分享者声明 / 1=目录发现 / 2=连通性验证通过 / 3=验证失败。
	EvidenceState int `gorm:"type:int;not null;index:idx_apigx_keycap_evi_cap,priority:1"`
	// DiscoveredAt 发现时间，UTC epoch 秒；不可变快照字段。
	DiscoveredAt int64 `gorm:"bigint;not null;default:0"`
	// VerifiedAt 最近验证时间，UTC epoch 秒；重新验证只更新它。
	VerifiedAt int64 `gorm:"bigint;not null;default:0"`
	// LastErrorCode 脱敏错误码。
	LastErrorCode string `gorm:"size:64;not null;default:''"`
	// RawMetadata 上游返回的原始描述 JSON（不可信，展示转义）；TEXT 列 + common.Marshal。
	RawMetadata string `gorm:"type:text"`
	// RawMetadataBytes 写入时的字节数（用于 MySQL text 上限监控，§3.4）。
	RawMetadataBytes int `gorm:"type:int;not null;default:0"`
	// CreatedAt 创建时间，UTC epoch 秒。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
}

// TableName 返回表名。
func (KeyModelCapability) TableName() string { return TablePrefix + "key_model_capabilities" }

// CanonicalModel 是「厂商、标准 ID、版本、类型、能力和描述来源」（计划书 §7 第 6 行）。
// 由平台维护；分享者只能提交映射建议或发布自定义模型，不能编辑全局标准模型（§7）。
//
// schema-draft §5.8
type CanonicalModel struct {
	// ID 稳定内部 ID；Grant、请求归属、历史计量一律用它。
	ID int64 `gorm:"primaryKey"`
	// APIModelID 对外标准 ID，客户端从可用目录/调用示例获取（§8.2）；发布后永不复用。
	APIModelID string `gorm:"size:191;not null;default:'';uniqueIndex:idx_apigx_canonmodel_api_id"`
	// VendorID 软引用底座 vendors.id；0=未匹配到已知厂商。
	VendorID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_canonmodel_vendor_fam,priority:1"`
	// VendorLabel 厂商展示名快照（上游厂商字段不可信，可能与底座厂商列表不一致）。
	VendorLabel string `gorm:"size:64;not null;default:''"`
	// ModelFamily 模型族名（如 gpt、claude）。
	ModelFamily string `gorm:"size:128;not null;default:'';index:idx_apigx_canonmodel_vendor_fam,priority:2"`
	// VersionLabel 版本标识（如 4o、3-5-sonnet）；不通过删除版本号/日期/量化后缀来强行归并（§8.2）。
	VersionLabel string `gorm:"size:64;not null;default:''"`
	// Variant 变体（如 latest、preview、thinking）；动态 latest 别名必须在此标注（§8.2）。
	Variant string `gorm:"size:64;not null;default:''"`
	// ModalityType 主类型：1=对话 / 2=向量 / 3=图片 / 4=语音识别 / 5=语音生成 / 6=重排 / 7=视频。
	ModalityType int `gorm:"type:int;not null;index:idx_apigx_canonmodel_status_type,priority:2"`
	// Capabilities JSON：平台级参考能力（工具调用、视觉输入、上下文长度、JSON 格式等）；
	// 供给的实际能力以 apigx_model_offerings.capabilities 为准（§8.2）。TEXT 列 + common.Marshal。
	Capabilities string `gorm:"type:text"`
	// DescriptionSource 描述来源：0=平台维护 / 1=上游声明 / 2=外部来源。
	DescriptionSource int `gorm:"type:int;not null"`
	// Description 描述（不可信内容，渲染前转义）。
	Description string `gorm:"type:text"`
	// Status 状态：0=草稿 / 1=生效 / 2=已弃用 / 3=已合并。
	Status int `gorm:"type:int;not null;index:idx_apigx_canonmodel_status_type,priority:1"`
	// MergedIntoID 合并目标，apigx_canonical_models.id；仅作路由与展示提示，不改变已有授权或历史归属（§7）。
	MergedIntoID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_canonmodel_merged_into"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
}

// TableName 返回表名。
func (CanonicalModel) TableName() string { return TablePrefix + "canonical_models" }

// ModelAlias 记录「上游或外部价格 ID 到标准模型的映射、范围和确认状态」。
// §8.4 要求「通过独立的『价格来源映射』连接标准模型」，并保留厂商入口/地区/服务档位。
//
// schema-draft §5.9
type ModelAlias struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// ScopeType 范围类型：0=平台全局 / 1=厂商 / 2=渠道 / 3=供给。
	ScopeType int `gorm:"type:int;not null;index:idx_apigx_modelalias_scope_state,priority:1"`
	// ScopeID 对应范围 ID；scope_type=0 时为 0。
	ScopeID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_modelalias_scope_state,priority:2"`
	// Source 来源：0=上游目录 / 1=外部价格源（模眼/ModelRadar/models.dev/OpenRouter） / 2=分享者建议。
	Source int `gorm:"type:int;not null;index:idx_apigx_modelalias_source_norm,priority:1"`
	// SourceRef 来源标识/URL（不可信，展示转义；出站前 SSRF 校验）。
	SourceRef string `gorm:"size:255;not null;default:''"`
	// ExternalModelID 上游或外部价格库的模型 ID（原文）。
	ExternalModelID string `gorm:"size:191;not null;default:''"`
	// NormalizedModelID 归一化后的匹配键（大小写/空白归一；不做版本号或日期剥离，§8.2）。
	NormalizedModelID string `gorm:"size:191;not null;default:'';index:idx_apigx_modelalias_source_norm,priority:2"`
	// AliasKeyHash SHA-256(scope_type|scope_id|source|normalized_model_id)；进唯一索引。
	AliasKeyHash string `gorm:"type:char(64);uniqueIndex:idx_apigx_modelalias_uniq,priority:1"`
	// TargetCanonicalModelID 映射目标，apigx_canonical_models.id。
	TargetCanonicalModelID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_modelalias_target_state,priority:1"`
	// Relation 关系：0=完全匹配 / 1=已确认别名 / 2=候选（仅提示，不自动路由）。
	Relation int `gorm:"type:int;not null"`
	// ConfirmationState 确认状态：0=待确认 / 1=已确认 / 2=已拒绝。
	ConfirmationState int `gorm:"type:int;not null;index:idx_apigx_modelalias_scope_state,priority:3;index:idx_apigx_modelalias_target_state,priority:2"`
	// ConfirmedByUserID 确认人，底座 users.id；分享者只能确认自己范围内的映射（§7）。
	ConfirmedByUserID int64 `gorm:"bigint;not null;default:0"`
	// ConfirmedAt 确认时间，UTC epoch 秒；0=未确认。
	ConfirmedAt int64 `gorm:"bigint;not null;default:0"`
	// RegionLabel 厂商入口/地区；同模型跨地区报价不互相覆盖（§8.3）。
	RegionLabel string `gorm:"size:64;not null;default:''"`
	// TierLabel 服务档位（Standard/Batch 等，§8.3）。
	TierLabel string `gorm:"size:64;not null;default:''"`
	// Evidence 证据 JSON（来源、抓取时间、页完整性标记；不可信）；TEXT 列 + common.Marshal。
	Evidence string `gorm:"type:text"`
	// AliveFlag 软删除墓碑列：0=存活，软删除时置为本行 ID（§3.5）。
	AliveFlag int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_modelalias_uniq,priority:2"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_modelalias_del"`
}

// TableName 返回表名。
func (ModelAlias) TableName() string { return TablePrefix + "model_aliases" }

// ModelOffering 是「标准模型与渠道的关联、上游 ID、实际能力、访问策略、价格版本」。
// 这是「指定来源」的粒度，也是 X-APIGX-Offering 请求头的取值来源（§9.1）。
//
// schema-draft §5.10
type ModelOffering struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// PublicID 对外 offer id，X-APIGX-Offering 用它（§9.1）。
	PublicID string `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_offering_pub"`
	// ChannelID 所属渠道，apigx_channels.id。
	ChannelID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_offering_channel,priority:1"`
	// CanonicalModelID 标准模型，apigx_canonical_models.id。
	CanonicalModelID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_offering_canon,priority:1"`
	// UpstreamModelID 转发时替换成的上游模型 ID（§8.2）。
	UpstreamModelID string `gorm:"size:191;not null;default:''"`
	// OfferingKeyHash SHA-256(channel_id|canonical_model_id|upstream_model_id)；进唯一索引。
	OfferingKeyHash string `gorm:"type:char(64);uniqueIndex:idx_apigx_offering_uniq,priority:1"`
	// Capabilities JSON：该供给的实际能力（工具调用、视觉、上下文长度、JSON、音色、视频规格等，§8.2）。
	Capabilities string `gorm:"type:text"`
	// AccessDefault 默认访问级别：0=私有 / 1=受限 / 2=公开；与分组默认策略求交，不能放宽父级（§7）。
	AccessDefault int `gorm:"type:int;not null;index:idx_apigx_offering_canon,priority:3"`
	// CapacityPolicyID 供给级容量策略，apigx_capacity_policies.id（scope_type=5）。
	CapacityPolicyID int64 `gorm:"bigint;not null;default:0"`
	// PriceRuleID 当前生效价格版本，apigx_price_rules.id；0=未定价。
	PriceRuleID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_offering_status_price,priority:2"`
	// Status 状态：0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停 / 4=已退役。
	Status int `gorm:"type:int;not null;index:idx_apigx_offering_canon,priority:2;index:idx_apigx_offering_channel,priority:2;index:idx_apigx_offering_status_price,priority:1"`
	// Revision 资源修订号；上游模型 ID 变更时递增。
	Revision int64 `gorm:"bigint;not null;default:0"`
	// VerifiedAt 最近连通性验证时间，UTC epoch 秒；0=未验证。
	VerifiedAt int64 `gorm:"bigint;not null;default:0"`
	// AliveFlag 软删除墓碑列：0=存活，软删除时置为本行 ID（§3.5）。
	AliveFlag int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_offering_uniq,priority:2"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
	// DeletedAt 软删除时间。
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_offering_del"`
}

// TableName 返回表名。
func (ModelOffering) TableName() string { return TablePrefix + "model_offerings" }

// AccessPolicy 是「公开、私有或受限策略」（计划书 §7 第 10 行前半）；
// 可设在分组（默认值）与供给（进一步收紧）两级。
//
// schema-draft §5.11
type AccessPolicy struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// OwnerUserID 策略归属，底座 users.id（服务端按登录用户校验归属，§6）。
	OwnerUserID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_accesspolicy_owner_scope,priority:1"`
	// ScopeType 范围类型：0=分享分组默认策略 / 1=供给策略。
	ScopeType int `gorm:"type:int;not null;uniqueIndex:idx_apigx_accesspolicy_scope,priority:1;index:idx_apigx_accesspolicy_owner_scope,priority:2;index:idx_apigx_accesspolicy_scope_vis,priority:1"`
	// ScopeID 范围 ID，apigx_share_groups.id 或 apigx_model_offerings.id。
	ScopeID int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_accesspolicy_scope,priority:2"`
	// Visibility 可见性：0=私有（仅本人） / 1=受限（仅获授权者可见） / 2=公开（可浏览）。
	Visibility int `gorm:"type:int;not null;index:idx_apigx_accesspolicy_scope_vis,priority:2"`
	// AllowAutoRoute 是否允许进入自动路由候选（可见 ≠ 可调用，§2/§7）。
	AllowAutoRoute bool `gorm:"not null"`
	// RequireTrustedSharer 是否只允许可信分享者范围内的用户调用。
	RequireTrustedSharer bool `gorm:"not null"`
	// PolicyVersion 权限版本号；每次变更 +1，用于权限变更传播与出站前重验（§10.3）。
	PolicyVersion int64 `gorm:"bigint;not null;default:0"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
}

// TableName 返回表名。
func (AccessPolicy) TableName() string { return TablePrefix + "access_policies" }

// Grant 是「用户授权、来源、到期和撤销状态」（计划书 §7 第 10 行后半）；
// 资源邀请兑换和指定用户都生成受资源范围约束的 Grant（§7）。
//
// schema-draft §5.12
type Grant struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// SubjectType 主体类型：0=用户 / 1=平台 Key / 2=邀请。
	SubjectType int `gorm:"type:int;not null;uniqueIndex:idx_apigx_grant_uniq,priority:1;index:idx_apigx_grant_subject,priority:1"`
	// SubjectID 主体 ID，按 subject_type 指向底座 users.id / apigx_api_keys.id / apigx_invites.id。
	SubjectID int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_grant_uniq,priority:2;index:idx_apigx_grant_subject,priority:2"`
	// ResourceType 资源类型：0=分享分组 / 1=模型供给。
	ResourceType int `gorm:"type:int;not null;uniqueIndex:idx_apigx_grant_uniq,priority:3;index:idx_apigx_grant_resource,priority:1"`
	// ResourceID 资源 ID，按 resource_type 指向 apigx_share_groups.id / apigx_model_offerings.id。
	ResourceID int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_grant_uniq,priority:4;index:idx_apigx_grant_resource,priority:2"`
	// ScopeKind 授权来源类别：0=指定用户 / 1=邀请兑换 / 2=所有者本人。
	ScopeKind int `gorm:"type:int;not null"`
	// SourceInviteID 来源邀请，apigx_invites.id；0 表示非邀请来源（不是 NULL，§5.12）。
	SourceInviteID int64 `gorm:"bigint;not null;default:0;uniqueIndex:idx_apigx_grant_uniq,priority:5;index:idx_apigx_grant_source_invite"`
	// GrantedByUserID 授予人，底座 users.id。
	GrantedByUserID int64 `gorm:"bigint;not null;default:0"`
	// GrantedAt 授予时间，UTC epoch 秒；不可变快照字段。
	GrantedAt int64 `gorm:"bigint;not null;default:0"`
	// ExpiresAt 到期时间，UTC epoch 秒；0=不过期。已生成 Grant 有独立到期时间，不随邀请到期而变（§7）。
	ExpiresAt int64 `gorm:"bigint;not null;default:0;index:idx_apigx_grant_subject,priority:4"`
	// GiftVersion 授予时的 apigx_access_policies.policy_version 快照，用于审计「授予时是什么策略」。
	GiftVersion int64 `gorm:"bigint;not null;default:0"`
	// RevokedAt 撤销时间，UTC epoch 秒；0=未撤销。只允许单向推进一次。
	RevokedAt int64 `gorm:"bigint;not null;default:0;index:idx_apigx_grant_subject,priority:3;index:idx_apigx_grant_resource,priority:3"`
	// RevokedByUserID 撤销人，底座 users.id。
	RevokedByUserID int64 `gorm:"bigint;not null;default:0"`
	// RevokeReason 脱敏撤销原因。
	RevokeReason string `gorm:"size:255;not null;default:''"`
	// CreatedAt 创建时间，UTC epoch 秒。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
}

// TableName 返回表名。
func (Grant) TableName() string { return TablePrefix + "grants" }

// Invite 是「注册邀请或资源邀请、Token 哈希、使用次数、有效期与目标范围」。
// §10.1 要求注册邀请码与资源访问邀请码使用独立类型和校验逻辑，邀请码保存哈希，
// 支持到期、次数限制、撤销和并发兑换时的原子扣减。
//
// schema-draft §5.13
type Invite struct {
	// ID 稳定内部 ID。
	ID int64 `gorm:"primaryKey"`
	// InviteType 邀请类型：0=注册邀请 / 1=资源访问邀请；两类共用表但校验逻辑独立（§10.1）。
	InviteType int `gorm:"type:int;not null;index:idx_apigx_invite_type_status_exp,priority:1"`
	// TokenHash SHA-256 十六进制，不存明文；不可恢复。
	TokenHash string `gorm:"type:char(64);uniqueIndex:idx_apigx_invite_token_hash"`
	// TokenPrefix 明文前 8 位，仅供界面识别。
	TokenPrefix string `gorm:"size:12;not null;default:''"`
	// TargetScopeType 目标范围类型：0=无（注册邀请） / 1=分享分组 / 2=模型供给。
	TargetScopeType int `gorm:"type:int;not null;index:idx_apigx_invite_target_status,priority:1"`
	// TargetScopeID 目标范围 ID；target_scope_type=0 时为 0。
	TargetScopeID int64 `gorm:"bigint;not null;default:0;index:idx_apigx_invite_target_status,priority:2"`
	// UseLimitMode 使用次数限制模式（四态，§3.3）。
	UseLimitMode int `gorm:"type:int;not null"`
	// MaxUses 最大使用次数；use_limit_mode=4 时有效且 > 0。
	MaxUses int64 `gorm:"bigint;not null;default:0"`
	// UsedCount 已使用次数；兑换时用一条 UPDATE ... WHERE status=0 AND used_count < max_uses 原子递增。
	UsedCount int64 `gorm:"bigint;not null;default:0"`
	// ExpiresAt 到期时间，UTC epoch 秒；0=不过期。
	ExpiresAt int64 `gorm:"bigint;not null;default:0;index:idx_apigx_invite_type_status_exp,priority:3"`
	// Status 状态：0=有效 / 1=已撤销 / 2=已用尽 / 3=已过期。
	Status int `gorm:"type:int;not null;index:idx_apigx_invite_target_status,priority:3;index:idx_apigx_invite_type_status_exp,priority:2"`
	// CreatedByUserID 创建人，底座 users.id；不可变快照字段。
	CreatedByUserID int64 `gorm:"bigint;not null;default:0"`
	// RevokedAt 撤销时间，UTC epoch 秒；0=未撤销。
	RevokedAt int64 `gorm:"bigint;not null;default:0"`
	// RevokedByUserID 撤销人，底座 users.id。
	RevokedByUserID int64 `gorm:"bigint;not null;default:0"`
	// RevokeGrants 撤销邀请时是否连带撤销由它产生的 Grant（§7 明确要求的可选项）。
	RevokeGrants bool `gorm:"not null"`
	// CreatedAt 创建时间，UTC epoch 秒；不可变快照字段。
	CreatedAt int64 `gorm:"bigint;not null;default:0"`
	// UpdatedAt 最近更新时间，UTC epoch 秒；原子扣减时一并写入。
	UpdatedAt int64 `gorm:"bigint;not null;default:0"`
}

// TableName 返回表名。
func (Invite) TableName() string { return TablePrefix + "invites" }
