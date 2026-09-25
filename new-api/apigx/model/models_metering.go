// 本文件对应 `docs/stage0/schema-draft.md` §5.14–§5.27，字段与约束依据该文档，不得自行增删字段。
//
// 覆盖 14 张计量 / 计价 / 治理表：
//
//	§5.14 apigx_fx_rates              §5.21 apigx_usage_events
//	§5.15 apigx_price_rules           §5.22 apigx_quality_buckets
//	§5.16 apigx_price_rule_items      §5.23 apigx_async_tasks
//	§5.17 apigx_capacity_policies     §5.24 apigx_upstream_object_refs
//	§5.18 apigx_reservations          §5.25 apigx_audit_events
//	§5.19 apigx_requests              §5.26 apigx_idempotency_records
//	§5.20 apigx_attempts              §5.27 apigx_platform_settings
//
// 书写规约全部来自同文档 §3 与包注释 `base.go`：类型别名 → GORM tag 对照表（§3.2）、
// 四态枚举（§3.3）、JSON 列一律 TEXT + 字节上界校验（§3.4）、保留期（§3.6）。
// 每一条索引都显式命名且 ≤ 40 字符，复合索引用 priority 明确顺序。
//
// 下列 14 张表全部**无软删除**（文档逐表声明「软删除：无」），因此不出现 `gorm.DeletedAt`
// 与 `alive_flag` 墓碑列；生命周期分别靠 `state` / `status` / 保留期清理表达。
package apigxmodel

// FxRate 对应 §5.14 `apigx_fx_rates`。
//
// 用途：PriceSnapshot 的汇率版本（派生表，服务 §8.5）——汇率必须保存来源、生效时间及
// 版本，不能将某个固定汇率写死为永久规则；不引入未经选择的第三方汇率依赖。
//
// 唯一约束：`uniqueIndex(quote_currency, effective_from)`——同一币种同一生效时刻只有一版。
// 不可变快照：整行几乎不可变；唯一允许的变更是 `effective_to` 从 0 单向封版，不允许改回。
type FxRate struct {
	ID                  int64  `gorm:"primaryKey"`                                                                                                 // 主键，自增交给 GORM
	BaseCurrency        string `gorm:"type:char(3);not null"`                                                                                      // 基础币种，固定 USD；快照
	QuoteCurrency       string `gorm:"type:char(3);not null;uniqueIndex:uniq_fxrate_ccy_from,priority:1;index:idx_fxrate_ccy_from,priority:1"`     // 原币种，如 CNY；快照
	RateMicro           int64  `gorm:"bigint;not null;default:0"`                                                                                  // 1 USD = rate_micro / 1e6 原币（7.123456 CNY → 7123456）；快照
	ConversionDirection int    `gorm:"type:int;not null"`                                                                                          // 换算方向；0 = USD = quote / rate，避免方向歧义；快照
	EffectiveFrom       int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_fxrate_ccy_from,priority:2;index:idx_fxrate_ccy_from,priority:2"` // 生效时刻（UTC epoch 秒）；快照
	EffectiveTo         int64  `gorm:"bigint;not null;default:0"`                                                                                  // 失效时刻；0=至今，仅允许单向封版一次
	SourceLabel         string `gorm:"size:64;not null;default:''"`                                                                                // 依据来源（如 admin），不含凭据；快照
	Note                string `gorm:"size:255;not null;default:''"`                                                                               // 说明
	CreatedByUserID     int64  `gorm:"bigint;not null;default:0"`                                                                                  // 创建者
	CreatedAt           int64  `gorm:"bigint;not null;default:0"`                                                                                  // 创建时刻（UTC epoch 秒）；快照
}

// TableName 返回 §5.14 定义的表名。
func (FxRate) TableName() string { return TablePrefix + "fx_rates" }

// PriceRule 对应 §5.15 `apigx_price_rules`。
//
// 用途：PriceRule / PriceSnapshot（不可变价格版本）——来源、原始记录、原币种、USD 价格、
// 汇率快照、单位、倍率口径、条件、人工覆盖与生效时间（计划书 §7 第 12 行）。新价格 = 新行 +
// `version+1`，绝不原地改价；人工覆盖优先且锁定。
//
// 唯一约束：`uniqueIndex(scope_type, scope_id, version)`。
// 不可变快照：除 `effective_to`（单向封版）与 `validation_state` / `validation_note`
// （待校验推进为终态一次）外整行不可变；`source`、`currency`、`fx_rate_id`、`fx_rate_micro`、
// `conditions`、`raw_record`、`effective_from`、`version` 一次写入永不修改。
type PriceRule struct {
	ID              int64  `gorm:"primaryKey"`                                                                                                             // 主键
	ScopeType       int    `gorm:"type:int;not null;uniqueIndex:uniq_pricerule_scope_version,priority:1;index:idx_pricerule_scope_eff,priority:1"`         // 范围：0=供给 / 1=上游 Key / 2=渠道；快照
	ScopeID         int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_pricerule_scope_version,priority:2;index:idx_pricerule_scope_eff,priority:2"` // 范围稳定 ID；快照
	Version         int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_pricerule_scope_version,priority:3"`                                          // 该 scope 内单调递增的版本号；快照
	Source          int    `gorm:"type:int;not null;index:idx_pricerule_source_eff,priority:1"`                                                            // 来源：0=人工覆盖 / 1=上游渠道报价 / 2=模眼 / 3=ModelRadar / 4=New API 内置预设 / 5=models.dev / 6=OpenRouter；快照
	SourceRef       string `gorm:"size:255;not null;default:''"`                                                                                           // 来源 URL 或标识（不可信；出站前 SSRF 校验，展示转义）
	Currency        string `gorm:"type:char(3);not null"`                                                                                                  // 原币种；快照
	PricingMode     int    `gorm:"type:int;not null"`                                                                                                      // 计价方式：0=分项绝对单价 / 1=按次（model_price） / 2=旧倍率兼容导入
	LegacyRatios    string `gorm:"type:text"`                                                                                                              // JSON：旧倍率原文，仅作兼容导入证据，不参与运行时计算
	FxRateID        int64  `gorm:"bigint;not null;default:0"`                                                                                              // 汇率版本；0=无需换算（原币种为 USD）；快照
	FxRateMicro     int64  `gorm:"bigint;not null;default:0"`                                                                                              // 汇率快照；USD 来源时为 1000000；快照
	UsdConverted    bool   `gorm:"not null"`                                                                                                               // 是否已换算到 USD
	ManualOverride  bool   `gorm:"not null"`                                                                                                               // 是否人工覆盖（人工覆盖优先，同步不得改写）
	Locked          bool   `gorm:"not null"`                                                                                                               // 人工覆盖锁定
	Conditions      string `gorm:"type:text"`                                                                                                              // JSON：条件（分辨率/质量档位/上下文阶梯/服务档位/是否 Batch/缓存条件/地区/档位）；快照
	ValidationState int    `gorm:"type:int;not null;index:idx_pricerule_valid_created,priority:1"`                                                         // 校验状态：0=待校验 / 1=已接受 / 2=已拒绝 / 3=冲突；仅单向推进一次
	ValidationNote  string `gorm:"size:255;not null;default:''"`                                                                                           // 脱敏校验说明；仅单向推进一次
	RawRecord       string `gorm:"type:text"`                                                                                                              // 来源原始记录（含分页完整性标记；不可信）；快照
	RawRecordBytes  int    `gorm:"type:int;not null;default:0"`                                                                                            // 写入字节数（MySQL text 上限监控）
	CreatedByUserID int64  `gorm:"bigint;not null;default:0"`                                                                                              // 创建者；0=系统同步
	EffectiveFrom   int64  `gorm:"bigint;not null;default:0;index:idx_pricerule_scope_eff,priority:3;index:idx_pricerule_source_eff,priority:2"`           // 生效时刻；快照
	EffectiveTo     int64  `gorm:"bigint;not null;default:0"`                                                                                              // 失效时刻；0=至今，被新版本取代时封版一次
	CreatedAt       int64  `gorm:"bigint;not null;default:0;index:idx_pricerule_valid_created,priority:2"`                                                 // 创建时刻；快照
}

// TableName 返回 §5.15 定义的表名。
func (PriceRule) TableName() string { return TablePrefix + "price_rules" }

// PriceRuleItem 对应 §5.16 `apigx_price_rule_items`。
//
// 用途：价格分项（派生子表，服务 §8.5）——估算基础为「各个不重叠计量分项的用量 × 对应单价
// 之和」；分项落表才能用 SQL 做按请求数/按秒/按张的聚合与对账。
//
// 唯一约束：`uniqueIndex(price_rule_id, dimension)`。
// 不可变快照：全部字段（随父行一起不可变），绝不回写。
// 「价格未知」与「价格为零」分别存储：本表**没有任何行** = 未知；有行且 `unit_price_micro = 0`
// = 明确零价。**不允许**用「单价 0」表示未知。
type PriceRuleItem struct {
	ID                 int64  `gorm:"primaryKey"`                                                                                             // 主键
	PriceRuleID        int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_pritem_rule_dim,priority:1"`                                  // 所属价格版本；快照
	Dimension          int    `gorm:"type:int;not null;uniqueIndex:uniq_pritem_rule_dim,priority:2;index:idx_pritem_dim_currency,priority:1"` // 计量维度：0=输入 token / 1=输出 token / 2=缓存读 token / 3=缓存写 token / 4=推理 token / 5=图片张数 / 6=输入音频秒 / 7=输出音频秒 / 8=输出视频秒 / 9=请求次数 / 10=输入字符数 / 11=文档数；快照
	UnitScale          int    `gorm:"type:int;not null"`                                                                                      // 单价单位：0=/1M tokens / 1=/1K tokens / 2=/张 / 3=/秒 / 4=/分钟 / 5=/次 / 6=/1K 字符 / 7=/文档；快照
	UnitPriceMicro     int64  `gorm:"bigint;not null;default:0"`                                                                              // 原币种绝对单价（micro）；快照
	Currency           string `gorm:"type:char(3);not null;index:idx_pritem_dim_currency,priority:2"`                                         // 原币种；快照（进索引用于跨供给横向价格对比）
	UsdUnitPriceMicro  int64  `gorm:"bigint;not null;default:0"`                                                                              // 换算后 USD 单价；未换算时为 0；快照
	UsdState           int    `gorm:"type:int;not null;index:idx_pritem_usd_state"`                                                           // 换算状态：0=已换算 / 1=待汇率 / 2=不适用（原币种即 USD）；快照（服务「待换算队列」）
	CountedInDimension int    `gorm:"type:int;not null;default:0"`                                                                            // 该分项是否已包含在另一分项内（-1=独立分项），表达「缓存 token 包含在输入总量中」；快照
	CreatedAt          int64  `gorm:"bigint;not null;default:0"`                                                                              // 创建时刻；快照
}

// TableName 返回 §5.16 定义的表名。
func (PriceRuleItem) TableName() string { return TablePrefix + "price_rule_items" }

// CapacityPolicy 对应 §5.17 `apigx_capacity_policies`。
//
// 用途：限制维度与配置版本（计划书 §7 第 13 行）——每个容量维度都是「模式列 + 值列」成对
// 出现的四态枚举（§3.3），空字段不得被解释成无限容量；校验落在 Go 侧，数据库不写 CHECK。
//
// 唯一约束：`uniqueIndex(scope_type, scope_id)`——一个范围一份当前策略，版本靠 `policy_version`
// 递增。不可变快照：`scope_type`、`scope_id`、`created_at`；全部维度列绝不追溯改写已有预留或
// 历史用量（已有请求/任务持有的是 `apigx_reservations.policy_version` 快照）。
type CapacityPolicy struct {
	ID                  int64  `gorm:"primaryKey"`                                                                                         // 主键
	OwnerUserID         int64  `gorm:"bigint;not null;default:0;index:idx_cappol_owner_scope,priority:1"`                                  // 配置归属（服务端按登录用户校验，防越权改他人限额）
	ScopeType           int    `gorm:"type:int;not null;uniqueIndex:uniq_cappol_scope,priority:1;index:idx_cappol_owner_scope,priority:2"` // 范围：0=用户 / 1=平台 Key / 2=渠道 / 3=上游 Key / 4=上游账户池 / 5=供给；快照
	ScopeID             int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_cappol_scope,priority:2"`                                 // 范围稳定 ID；快照
	PolicyVersion       int64  `gorm:"bigint;not null;default:0"`                                                                          // 配置版本；调度实时读取生效版本
	MaxConcurrencyMode  int    `gorm:"type:int;not null"`                                                                                  // 最大并发请求数：模式列（0=未配置 / 1=禁用 / 2=继承上级默认 / 3=明确不限 / 4=明确值）
	MaxConcurrency      int64  `gorm:"bigint;not null;default:0"`                                                                          // 最大并发请求数：值列（所有实例共享占用数，不能各自单独计算）
	RpmMode             int    `gorm:"type:int;not null"`                                                                                  // 每分钟请求数：模式列
	RpmLimit            int64  `gorm:"bigint;not null;default:0"`                                                                          // 每分钟请求数：值列
	TpmMode             int    `gorm:"type:int;not null"`                                                                                  // 每分钟 token 数：模式列
	TpmLimit            int64  `gorm:"bigint;not null;default:0"`                                                                          // 每分钟 token 数：值列
	MaxRunningTasksMode int    `gorm:"type:int;not null"`                                                                                  // 最大运行中异步任务数：模式列
	MaxRunningTasks     int64  `gorm:"bigint;not null;default:0"`                                                                          // 最大运行中异步任务数：值列（提交成功后持续占用到终态）
	DailyRequestMode    int    `gorm:"type:int;not null"`                                                                                  // 每日请求数：模式列
	DailyRequestLimit   int64  `gorm:"bigint;not null;default:0"`                                                                          // 每日请求数：值列
	DailyBudgetMode     int    `gorm:"type:int;not null"`                                                                                  // 每日预算：模式列
	DailyBudgetMicro    int64  `gorm:"bigint;not null;default:0"`                                                                          // 每日 USD 估算预算（micro）：值列
	MaxInputTokensMode  int    `gorm:"type:int;not null"`                                                                                  // 单次输入上限：模式列
	MaxInputTokens      int64  `gorm:"bigint;not null;default:0"`                                                                          // 单次输入上限：值列
	MaxOutputTokensMode int    `gorm:"type:int;not null"`                                                                                  // 单次输出上限：模式列
	MaxOutputTokens     int64  `gorm:"bigint;not null;default:0"`                                                                          // 单次输出上限：值列（必须是**可执行的**，不能只是展示值）
	MaxImageSideMode    int    `gorm:"type:int;not null"`                                                                                  // 图片边长上限：模式列
	MaxImageSide        int64  `gorm:"bigint;not null;default:0"`                                                                          // 图片边长上限：值列
	MaxAudioSecondsMode int    `gorm:"type:int;not null"`                                                                                  // 音频时长上限：模式列
	MaxAudioSeconds     int64  `gorm:"bigint;not null;default:0"`                                                                          // 音频时长上限（秒）：值列
	MaxVideoSecondsMode int    `gorm:"type:int;not null"`                                                                                  // 视频时长上限：模式列
	MaxVideoSeconds     int64  `gorm:"bigint;not null;default:0"`                                                                          // 视频时长上限（秒）：值列
	QueueMode           int    `gorm:"type:int;not null"`                                                                                  // 排队模式：0=不排队（满载即 429） / 1=有限短等待
	QueueMaxWaitMs      int64  `gorm:"bigint;not null;default:0"`                                                                          // 排队上限毫秒；queue_mode=0 时置 0
	LimitTimezone       string `gorm:"size:64;not null;default:''"`                                                                        // 日限额划分时区，默认 Asia/Shanghai
	CreatedAt           int64  `gorm:"bigint;not null;default:0"`                                                                          // 创建时刻；快照
	UpdatedAt           int64  `gorm:"bigint;not null;default:0"`                                                                          // 更新时刻
}

// TableName 返回 §5.17 定义的表名。
func (CapacityPolicy) TableName() string { return TablePrefix + "capacity_policies" }

// Reservation 对应 §5.18 `apigx_reservations`。
//
// 用途：并发或运行任务租约、RPM/TPM 与预算预留、归还和过期状态（计划书 §7 第 13 行）——这是
// 「持久预算与请求状态由数据库事务维护」的**持久侧**；Redis 侧并发/速率计数器不在此表。
//
// 唯一约束：`uniqueIndex(reservation_uid)` 与
// `uniqueIndex(request_id, dimension, scope_type, scope_id, attempt_id)`——防止同一请求对同一
// 维度重复预留。本表是**追加式**的：`amount_micro` 是首次估算证据、**永不改写**，结算差异写进
// `settled_amount_micro`（只增不改）。租约到期**不证明**上游已停止，`state` 必须先进入 4=对账中。
type Reservation struct {
	ID                 int64  `gorm:"primaryKey"`                                                                                                       // 主键
	ReservationUID     string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_resv_uid"`                                                            // 预留幂等 ID；归还、续租、对账全部靠它；快照
	RequestID          int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_resv_request_dim,priority:1;index:idx_resv_scope_cycle_dim,priority:2"` // 所属请求；快照
	AttemptID          int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_resv_request_dim,priority:5"`                                           // 所属尝试；0=请求级预留；快照
	AsyncTaskID        int64  `gorm:"bigint;not null;default:0"`                                                                                        // 所属异步任务；0=非任务；快照
	CapacityPolicyID   int64  `gorm:"bigint;not null;default:0"`                                                                                        // 容量策略；快照
	PolicyVersion      int64  `gorm:"bigint;not null;default:0"`                                                                                        // 配置版本快照；绝不回写
	ScopeType          int    `gorm:"type:int;not null;uniqueIndex:uniq_resv_request_dim,priority:3;index:idx_resv_scope_cycle_dim,priority:1"`         // 范围类型（冗余便于聚合与对账）；快照
	ScopeID            int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_resv_request_dim,priority:4;index:idx_resv_scope_cycle_dim,priority:3"` // 范围稳定 ID（冗余）；快照
	Dimension          int    `gorm:"type:int;not null;uniqueIndex:uniq_resv_request_dim,priority:2;index:idx_resv_scope_cycle_dim,priority:4"`         // 维度：0=并发 / 1=RPM / 2=TPM / 3=运行任务 / 4=USD 预算 / 5=请求数；快照
	AmountMicro        int64  `gorm:"bigint;not null;default:0"`                                                                                        // 预留额（USD 预算用 micro，计数类×1e6 以统一类型）；**永不改写**
	SettledAmountMicro int64  `gorm:"bigint;not null;default:0"`                                                                                        // 实际用量调整后的值（追加而非覆盖 amount_micro，只增不改）
	State              int    `gorm:"type:int;not null;index:idx_resv_state_expires,priority:1"`                                                        // 状态：0=已持有 / 1=已结算 / 2=已释放 / 3=已过期 / 4=对账中；仅单向推进
	CycleKey           string `gorm:"size:16;not null;default:'';index:idx_resv_scope_cycle_dim,priority:4"`                                            // 准入日（Asia/Shanghai 的 YYYY-MM-DD；跨日完成的结算回到原预留周期）；快照
	AcquiredAt         int64  `gorm:"bigint;not null;default:0"`                                                                                        // 获取时刻；快照
	ExpiresAt          int64  `gorm:"bigint;not null;default:0;index:idx_resv_state_expires,priority:2"`                                                // 租约到期时刻（长流用续租）；服务对账/恢复扫描
	RenewedAt          int64  `gorm:"bigint;not null;default:0"`                                                                                        // 最近续租时刻；仅单向推进
	ReleasedAt         int64  `gorm:"bigint;not null;default:0"`                                                                                        // 释放时刻；仅单向推进
	LeaseOwnerNode     string `gorm:"size:128;not null;default:''"`                                                                                     // 实例标识，用于进程异常后的恢复与对账
	CreatedAt          int64  `gorm:"bigint;not null;default:0"`                                                                                        // 创建时刻；快照
	UpdatedAt          int64  `gorm:"bigint;not null;default:0"`                                                                                        // 更新时刻
}

// TableName 返回 §5.18 定义的表名。
func (Reservation) TableName() string { return TablePrefix + "reservations" }

// Request 对应 §5.19 `apigx_requests`。
//
// 用途：一次用户请求的权威记录（计划书 §7 第 14 行前半；§8.5「一次用户调用对应一个 Request」）。
// **不记录请求正文**——平台默认只记录请求元数据和用量，不保存提示词、输出文本或原始音视频。
//
// 唯一约束：`uniqueIndex(request_uid)`。幂等唯一键**不放本表**（非幂等请求没有幂等键，空字符串
// 会互相冲突），由 §5.26 `apigx_idempotency_records` 承担。
// 不可变快照：`request_uid`、`requested_model`、`api_model_id`、`canonical_model_id`、`route_mode`、
// `pinned_offering_id`、`resolved_scope_kind`、`allow_fallback`、`admission_cycle_key`、
// `admission_at`、`client_ip_hash`、`idempotency_record_id`、`created_at`。
// 仅允许单向推进（幂等 CAS）：耗时/计数/终态列。
type Request struct {
	ID                  int64  `gorm:"primaryKey"`                                                                                                                                                 // 主键
	RequestUID          string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_request_uid"`                                                                                                   // 对外脱敏请求 ID（建议由 common.NewRequestId() 生成）；快照
	LogRequestID        string `gorm:"size:64;not null;default:'';index:idx_request_log_request"`                                                                                                  // 与底座 logs.request_id 对齐（**弱关联**：logs 可能跨库或 ClickHouse，不 JOIN、不事务）；快照
	UserID              int64  `gorm:"bigint;not null;default:0;index:idx_request_user_created,priority:1"`                                                                                        // 使用者
	ApiKeyID            int64  `gorm:"bigint;not null;default:0;index:idx_request_apikey_created,priority:1"`                                                                                      // 平台 Key
	TokenID             int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 影子令牌；快照
	CanonicalModelID    int64  `gorm:"bigint;not null;default:0;index:idx_request_model_created,priority:1"`                                                                                       // 规范模型（模型合并/别名调整不改写）；快照
	ApiModelID          string `gorm:"size:191;not null;default:''"`                                                                                                                               // 快照：当时的对外标准 ID
	RequestedModel      string `gorm:"size:191;not null;default:''"`                                                                                                                               // 客户端原始 model 字符串（不可信输入，存原文）；快照
	Endpoint            string `gorm:"size:128;not null;default:''"`                                                                                                                               // 请求路径
	Protocol            string `gorm:"size:32;not null;default:''"`                                                                                                                                // 客户端协议族
	Capability          int    `gorm:"type:int;not null"`                                                                                                                                          // 能力枚举（同 §5.7：1=对话 / 2=向量 / 3=图片生成 / 4=图片编辑 / 5=语音识别 / 6=语音生成 / 7=重排 / 8=视频）
	RouteMode           int    `gorm:"type:int;not null"`                                                                                                                                          // 路由模式：0=指定来源 / 1=延迟优先 / 2=吞吐优先 / 3=可靠性优先 / 4=综合自动；快照
	PinnedOfferingID    int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 客户端显式指定的供给；0=未指定；快照
	ResolvedScopeKind   int    `gorm:"type:int;not null"`                                                                                                                                          // 权威路由决定来源：0=请求头显式指定 / 1=Key 固定来源 / 2=Key 默认偏好 / 3=系统默认 auto；快照
	AllowFallback       bool   `gorm:"not null"`                                                                                                                                                   // 快照：本次是否允许回退（硬限制）
	AdmissionState      int    `gorm:"type:int;not null;index:idx_request_admit_cycle,priority:1"`                                                                                                 // 准入结果：0=已准入 / 1=容量不足拒绝 / 2=鉴权拒绝 / 3=参数冲突拒绝 / 4=预算拒绝
	AdmissionCycleKey   string `gorm:"size:16;not null;default:'';index:idx_request_admit_cycle,priority:2"`                                                                                       // 准入日（YYYY-MM-DD）；快照
	ClientIpHash        string `gorm:"type:char(64)"`                                                                                                                                              // 脱敏 IP 摘要（不存原始 IP）；快照
	IdempotencyRecordID int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 幂等记录；0=非幂等请求；快照
	AdmissionAt         int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 「提交准入完成」时刻，是「请求是否已接收」的边界；快照
	StartedAt           int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 网关开始处理时刻；仅单向推进
	FirstOutputAt       int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 首个有效输出时刻；仅单向推进
	CompletedAt         int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 完成时刻；仅单向推进
	QueueMs             int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 排队耗时（**计入用户总耗时**）；仅单向推进
	TotalMs             int64  `gorm:"bigint;not null;default:0"`                                                                                                                                  // 总耗时；仅单向推进
	AttemptCount        int    `gorm:"type:int;not null;default:0"`                                                                                                                                // 实际上游尝试次数（上游请求数按 Attempt 计）；仅单向推进
	FinalState          int    `gorm:"type:int;not null"`                                                                                                                                          // 终态：0=成功 / 1=失败 / 2=已取消 / 3=结果未知 / 4=部分成功；仅单向推进
	FailureClass        int    `gorm:"type:int;not null"`                                                                                                                                          // 请求级失败归因（把平台自身拒绝/用户参数错误/用户主动取消与上游故障分开）；仅单向推进
	CreatedAt           int64  `gorm:"bigint;not null;default:0;index:idx_request_user_created,priority:2;index:idx_request_apikey_created,priority:2;index:idx_request_model_created,priority:2"` // 创建时刻；快照
}

// TableName 返回 §5.19 定义的表名。
func (Request) TableName() string { return TablePrefix + "requests" }

// Attempt 对应 §5.20 `apigx_attempts`。
//
// 用途：每次上游尝试的渠道和 Key 策略、实际渠道及 Key ID、结果及耗时（计划书 §7 第 14 行后半）。
// 本表是「一次请求只有一个权威路由决定、实际 Key 同样由该调度流程决定，不能在原生
// `GetNextEnabledKey` 中再次抽选」这条不变量的落地证据。
//
// 唯一约束：`uniqueIndex(attempt_uid)` 与 `uniqueIndex(request_id, attempt_index)`（后者已覆盖
// 文档要求的 `index(request_id, attempt_index)`，不再重复建同名索引）。
// 不可变快照：**本表是全库最不可变的一张表**——`attempt_uid`、`request_id`、`attempt_index`、
// `selected_channel_id`、`base_channel_id`、`offering_id`、`upstream_key_id`、
// `upstream_key_credential_version`、`upstream_account_id`、`upstream_model_id`、`price_rule_id`、
// `fx_rate_micro`、`reservation_uid`、`retry_reason`、`started_at`、`created_at` 全部一次写入
// 永不修改：Key 轮换/停用/移除后**不得**把历史 Attempt 改绑到池里的下一把 Key；Key 重试成功
// 不得掩盖前一次失败；超时不得改判为按时成功。
type Attempt struct {
	ID                           int64  `gorm:"primaryKey"`                                                                                                                                                   // 主键
	AttemptUID                   string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_attempt_uid"`                                                                                                     // 尝试幂等 ID；快照
	RequestID                    int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_attempt_request_index,priority:1"`                                                                                  // 所属请求；快照
	AttemptIndex                 int    `gorm:"type:int;not null;default:0;uniqueIndex:uniq_attempt_request_index,priority:2"`                                                                                // 第几次上游尝试（第一版最多 2）；快照
	SelectedChannelID            int64  `gorm:"bigint;not null;default:0;index:idx_attempt_channel_started,priority:1"`                                                                                       // 权威路由决定的**实际渠道**（不是候选池）；快照
	BaseChannelID                int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 底座 channels.id；快照
	OfferingID                   int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 供给；快照
	UpstreamKeyID                int64  `gorm:"bigint;not null;default:0;index:idx_attempt_upkey_started,priority:1"`                                                                                         // **实际使用的 Key 稳定 ID**（不是池内下一把）；快照
	UpstreamKeyCredentialVersion int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 凭据版本快照（轮换后仍能指回「当时用的是哪一版」）
	UpstreamAccountID            int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 上游账户池快照（共享限额归属）
	UpstreamModelID              string `gorm:"size:191;not null;default:''"`                                                                                                                                 // 快照：实际发给上游的模型 ID
	PriceRuleID                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 快照：该次实际 Key 的价格版本（不借用池内最低价）
	FxRateMicro                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 汇率快照
	ReservationUID               string `gorm:"size:64;not null;default:''"`                                                                                                                                  // 容量预留的幂等引用；快照
	State                        int    `gorm:"type:int;not null;index:idx_attempt_state_started,priority:1"`                                                                                                 // 状态：0=待发送 / 1=已发送 / 2=成功 / 3=上游错误 / 4=超时 / 5=已取消 / 6=结果未知；仅单向推进
	FailureClass                 int    `gorm:"type:int;not null"`                                                                                                                                            // 失败归因：0=无 / 1=上游超时 / 2=连接错误 / 3=上游 5xx / 4=上游鉴权失败 / 5=上游不可用 / 6=上游限流 / 7=平台拒绝 / 8=用户参数错误 / 9=用户取消；仅单向推进
	RetryReason                  int    `gorm:"type:int;not null"`                                                                                                                                            // 重试原因：0=无 / 1=Key 鉴权失效 / 2=Key 冷却 / 3=渠道故障 / 4=容量不足（换 Key 与上游重试共用同一预算）；快照
	HttpStatus                   int    `gorm:"type:int;not null;default:0"`                                                                                                                                  // 上游 HTTP 状态（脱敏，不含响应体）；仅单向推进
	StartedAt                    int64  `gorm:"bigint;not null;default:0;index:idx_attempt_channel_started,priority:2;index:idx_attempt_upkey_started,priority:2;index:idx_attempt_state_started,priority:2"` // 发送时刻；快照
	FirstOutputAt                int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 首个有效输出时刻；仅单向推进
	CompletedAt                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 完成时刻；仅单向推进
	TtftMs                       int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 首个**有效**输出延迟（心跳/空事件不算，由适配器判定）；仅单向推进
	GenerationMs                 int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 生成时段耗时；仅单向推进
	OutputTokens                 int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 适配器可见的输出 token（不可见时置 0 并在 UsageEvent.trust 标注）；仅单向推进
	CreatedAt                    int64  `gorm:"bigint;not null;default:0"`                                                                                                                                    // 创建时刻；快照
}

// TableName 返回 §5.20 定义的表名。
func (Attempt) TableName() string { return TablePrefix + "attempts" }

// UsageEvent 对应 §5.21 `apigx_usage_events`。
//
// 用途：分项用量、可信度、成本估算状态、价格快照与归属（计划书 §7 第 15 行）。重复事件按稳定
// 事件 ID 幂等处理；补到准确 usage 时**追加校正事件**（`kind=2` + `correction_of_event_id`），
// 保留原估算与修订轨迹，不覆盖历史证据、不重复扣预算。
//
// 唯一约束：`uniqueIndex(event_uid)`。
// 不可变快照：**整行不可变**（除 `id` 外全部一次写入）；成本、价格、汇率、归属、用量与可信度列
// 绝不回写。
//
// 两条硬约束（§8.4/§8.5）：① `cost_state=2`（未定价）的行 `cost_usd_micro` 必须为 0，并计入
// 「未估算数量」，统计页的「可估算总额」**不得**把这些行当 0 加进总额；② 流式中间 usage 与最终
// 累计 usage **不相加**，由适配器区分后只落一条最终事件。
type UsageEvent struct {
	ID                  int64  `gorm:"primaryKey"`                                                                                                                                                                                                                                 // 主键
	EventUID            string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_usage_event_uid"`                                                                                                                                                                               // 稳定事件 ID，幂等去重；快照
	Kind                int    `gorm:"type:int;not null"`                                                                                                                                                                                                                          // 事件类型：0=用量 / 1=调整 / 2=校正（校正指向原事件，不覆盖）；快照
	CorrectionOfEventID int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 被修订的原事件；0=非校正，形成修订轨迹；快照
	RequestID           int64  `gorm:"bigint;not null;default:0;index:idx_usage_request"`                                                                                                                                                                                          // 同步请求来源；0=非同步请求来源；快照
	AttemptID           int64  `gorm:"bigint;not null;default:0;index:idx_usage_attempt"`                                                                                                                                                                                          // 上游尝试；0=请求级或任务级；快照
	AsyncTaskID         int64  `gorm:"bigint;not null;default:0;index:idx_usage_async_task"`                                                                                                                                                                                       // 异步任务来源；0=非任务；快照
	UserID              int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 使用者（归属）；快照
	OwnerUserID         int64  `gorm:"bigint;not null;default:0;index:idx_usage_owner_occurred,priority:1"`                                                                                                                                                                        // 分享者（分享者消耗视图的归属依据）；快照
	ApiKeyID            int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 平台 Key；快照
	CanonicalModelID    int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 规范模型；快照
	ApiModelID          string `gorm:"size:191;not null;default:''"`                                                                                                                                                                                                               // 快照：当时的对外标准 ID
	OfferingID          int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 供给；快照
	ChannelID           int64  `gorm:"bigint;not null;default:0;index:idx_usage_channel_occurred,priority:1"`                                                                                                                                                                      // 实际执行渠道；快照
	UpstreamKeyID       int64  `gorm:"bigint;not null;default:0;index:idx_usage_upkey_occurred,priority:1"`                                                                                                                                                                        // 实际执行 Key；快照
	UpstreamAccountID   int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 实际执行上游账户池；快照
	Capability          int    `gorm:"type:int;not null;default:0;index:idx_usage_cap_occurred,priority:1"`                                                                                                                                                                        // 能力枚举：0=未知 / 1=对话 / 2=向量 / 3=图片生成 / 4=图片编辑 / 5=语音识别 / 6=语音生成 / 7=重排 / 8=视频；快照；旧事件升级时安全回填为未知
	Dimension           int    `gorm:"type:int;not null;index:idx_usage_cycle_dim,priority:2"`                                                                                                                                                                                     // 计量维度（同 §5.16）；快照
	QuantityMilli       int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 用量，**千分之一最小单位**（1 token / 1 张 / 1 秒 / 1 次 = 1000，用整数表达小数秒）；快照
	UnitScale           int    `gorm:"type:int;not null"`                                                                                                                                                                                                                          // 该维度的计量单位（同 §5.16）；快照
	Trust               int    `gorm:"type:int;not null"`                                                                                                                                                                                                                          // 可信度：0=上游上报 / 1=本地估算 / 2=未知 / 3=不可得（不伪装为实际计量）；快照
	PriceRuleID         int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 价格版本快照引用；0=未定价
	PriceRuleItemID     int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 价格分项快照引用；0=未定价
	UnitPriceMicro      int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 快照：该分项单价（原币种）
	Currency            string `gorm:"type:char(3);not null"`                                                                                                                                                                                                                      // 快照：原币种
	FxRateMicro         int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 快照：换算时使用的汇率
	CostMicro           int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 快照：原币种成本
	CostUsdMicro        int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 快照：USD 成本
	CostState           int    `gorm:"type:int;not null;index:idx_usage_cost_occurred,priority:1"`                                                                                                                                                                                 // 成本状态：0=参考成本已估 / 1=渠道成本已估 / 2=未定价 / 3=待换算（缺汇率） / 4=明确零价；快照
	CycleKey            string `gorm:"size:16;not null;default:'';index:idx_usage_cycle_dim,priority:1"`                                                                                                                                                                           // 结算归属周期（跨日结算回到原预留周期）；快照
	OccurredAt          int64  `gorm:"bigint;not null;default:0;index:idx_usage_owner_occurred,priority:2;index:idx_usage_channel_occurred,priority:2;index:idx_usage_upkey_occurred,priority:2;index:idx_usage_cost_occurred,priority:2;index:idx_usage_cap_occurred,priority:2"` // 事件时间（UTC epoch 秒）；快照
	CreatedAt           int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                                                  // 创建时刻；快照
}

// TableName 返回 §5.21 定义的表名。
func (UsageEvent) TableName() string { return TablePrefix + "usage_events" }

// QualityBucket 对应 §5.22 `apigx_quality_buckets`。
//
// 用途：供给、能力、地区或部署点、时间桶、样本量、延迟及吞吐分布（计划书 §7 第 16 行）。至少按
// 「模型供给 × 能力 × 部署点」区分，并保留任务规格或输入输出长度分桶；同时保存分钟桶，按所需精度
// 聚合并标记更新时间。**不引用底座 `perf_metrics`**（维度不匹配）。
//
// 唯一约束：`uniqueIndex(bucket_key_hash)`——`deploy_point` + `spec_bucket` 在 utf8mb4 下最坏合计
// 384 字节，叠加其余身份列会超 MySQL COMPACT 行格式的 767 字节索引前缀边界，用固定 64 字节摘要
// 完全绕开该风险（§3.5）。
// 不可变快照：桶身份列（`offering_id`、`capability`、`deploy_point`、`spec_bucket`、`window_kind`、
// `bucket_ts`、`aggregator_version`）全部不可变；计数/求和列只允许**原子累加**（仿底座
// `model/perf_metric.go` 的 `clause.OnConflict` + `gorm.Expr("col + ?")`），
// `reliability_lower_bound_ppm` 由重算过程覆盖。迟到结果进入其**事件时间**所在的桶，不改写其他桶。
type QualityBucket struct {
	ID                       int64  `gorm:"primaryKey"`                                                                                                                            // 代理主键（与其余表一致；不用 64 字符哈希做主键，避免 InnoDB 聚簇索引过宽）
	BucketKeyHash            string `gorm:"type:char(64);not null;default:'';uniqueIndex:uniq_qbucket_key_hash"`                                                                   // SHA-256(offering_id|capability|deploy_point|spec_bucket|window_kind|bucket_ts|aggregator_version)；桶自然键
	OfferingID               int64  `gorm:"bigint;not null;default:0;index:idx_qbucket_roll_window,priority:1"`                                                                    // 供给；桶身份
	Capability               int    `gorm:"type:int;not null;index:idx_qbucket_roll_window,priority:2"`                                                                            // 能力；桶身份
	DeployPoint              string `gorm:"size:64;not null;default:''"`                                                                                                           // 地区/部署点；无则为空串；桶身份
	SpecBucket               string `gorm:"size:32;not null;default:''"`                                                                                                           // 任务规格或输入/输出长度分桶键；空串=不适用；桶身份
	WindowKind               int    `gorm:"type:int;not null;index:idx_qbucket_roll_window,priority:3"`                                                                            // 窗口类型：0=分钟桶 / 1=小时桶（6 小时窗口由分钟桶聚合）；桶身份
	BucketTs                 int64  `gorm:"bigint;not null;default:0;index:idx_qbucket_roll_window,priority:4;index:idx_qbucket_bucket_ts;index:idx_qbucket_aggver_ts,priority:2"` // 桶起始时间（UTC epoch 秒）；桶身份
	AggregatorVersion        int64  `gorm:"bigint;not null;default:0;index:idx_qbucket_aggver_ts,priority:1"`                                                                      // 指标配置版本（Key 模式/可用 Key 集合/定价配置明显变化时新建版本）；桶身份
	NaturalRequestCount      int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 自然调用样本数（只允许原子累加）
	ProbeCount               int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 主动探测样本数（与自然调用分别统计）
	CompleteSuccessCount     int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 完整成功数（同步正确结束/流式完整结束/异步终态成功；HTTP 200 或「任务已创建」不算）
	TimeoutCount             int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 超时事件数（超时进入窗口，不因迟到结果改判）
	UpstreamFailureCount     int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 上游故障数
	CancelledCount           int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 用户取消（**单列**）
	PlatformRejectCount      int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 平台自身拒绝（**单列**）
	LatencyMsSum             int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 总耗时分布：求和
	LatencyMsP50             int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 总耗时分布：P50（分位数由写入侧 Go 用环形缓冲预计算后写入）
	TtftMsSum                int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 首字延迟分布：求和
	TtftMsP50                int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 首字延迟分布：P50（同上，写入侧预计算）
	OutputTokensSum          int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 文本吞吐分子：有效输出 token 求和
	GenerationMsSum          int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 文本吞吐分母：生成时段耗时求和
	SampleOkCount            int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 合格尝试总数（可靠性分母）
	ReliabilityLowerBoundPpm int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 可靠性置信下界（百万分之一）；样本不足时置 0 并由自然样本数判定「样本不足」
	UpdatedAt                int64  `gorm:"bigint;not null;default:0"`                                                                                                             // 标记更新时间
}

// TableName 返回 §5.22 定义的表名。
func (QualityBucket) TableName() string { return TablePrefix + "quality_buckets" }

// BreakerProbeState persists the recovery state for one model offering after
// its base channel has been removed from routing by the APIGX circuit breaker.
// The state lives in the database so process restarts and multiple master
// instances cannot lose or duplicate recovery work.
type BreakerProbeState struct {
	ID                 int64  `gorm:"primaryKey"`
	OfferingID         int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_breaker_probe_offering"`
	BaseChannelID      int64  `gorm:"bigint;not null;default:0;index:idx_breaker_probe_channel_state,priority:1"`
	State              int    `gorm:"type:int;not null;index:idx_breaker_probe_due,priority:1;index:idx_breaker_probe_channel_state,priority:2"`
	ConsecutiveSuccess int    `gorm:"type:int;not null;default:0"`
	BackoffSeconds     int64  `gorm:"bigint;not null;default:0"`
	OpenedAt           int64  `gorm:"bigint;not null;default:0"`
	NextProbeAt        int64  `gorm:"bigint;not null;default:0;index:idx_breaker_probe_due,priority:2"`
	LastProbeAt        int64  `gorm:"bigint;not null;default:0"`
	RecoveryBaselineTs int64  `gorm:"bigint;not null;default:0"`
	LastErrorCode      string `gorm:"size:64;not null;default:''"`
	OwnsChannelDisable bool   `gorm:"not null"`
	CreatedAt          int64  `gorm:"bigint;not null;default:0"`
	UpdatedAt          int64  `gorm:"bigint;not null;default:0"`
}

func (BreakerProbeState) TableName() string { return TablePrefix + "breaker_probe_states" }

// AsyncTask 对应 §5.23 `apigx_async_tasks`。
//
// 用途：平台任务 ID、上游任务 ID、用户、原渠道与 Key/凭据版本、分享者与权限版本快照、价格版本、
// 状态、结果引用（计划书 §7 第 17 行）。这些快照保证轮换/撤权/封禁后**仍能定位原任务**：查询/取消/
// 下载不参与普通轮询、不随机换 Key、不跨用户读取上游任务。
//
// 唯一约束：`uniqueIndex(public_task_id)` 与 `uniqueIndex(base_task_id)`。
// 不可变快照：`public_task_id`、`base_task_id`、`upstream_task_id`、`user_id`、`channel_id`、
// `upstream_key_id`、`upstream_key_credential_version`、`upstream_account_id`、`share_group_id`、
// `owner_user_id`、`grant_version`、`policy_version`、`price_rule_id`、`fx_rate_micro`、`action`、
// `submit_at`。仅允许单向推进的是状态/时间/结算/结果引用列。
type AsyncTask struct {
	ID                           int64  `gorm:"primaryKey"`                                                                                                                                                                                                   // 主键
	PublicTaskID                 string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_asynctask_public_id"`                                                                                                                                             // 平台任务 ID（对外）；快照
	BaseTaskID                   int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_asynctask_base_id"`                                                                                                                                                 // 底座 tasks.id（影子任务，复用底座轮询与结算链路）；快照
	UpstreamTaskID               string `gorm:"size:191;not null;default:''"`                                                                                                                                                                                 // 上游任务 ID（**私有，不出公共 DTO**，不得用于跨用户访问）；快照
	UserID                       int64  `gorm:"bigint;not null;default:0;index:idx_asynctask_user_status_created,priority:1"`                                                                                                                                 // 任务所有者（历史访问授权来自创建时的所有权）；快照
	ApiKeyID                     int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 平台 Key；快照
	BaseTokenID                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 底座 tokens.id；仅用于恢复该请求的容量维度；快照
	CanonicalModelID             int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 规范模型；任务提交时冻结
	ApiModelID                   string `gorm:"size:191;not null;default:''"`                                                                                                                                                                                 // 客户端调用时的 API 模型 ID；任务提交时冻结
	OfferingID                   int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 原供给；快照
	ChannelID                    int64  `gorm:"bigint;not null;default:0;index:idx_asynctask_channel_status,priority:1"`                                                                                                                                      // 原渠道快照（后续查询/取消/下载不参与普通轮询）
	UpstreamKeyID                int64  `gorm:"bigint;not null;default:0;index:idx_asynctask_upkey_status,priority:1"`                                                                                                                                        // 原 Key 快照（轮换/停用/移除后不改绑）；快照
	UpstreamKeyCredentialVersion int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 凭据版本快照（轮换后原版本只为绑定任务保留必要的查询/取消能力）
	UpstreamAccountID            int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 上游账户池快照；快照
	ShareGroupID                 int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 分享分组快照；快照
	OwnerUserID                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 分享者快照；快照
	GrantVersion                 int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 权限版本快照；快照
	PolicyVersion                int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 容量策略版本快照；快照
	PriceRuleID                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 价格版本快照；快照
	FxRateMicro                  int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 汇率快照；快照
	CapacityPolicyID             int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 容量策略
	ReservationUID               string `gorm:"size:64;not null;default:''"`                                                                                                                                                                                  // 运行任务槽位预留引用（提交成功后持续占用到终态）
	ReservationReleaseState      int    `gorm:"type:int;not null;default:0;index:idx_asynctask_release_state"`                                                                                                                                                // 0=持有 / 1=待幂等释放 / 2=已释放
	ReservationReleasedAt        int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 容量租约确认释放时刻
	Action                       string `gorm:"size:40;not null;default:''"`                                                                                                                                                                                  // 任务类型（对齐底座 tasks.action）；快照
	Capability                   int    `gorm:"type:int;not null"`                                                                                                                                                                                            // 能力枚举
	Status                       int    `gorm:"type:int;not null;index:idx_asynctask_user_status_created,priority:2;index:idx_asynctask_status_timeout,priority:1;index:idx_asynctask_channel_status,priority:2;index:idx_asynctask_upkey_status,priority:2"` // 状态：0=待提交 / 1=运行中 / 2=成功 / 3=失败 / 4=已取消 / 5=结果未知 / 6=对账中；仅单向推进
	StatusReason                 string `gorm:"size:128;not null;default:''"`                                                                                                                                                                                 // 脱敏原因码；仅单向推进
	ClientDisconnected           bool   `gorm:"not null"`                                                                                                                                                                                                     // 客户端断连（**提交响应结束 ≠ 释放槽位**）；仅单向推进
	SubmitAt                     int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 提交时刻；快照
	StartAt                      int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 上游开始时刻；仅单向推进
	FinishAt                     int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 终态时刻；仅单向推进
	TimeoutAt                    int64  `gorm:"bigint;not null;default:0;index:idx_asynctask_status_timeout,priority:2"`                                                                                                                                      // 超时线（用于质量窗口：超过适配器服务时限且结果仍未知的尝试以超时事件进入窗口）；仅单向推进
	BillingSettled               bool   `gorm:"not null"`                                                                                                                                                                                                     // 是否已形成一份任务用量结算（提交/轮询/终态只能形成一份）；仅单向推进
	SettledUsageEventID          int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 结算指向的 usage_event；billing_settled=1 时非 0；仅单向推进
	IdempotencyRecordID          int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 幂等创建记录
	PollFailures                 int    `gorm:"type:int;not null;default:0"`                                                                                                                                                                                  // 连续无法识别的轮询结果计数；仅单向累加
	CreatedAt                    int64  `gorm:"bigint;not null;default:0;index:idx_asynctask_user_status_created,priority:3"`                                                                                                                                 // 创建时刻
	UpdatedAt                    int64  `gorm:"bigint;not null;default:0"`                                                                                                                                                                                    // 更新时刻
}

// TableName 返回 §5.23 定义的表名。
func (AsyncTask) TableName() string { return TablePrefix + "async_tasks" }

// UpstreamObjectRef 对应 §5.24 `apigx_upstream_object_refs`。
//
// 用途：用户、平台对象 ID、上游对象 ID、对象类型、原渠道/Key/账户、访问期限；用于文件、会话、
// 缓存和任务产物（计划书 §7 第 18 行）。客户端只用 `ref_uid`，**永不用上游 ID**。
//
// 唯一约束：`uniqueIndex(ref_uid)` 与 `uniqueIndex(channel_id, object_type, upstream_object_id)`——
// 同一渠道内同类型上游对象只登记一条，防止跨用户重复登记导致归属歧义。
// 不可变快照：`ref_uid`、`user_id`、`object_type`、`upstream_object_id`、`channel_id`、
// `upstream_key_id`、`upstream_key_credential_version`、`upstream_account_id`、`created_at`；
// 凭据轮换或移除后**不得**改绑 `upstream_key_id`。软删除：无，用 `state`（含 2=已清理）表达
// 生命周期，保留「曾经存在过什么」的证据。
type UpstreamObjectRef struct {
	ID                           int64  `gorm:"primaryKey"`                                                                                                                                      // 主键
	RefUID                       string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_objref_ref_uid"`                                                                                     // 对外平台对象 ID；快照
	UserID                       int64  `gorm:"bigint;not null;default:0;index:idx_objref_user_type_state,priority:1"`                                                                           // 所有者（**访问校验的唯一依据**）；快照
	ObjectType                   int    `gorm:"type:int;not null;uniqueIndex:uniq_objref_channel_object,priority:2;index:idx_objref_user_type_state,priority:2"`                                 // 对象类型：0=文件 / 1=会话 / 2=缓存 / 3=任务产物；快照
	UpstreamObjectID             string `gorm:"size:191;not null;default:'';uniqueIndex:uniq_objref_channel_object,priority:3"`                                                                  // 上游对象 ID（**私有，不出公共 DTO**）；快照
	ChannelID                    int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_objref_channel_object,priority:1"`                                                                     // 原渠道快照；快照
	UpstreamKeyID                int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 原 Key 快照；快照
	UpstreamKeyCredentialVersion int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 凭据版本快照；快照
	UpstreamAccountID            int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 上游账户池快照；快照
	OfferingID                   int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 供给；0=不属于特定供给
	StorageBackend               int    `gorm:"type:int;not null"`                                                                                                                               // 存储后端：0=仅上游 / 1=平台私有对象存储
	StorageURI                   string `gorm:"size:512;not null;default:''"`                                                                                                                    // 平台私有存储路径（**不含任何凭据**）
	AccessPolicy                 int    `gorm:"type:int;not null"`                                                                                                                               // 访问策略：0=仅所有者鉴权访问 / 1=允许签名下载；仅单向推进
	SignedUrlTtlSeconds          int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 签名下载有效期上限（默认 ≤ 600 秒且可撤销）；仅单向推进
	SizeBytes                    int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 对象字节数
	MimeType                     string `gorm:"size:128;not null;default:''"`                                                                                                                    // MIME 类型（不可信输入，响应时按白名单处理）
	ContentSha256                string `gorm:"type:char(64)"`                                                                                                                                   // 内容摘要（完整性校验，不用于鉴权）
	State                        int    `gorm:"type:int;not null;index:idx_objref_user_type_state,priority:3;index:idx_objref_state_purge,priority:1;index:idx_objref_expires_state,priority:2"` // 状态：0=有效 / 1=已过期 / 2=已清理 / 3=已撤销；仅单向推进
	ExpiresAt                    int64  `gorm:"bigint;not null;default:0;index:idx_objref_expires_state,priority:1"`                                                                             // 访问期限；仅单向推进
	PurgeAfter                   int64  `gorm:"bigint;not null;default:0;index:idx_objref_state_purge,priority:2"`                                                                               // 清理时间（终态后默认 24 小时）；仅单向推进
	CreatedAt                    int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 创建时刻；快照
	UpdatedAt                    int64  `gorm:"bigint;not null;default:0"`                                                                                                                       // 更新时刻
}

// TableName 返回 §5.24 定义的表名。
func (UpstreamObjectRef) TableName() string { return TablePrefix + "upstream_object_refs" }

// AuditEvent 对应 §5.25 `apigx_audit_events`。
//
// 用途：分享配置、权限、价格、凭据轮换与管理员操作的**脱敏**记录（计划书 §7 第 19 行）。
//
// 唯一约束：`uniqueIndex(event_uid)`。
// 不可变快照：**整行不可变**。并且本表自身**绝不允许写入凭据**——不得含密码、验证码、恢复码、
// 私钥、可用会话或认证令牌、上游 Key 明文；`actor_credential_ref` 只存指纹。
type AuditEvent struct {
	ID                 int64  `gorm:"primaryKey"`                                                                                                                                                  // 主键
	EventUID           string `gorm:"size:64;not null;default:'';uniqueIndex:uniq_audit_event_uid"`                                                                                                // 事件幂等 ID；快照
	ActorUserID        int64  `gorm:"bigint;not null;default:0;index:idx_audit_actor_occurred,priority:1"`                                                                                         // 操作者；0=系统；快照
	ActorRole          int    `gorm:"type:int;not null"`                                                                                                                                           // 快照：事件发生时的角色（不随后续改角色而变）
	ActorApiKeyID      int64  `gorm:"bigint;not null;default:0"`                                                                                                                                   // 操作者 Key；0=非 Key 操作；快照
	ActorCredentialRef string `gorm:"type:char(64)"`                                                                                                                                               // 操作者凭据的**指纹**（绝不是凭据本身）；快照
	Category           string `gorm:"size:24;not null;default:'';index:idx_audit_category_occurred,priority:1"`                                                                                    // 类别：share_config / access / price / credential_rotation / admin / retention
	Action             string `gorm:"size:64;not null;default:''"`                                                                                                                                 // 动作码
	TargetType         string `gorm:"size:32;not null;default:'';index:idx_audit_target_occurred,priority:1"`                                                                                      // 目标类型
	TargetID           int64  `gorm:"bigint;not null;default:0;index:idx_audit_target_occurred,priority:2"`                                                                                        // 目标稳定 ID
	TargetPublicID     string `gorm:"size:64;not null;default:''"`                                                                                                                                 // 目标对外标识（不用名称，改名不影响可读性）
	BeforeDigest       string `gorm:"type:char(64)"`                                                                                                                                               // 变更前摘要（脱敏，可比较但不可逆）
	AfterDigest        string `gorm:"type:char(64)"`                                                                                                                                               // 变更后摘要（脱敏，可比较但不可逆）
	ChangeSummary      string `gorm:"type:text"`                                                                                                                                                   // 字段级变更摘要（脱敏；绝不含凭据/验证码/恢复码/私钥/令牌/上游 Key 明文）
	RequestID          string `gorm:"size:64;not null;default:''"`                                                                                                                                 // 关联请求（与底座 logs.request_id 及 apigx_requests.request_uid 对齐）
	IpHash             string `gorm:"type:char(64)"`                                                                                                                                               // 脱敏 IP 摘要
	UserAgent          string `gorm:"size:255;not null;default:''"`                                                                                                                                // 客户端 UA
	OccurredAt         int64  `gorm:"bigint;not null;default:0;index:idx_audit_actor_occurred,priority:2;index:idx_audit_target_occurred,priority:3;index:idx_audit_category_occurred,priority:2"` // 事件时间；快照
	CreatedAt          int64  `gorm:"bigint;not null;default:0"`                                                                                                                                   // 创建时刻；快照
}

// TableName 返回 §5.25 定义的表名。
func (AuditEvent) TableName() string { return TablePrefix + "audit_events" }

// IdempotencyRecord 对应 §5.26 `apigx_idempotency_records`。
//
// 用途：支持的创建端点以「用户 + 平台 Key 授权范围 + 幂等键 + 请求摘要」识别同一次创建，同键
// 不同参数返回冲突；请求摘要仅作校验，不保存明文内容（§9.5）。
//
// 唯一约束：`uniqueIndex(user_id, api_key_id, operation, idempotency_key_hash)`——并发下由数据库
// 唯一键保证只有一次创建成功，随后比对 `request_digest`。
// 不可变快照：`user_id`、`api_key_id`、`operation`、`idempotency_key_hash`、`request_digest`、
// `created_at`。边界：上游不支持幂等或结果无法查询时状态保留为「未知」（`state=3`），
// **不得**被自动重试改写为「已完成」。
type IdempotencyRecord struct {
	ID                 int64  `gorm:"primaryKey"`                                                             // 主键
	UserID             int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_idem_scope_key,priority:1"`   // 用户；快照
	ApiKeyID           int64  `gorm:"bigint;not null;default:0;uniqueIndex:uniq_idem_scope_key,priority:2"`   // 平台 Key；快照
	Operation          string `gorm:"size:32;not null;default:'';uniqueIndex:uniq_idem_scope_key,priority:3"` // 操作类型（chat / image / audio.speech / video.create / file.upload）；快照
	IdempotencyKeyHash string `gorm:"type:char(64);uniqueIndex:uniq_idem_scope_key,priority:4"`               // 客户端幂等键摘要（长字符串一律用摘要进唯一索引）；快照
	RequestDigest      string `gorm:"type:char(64)"`                                                          // 请求摘要（仅作校验，不保存明文内容）；快照
	State              int    `gorm:"type:int;not null;index:idx_idem_state_created,priority:1"`              // 状态：0=进行中 / 1=已完成 / 2=同键不同参数冲突 / 3=上游结果未知
	RequestID          int64  `gorm:"bigint;not null;default:0"`                                              // 生成的请求；0=未生成请求
	AsyncTaskID        int64  `gorm:"bigint;not null;default:0"`                                              // 关联异步任务；0=非任务
	ResponseRef        string `gorm:"size:255;not null;default:''"`                                           // 结果引用（**不含凭据**）
	CreatedAt          int64  `gorm:"bigint;not null;default:0;index:idx_idem_state_created,priority:2"`      // 创建时刻；快照
	CompletedAt        int64  `gorm:"bigint;not null;default:0"`                                              // 完成时刻；仅单向推进
}

// TableName 返回 §5.26 定义的表名。
func (IdempotencyRecord) TableName() string { return TablePrefix + "idempotency_records" }

// PlatformSetting 对应 §5.27 `apigx_platform_settings`。
//
// 用途：承载平台级可配置默认值（保留期天数、默认时区、签名下载 TTL 上限、冷启动流量比例、自动
// 路由权重、探测预算等）。**不写底座 `options`**，避免与上游已知键白名单加载语义耦合。
//
// 主键即唯一（`setting_key`），无额外索引（按主键点查）；无软删除，删除即移除键。
// 保留期、时区、权重等配置变更**只影响未来行为**，不得追溯改写历史计量、历史周期归属或历史质量桶。
type PlatformSetting struct {
	SettingKey      string `gorm:"size:128;primaryKey;autoIncrement:false"` // 键名（避开保留字 key）；主键即唯一，不自增
	SettingValue    string `gorm:"type:text"`                               // 值（JSON 或标量字符串）
	ValueVersion    int64  `gorm:"bigint;not null;default:0"`               // 值版本（配置变更用版本传播）
	UpdatedByUserID int64  `gorm:"bigint;not null;default:0"`               // 更新者；0=系统默认
	UpdatedAt       int64  `gorm:"bigint;not null;default:0"`               // 更新时刻
}

// TableName 返回 §5.27 定义的表名。
func (PlatformSetting) TableName() string { return TablePrefix + "platform_settings" }
