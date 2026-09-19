# APIGX 核心实体表结构草案（阶段 0）

对象：`docs/PROJECT_PLAN.md` §7「核心数据模型」的全部实体，落成可实现的表结构，并给出与底座 New API 现有表共存的迁移方案。

底座基线：`new-api/`，固定提交 `69a50029819a26c53e6babd276d49cfe2f8880ad`（依据 `docs/stage0/base-lock.md` §1）。

---

## 0. 声明：本草案的验证边界

> **本草案未在任何数据库上执行过。** 没有启动过 SQLite、MySQL 或 PostgreSQL 实例，没有运行过 GORM `AutoMigrate`，没有执行过任何 `CREATE TABLE` / `ALTER TABLE` / `CREATE INDEX`，没有跑过底座、没有跑过它的测试。
>
> 本文所有关于底座行为的结论，均来自**只读源码阅读**，每条都给出可打开的「文件:行号」。凡无法从源码确认的，一律写「**未核实**」，不做推断性断言（见 §9）。
>
> 表名、字段名、类型与索引**都是设计意图，不是已验证的 schema**。任何一条都必须按 `new-api/AGENTS.md` 的数据库条款在**真实 SQLite、MySQL、PostgreSQL 三套实例**上各跑一遍「全新库 + 从底座旧库升级 + 连续启动两次验幂等」才算通过，并把数据库版本、命令与结果记录下来。
>
> 本文件不修改 `new-api/` 下任何文件，也不在 `new-api/docs/` 下新建文件（遵守 `CODE_STYLE.md` 第 23 条）。

---

## 1. 结论摘要

### 1.1 覆盖范围

计划书 §7 列出的实体**一个不漏**，映射关系如下（27 张新表 = 22 张实体表 + 5 张派生表）：

| # | 计划书 §7 实体 | 落表 | 类型 |
|---|---|---|---|
| 1 | User | 复用底座 `users` + `apigx_user_profiles` | 实体表（1:1 扩展） |
| 2 | APIKey | `apigx_api_keys` + 每 Key 一行底座 `tokens`（影子令牌） | 实体表 |
| 3 | ShareGroup | `apigx_share_groups` | 实体表 |
| 4 | Channel | `apigx_channels` + 每渠道一行底座 `channels`（影子渠道） | 实体表 |
| 5 | UpstreamKey | `apigx_upstream_keys` | 实体表 |
| 6 | KeyModelCapability | `apigx_key_model_capabilities` | 实体表 |
| 7 | CanonicalModel | `apigx_canonical_models` | 实体表 |
| 8 | ModelAlias | `apigx_model_aliases` | 实体表 |
| 9 | ModelOffering | `apigx_model_offerings` | 实体表 |
| 10 | AccessPolicy | `apigx_access_policies` | 实体表 |
| 11 | Grant | `apigx_grants` | 实体表 |
| 12 | Invite | `apigx_invites` | 实体表 |
| 13 | PriceRule（价格版本） | `apigx_price_rules` + `apigx_price_rule_items` | 实体表 + 派生子表 |
| 14 | PriceSnapshot（汇率快照） | `apigx_fx_rates`（版本化汇率）+ 各消费行的 `fx_rate_micro` 冗余快照列 | 派生表 + 快照列 |
| 15 | CapacityPolicy | `apigx_capacity_policies` | 实体表 |
| 16 | Reservation | `apigx_reservations` | 实体表 |
| 17 | Request | `apigx_requests` | 实体表 |
| 18 | Attempt | `apigx_attempts` | 实体表 |
| 19 | UsageEvent | `apigx_usage_events` | 实体表（追加式） |
| 20 | QualityBucket | `apigx_quality_buckets` | 实体表（聚合桶） |
| 21 | AsyncTask | `apigx_async_tasks` + 每任务一行底座 `tasks` | 实体表 |
| 22 | UpstreamObjectRef | `apigx_upstream_object_refs` | 实体表 |
| 23 | AuditEvent | `apigx_audit_events` | 实体表 |
| — | （派生）上游账户池，服务 §9.2「同一上游账户的共享限额」 | `apigx_upstream_accounts` | 派生表 |
| — | （派生）幂等记录，服务 §9.5「同键不同参数返回冲突」 | `apigx_idempotency_records` | 派生表 |
| — | （派生）平台级设置，避免耦合底座 `options` 加载语义 | `apigx_platform_settings` | 派生表 |

**关于派生表的说明**：计划书 §7 的表格里没有单独列出这 5 张表，但 §9.2（上游共享账户池）、§9.5（幂等创建）、§8.5（版本化汇率与多分项定价）、§8.3 之后的平台配置需求，在 schema 层没有等价的既有载体。把它们显式落表而不是塞进 JSON 列，是为了让容量、成本、幂等这三类**必须被 SQL 聚合与条件更新**的数据可查、可约束、可审计。

### 1.2 三条最重要的结论

1. **三库兼容优先于「只支持 PostgreSQL」。** 计划书 §6 写「数据库优先 PostgreSQL」，底座 `new-api/AGENTS.md` 的数据库条款要求同时兼容 SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6（`docs/stage0/base-lock.md` §6、§7-1）。本草案一律按三库可移植设计：**不使用 `SERIAL` / `AUTO_INCREMENT` 字面量、不使用 PG 专用操作符、不使用 MySQL 专用函数、不使用部分索引（partial index）、不使用 `type:json` 列类型**。

2. **分享分组绝不进入底座现有分组字段。** `apigx_share_groups` 是唯一权威载体；`users.group`（`new-api/model/user.go:100`）、`tokens.group`（`new-api/model/token.go:29`）、`channels.group`（`new-api/model/channel.go:40`）保持底座原生语义，**不写入任何 APIGX 分享分组标识**（`CODE_STYLE.md` 第 9 条）。

3. **平台 API Key 的「只存摘要」需要在底座鉴权查找路径上加一个哈希分支。** 底座的令牌查找是「用客户端提交的明文 key 做等值查询」：`middleware/auth.go:302-306` 与 `401-405` 取 `Authorization` → 去 `sk-` 前缀 → **按 `-` 切分只取第一段** → `model.GetTokenByKey` → `new-api/model/token.go:290` 的 `DB.Where(commonKeyCol+" = ?", key)`。这条链路里没有任何哈希步骤，因此**仅靠新增表无法实现 §10.3 要求的「保存安全校验摘要而非可恢复明文」**。这是一个必须在写代码前显式决策的冲突点，方案见 §6.2。

---

## 2. 底座既有约定核查（每条均可按「文件:行号」打开核对）

### 2.1 三库兼容与方言开关

| 结论 | 证据 |
|---|---|
| 主库类型有明确枚举与判定函数 | `new-api/common/database.go:7`（`DatabaseTypeSQLite`）、`:12-19`（`mainDatabaseType` / `MainDatabaseType()`）、`:36`（`UsingMainDatabase`）、`:40`（`UsingLogDatabase`） |
| 方言相关的列名引用必须走共享变量 | `new-api/model/main.go:22-23` 声明 `commonGroupCol` / `commonKeyCol`；`:43-64` 的 `initCol()` 按主库/日志库类型分别赋 `` `group` `` / `"group"` 与 `` `key` `` / `"key"` |
| 布尔字面量同样有共享变量 | `new-api/model/main.go:24` 声明 `commonTrueVal` / `commonFalseVal`；`:48-49` PG 用 `"true"/"false"`，`:53-54` 其它用 `"1"/"0"` |
| 不用裸 SQL 写自增 | `new-api/AGENTS.md`「Let GORM handle primary key generation; do not use `AUTO_INCREMENT` or `SERIAL` directly」；底座自身 `Task.ID` 上的 `AUTO_INCREMENT` tag（`new-api/model/task.go:51`）是上游既有代码，不是我们可照抄的新写法 |
| 行锁必须走共享 helper | `new-api/model/locking.go:20-25`：`lockForUpdate(tx)` 在 SQLite 直接返回原 `tx`（SQLite 无 `FOR UPDATE` 语法），其它方言加 `clause.Locking{Strength:"UPDATE"}`。`:12-15` 的注释明确说明 GORM v2 会**静默忽略** v1 的 `Set("gorm:query_option","FOR UPDATE")` |
| SQLite 不支持 `ALTER COLUMN` | `new-api/model/main.go:581-582`、`:635-638` 两处显式早退；`:571` 用 `ALTER TABLE ... ADD COLUMN` 作为 SQLite 的加列路径 |
| 索引前缀长度约束有上游先例 | `new-api/model/task.go:54` 用 `type:varchar(191)` 承载被索引的上游任务 ID |
| 固定长度摘要列有上游先例 | `new-api/model/user.go:95` 的 `type:char(32)`；`new-api/model/redemption.go:17` 的 `type:char(32)`；PG 会把 `CHAR(n)` 报告为 `bpchar`，上游为此写了专用 normalize：`new-api/model/migration_dialector.go:61-68` |

### 2.2 布尔字段与 `default:true`

| 结论 | 证据 |
|---|---|
| 底座不用 `gorm:"default:true"`，布尔字段普遍是**无 default tag 的裸 `bool`** | `new-api/model/token.go:24-25`（`UnlimitedQuota` / `ModelLimitsEnabled`）、`new-api/model/log.go:72`（`IsStream`）、`new-api/model/ability.go:22`（`Enabled`） |
| 原因是三库的布尔默认值归一化不一致，会导致 `AutoMigrate` 每次启动重复 `ALTER TABLE` | `new-api/AGENTS.md`「Avoid GORM boolean default tags such as `gorm:"default:true"` when the default is a business rule already enforced by code … do not replace `default:true` with `default:1` unless the behavior is verified across SQLite, MySQL, and PostgreSQL」 |
| 上游确实会为「默认值比较」写方言补丁 | `new-api/model/migration_dialector.go:24-45`：MySQL 的 `decimal` 默认值会补零（`0` → `0.000000`），上游为此跳过等价默认值比较 |

**本草案的取值**：所有布尔字段用裸 `bool` + `gorm:"not null"`，**不带任何 default tag**，由 Go 侧构造/归一化显式赋值。

### 2.3 迁移必须与 `AutoMigrate` 共存

| 结论 | 证据 |
|---|---|
| 主库迁移入口是 `migrateDB()`，迁移失败会**致命** | `new-api/model/main.go:320-393`；`new-api/main.go:314-318`：`model.InitDB()` 返回 error 时调用 `common.FatalLog` |
| 迁移**只在主节点**执行 | `new-api/model/main.go:215-217`（`if !common.IsMasterNode { return nil }`）；`new-api/common/init.go:89`（`IsMasterNode = os.Getenv("NODE_TYPE") != "slave"`） |
| 表由 `DB.AutoMigrate(...)` 批量声明 | `new-api/model/main.go:337-373`（列出 `Channel/Token/User/.../AuthzRole`） |
| 日志库是**独立连接**，可指向独立数据库甚至 ClickHouse | `new-api/model/main.go:230-272`（`InitLogDB`）；`:395-410`（`migrateLOGDB` / `migrateClickHouseLogDB`） |
| 上游已有「迁移前先做前置校验，不满足就拒绝启动」的模式 | `new-api/model/main.go:279-306`（`ensureUserQuotaColumns`：32 位钱包 schema 直接报错） |
| 上游已有「先探测 schema，已是目标形态则直接返回」的幂等模式 | `new-api/model/main.go:580-629`（`migrateTokenModelLimitsToText`）、`:633-689`（`migrateSubscriptionPlanPriceAmount`）、`new-api/model/option_primary_key_migration.go:20-44` |
| 上游已有「条件更新 + `RowsAffected` 判定」的原子扣减模式 | `new-api/model/quota_reserve.go:144-149`（`reserveUserQuotaDB`）与 `:151-160`（`reserveTokenQuotaDB`） |
| 上游已有「唯一键冲突即累加」的聚合桶 upsert 模式 | `new-api/model/perf_metric.go:33-48`（`clause.OnConflict` + `gorm.Expr("... + ?")`）、`new-api/model/system_instance.go:61-69` |
| 上游已有 CAS 式状态推进（`task_cas_test.go` 覆盖） | `new-api/model/task_cas_test.go`（文件名层面的存在证据；具体 CAS 实现未逐行核对） |

**对我们的直接约束**：

- 新表迁移必须挂在 `migrateDB()` 的 `DB.AutoMigrate(...)` 之后，**只在主节点执行**，失败即让启动失败（与上游一致，不静默跳过）。
- 我们的表**只放主库 `DB`，绝不放 `LOG_DB`**：`LOG_DB` 可能是独立数据库或 ClickHouse（`model/main.go:395-410`），而 ClickHouse 分支不支持 `AutoMigrate` 的全部语义，且我们的 `apigx_usage_events` 需要与 `apigx_reservations` / `apigx_attempts` 保持事务一致。
- JSON 列**不写 `type:json`**（底座 `channels.channel_info` 用了 `type:json`，见 `new-api/model/channel.go:54`；但 `new-api/AGENTS.md` 要求非方言特性的 JSON 列要有 `TEXT` 回退）。本草案统一 `type:text` + `common.Marshal/Unmarshal`（`new-api/common/json.go:42/46/50/66`）。

### 2.4 鉴权与 Key 查找路径的三个硬约束（直接决定 `apigx_api_keys` 的设计）

| 约束 | 证据 | 对我们的影响 |
|---|---|---|
| 客户端 key 会被 `strings.TrimPrefix(key, "sk-")`，然后**按 `-` 切分并只取第一段** | `new-api/middleware/auth.go:302-304`；第二处同样逻辑在 `:401-403` | 平台 Key 的**明文与摘要都必须不含 `-`**。SHA-256 十六进制满足；UUID / URL-safe Base64 **不满足**，会被截断成第一段导致查不到 |
| 底座的令牌查找是明文等值查询，无哈希步骤 | `new-api/model/token.go:280-301`（`GetTokenByKey`，`:290` 的 `DB.Where(commonKeyCol+" = ?", key)`）；`new-api/model/token.go:220-229`（`ValidateUserToken`） | 「只存摘要」必须改这条查找路径，见 §6.2 |
| 令牌 key 由底座自行生成，字符集为 `[0-9a-zA-Z]`（**不含 `-`**） | `new-api/common/utils.go:229`（`keyChars`）、`:231-244`（`GenerateRandomCharsKey`）、`:254-257`（`GenerateKey` = 48 字符）；调用点 `new-api/controller/token.go:326` | 平台 Key 生成器必须复用同一字符集约束 |
| `tokens.key` 的 DB 列宽是 `varchar(128)` 且有唯一索引 | `new-api/model/token.go:17` | 64 位十六进制摘要可以放进去；但这是**列宽**可行，不等于**查找路径**可行（见上一条） |
| key 后缀 `-<channelId>` 是底座原生「指定渠道」入口，且**只对管理员开放**，普通用户返回 403 | `new-api/middleware/auth.go:536-554`（`if len(parts) > 1 { if model.IsAdmin(token.UserId) { ... } else { abortWithOpenAiMessage(403, "普通用户不支持指定渠道") } }`）；`new-api/middleware/distributor.go:48-78`（pin 生效后的渠道固定与校验） | APIGX 的「指定来源」**不能**复用 token 后缀机制，必须走平台自己的请求头 + `apigx_api_keys.pinned_offering_id`／`scope_mode`，并在服务端校验（计划书 §9.1） |

### 2.5 与底座现有表名不冲突

底座表名由 GORM 默认命名策略生成（`PerfMetric` 显式覆写为 `perf_metrics`，`new-api/model/perf_metric.go:25-27`）。可从代码中直接核对的例子：`abilities`（`new-api/model/ability.go:35` 的 `DB.Table("abilities")`）、`logs`（`new-api/model/main.go:438` 的 ClickHouse DDL）、`tokens`（`new-api/model/main.go:586`）、`options`（`new-api/model/option_primary_key_migration.go:73` 的 `to_regclass('options')`）、`audit_logs`（`new-api/model/audit_log.go:247`）、`subscription_plans`（`new-api/model/main.go:503`）。

**不依赖逐表枚举的证明**：本草案全部 27 张表名均以 `apigx_` 开头，底座表名无一以 `apigx_` 开头，因此命名空间不相交。表名一律小写 + 下划线（MySQL 在 Linux 下表名大小写敏感）。

### 2.6 底座已有、我们**不复用**的两张近邻表

| 底座表 | 证据 | 为什么另建 |
|---|---|---|
| `audit_logs` | 结构 `new-api/model/audit_log.go:26-46`；建表 `:243-255`（非 ClickHouse 走 `LOG_DB.AutoMigrate`，ClickHouse 走手写 DDL） | 它在**日志库**，可能是独立库或 ClickHouse；且注释明确「retained independently of usage logs and their cleanup/TTL policy」。APIGX 的审计事件需要与 `apigx_grants` / `apigx_price_rules` 在同一主库内做事务一致性校验，故另建 `apigx_audit_events`（见 §5.23） |
| `perf_metrics` | 结构 `new-api/model/perf_metric.go:11-23`；唯一键 `(model_name, group, bucket_ts)` | 它的维度是「模型名 × 底座分组 × 桶」，无法承载计划书 §9.4 要求的「模型供给 × 能力 × 部署点 × 规格分桶 × 聚合器版本」，也不含超时/取消/平台拒绝的分项与探测样本区分。另建 `apigx_quality_buckets`，不修改上游表 |

---

## 3. 全局字段与类型规约（三库可移植）

### 3.1 命名规约

| 项 | 规约 | 理由 |
|---|---|---|
| 表名前缀 | 全部 `apigx_` | 与底座命名空间不相交（§2.5） |
| 列名 | 小写 + 下划线；**枚举/标识列带语义后缀**（`_state` / `_mode` / `_kind` / `_type`） | 三库大小写处理不一致，全小写最安全 |
| **禁止裸用保留字列名** | 不用 `group`、`key`、`order`、`user`、`desc`、`usage` | 底座为 `group` / `key` 维护了 `commonGroupCol` / `commonKeyCol`（`new-api/model/main.go:22-23`）。我们的表从命名上绕开，就没有方言引号问题 |
| 索引名 | **每一条索引都显式命名**，长度 ≤ 40 字符 | MySQL 标识符上限 64 字符；GORM 自动拼接的长表名 + 复合列名有越界风险 |
| 时间列 | 一律 `_at`，存 **UTC epoch 秒**（`int64`） | 底座用 `common.GetTimestamp()`（`new-api/common/utils.go:264-266`）；计划书 §8.5「数据库时间戳统一 UTC」 |
| 快照列 | 需要「历史上长什么样」的列，一律**不加外键语义、不加级联**，只存值 | 计划书 §7「展示名、价格或 owner 变化不回写历史快照」 |

### 3.2 类型别名表（本文件所有表的字段类型都用这里的别名书写）

> 「GORM 期望生成」列是**设计意图**，不是运行结果。GORM 版本 `gorm.io/gorm v1.25.12`（`docs/stage0/base-lock.md` §4.1）。**未核实**：GORM 在三库上对下列 tag 生成的确切 DDL。

| 别名 | Go 类型 | GORM tag | SQLite | MySQL ≥ 5.7.8 | PostgreSQL ≥ 9.6 | 说明 |
|---|---|---|---|---|---|---|
| `ID` | `int64` | `gorm:"primaryKey"` | `integer`（rowid 别名，自增） | `bigint` + 自增（GORM 生成） | `bigserial`（GORM 生成） | **绝不手写 `AUTO_INCREMENT` / `SERIAL`** |
| `FK` | `int64` | `gorm:"bigint;not null;default:0"` | `integer` | `bigint` | `bigint` | 指向其它表的 `ID`；**不建数据库级外键约束**（见 §6.1） |
| `BIGINT` | `int64` | `gorm:"bigint;not null;default:0"` | `integer` | `bigint` | `bigint` | 计数、求和 |
| `INT` | `int` | `gorm:"type:int;not null;default:0"` | `int` | `int` | **`bigint`（实测）** | 小整数；与底座 `User.Role`（`new-api/model/user.go:87` 的 `type:int;default:1`）同风格。**勘误（2026-09-16）**：GORM 在 PostgreSQL 上把 Go `int` 映射为 `bigint`（Go `int` 为 64 位），`type:int` 也一样；**底座自身**的 `channels.status/type/weight` 实测同样是 `bigint`。因此 PG 上 `INT` 与 `BIGINT` 无区别，本项目接受该行为（要真正 4 字节只能写方言专有类型，会破坏三库可移植）。详见 `docs/stage1/schema-impl-notes.md` §2 |
| `ENUM` | `int` | `gorm:"type:int;not null"`（**无 default**） | `int` | `int` | `int` | 状态/模式枚举；由 Go 侧常量显式赋值 |
| `BOOL` | `bool` | `gorm:"not null"`（**无 default**） | `numeric` | `boolean` | `boolean` | 见 §2.2；需要「四态」时改用 `ENUM` |
| `MICRO` | `int64` | `gorm:"bigint;not null;default:0"` | `integer` | `bigint` | `bigint` | **金额最小单位**：1 unit = 1e-6 USD（micro-USD）；汇率同理（1e-6 倍） |
| `TS` | `int64` | `gorm:"bigint;not null;default:0"` | `integer` | `bigint` | `bigint` | UTC epoch 秒；`0` 表示「未设置」 |
| `STR(n)` | `string` | `gorm:"size:n;not null;default:''"` | `text` | `varchar(n)` | `varchar(n)` | 短字符串（`n ≤ 191`） |
| `STRN(n)` | `string` | `gorm:"size:n"`（可空） | `text` | `varchar(n)` | `varchar(n)` | 可空短字符串 |
| `TEXT` | `string` | `gorm:"type:text"` | `text` | `text`（≤ 65535 字节） | `text` | 长文本 / JSON 载荷 |
| `HASH64` | `string` | `gorm:"type:char(64)"` | `text` | `char(64)` | `char(64)` | SHA-256 十六进制；**保证无 `-`**（§2.4）。PG 侧会被报告为 `bpchar`，上游已有 normalize（`new-api/model/migration_dialector.go:61-68`） |
| `CUR3` | `string` | `gorm:"type:char(3);not null"` | `text` | `char(3)` | `char(3)` | ISO 4217 币种，如 `USD` / `CNY` |
| `SOFTDEL` | `gorm.DeletedAt` | `gorm:"index"` | `datetime` | `datetime(3)` | `timestamptz` | 与底座一致（`new-api/model/user.go:106`、`model/token.go:32`、`model/redemption.go:25`、`model/model_meta.go:48`、`model/prefill_group.go:86`、`model/vendor_meta.go:25`） |

**金额为什么用 `MICRO` 而不是 `decimal`**：底座只在 `subscription_plans.price_amount` 用 `type:decimal(10,6)`，且 Go 侧类型是 `float64`（`new-api/model/subscription.go:153`），另有专门的迁移函数处理 MySQL 默认值补零（`new-api/model/migration_dialector.go:24-45`）。`shopspring/decimal` 在 SQLite（`glebarez/sqlite` 纯 Go 驱动）上的 `Scanner`/`Valuer` 行为**未核实**。计划书 §8.5 允许「定点小数**或**整数最小计量单位」，因此选 `int64` micro 单位：三库行为完全一致，无驱动差异风险，且换算可用 `common.QuotaFromDecimal` / `*Checked` 系列（`new-api/common/quota_math.go:112/119/133/140/152/159/172`）收口。

- `MICRO` 上限：`9.22e18 / 1e6 ≈ 9.2e12` USD，远高于任何真实额度。
- 单价精度：token 单价以 **每 1M tokens** 表达（计划书 §8.5），$0.15/1M = `150000` micro-USD，精度充裕。
- 小数秒：视频/音频按秒计价时用 `quantity_milli`（1 秒 = 1000），见 §5.20。

### 3.3 「限制维度」的四态枚举（`CODE_STYLE.md` 第 11 条）

计划书 §9.2 要求「未配置 / 禁用 / 继承默认 / 明确不限」分别存储，空字段不得解释为无限容量。因此**所有容量维度都是「模式列 + 值列」成对出现**：

| `*_mode` 取值 | 含义 | 值列取值 |
|---|---|---|
| `0` | 未配置（未表态） | 必须为 `0`，且**不得**被解释为不限 |
| `1` | 禁用（该资源不能用于此维度） | 必须为 `0` |
| `2` | 继承上级默认 | 必须为 `0`，解析时向上查找 |
| `3` | 明确不限 | 必须为 `0` |
| `4` | 明确值 | 必须 `> 0` |

校验落在 Go 侧（服务端强制）。数据库层不写 `CHECK` 约束——三库对 `CHECK` 的支持与执行时机差异较大，且底座未使用 `CHECK`。

### 3.4 JSON 列规约

- 一律 `TEXT`，Go 侧用 `common.Marshal` / `common.Unmarshal` / `common.UnmarshalJsonStr`（`new-api/common/json.go:66/42/46`），**禁止直接 `encoding/json`**（`new-api/AGENTS.md` 的 JSON 条款、`CODE_STYLE.md` 第 18 条）。
- **写入前必须做字节数上界校验**：MySQL `text` 上限 65535 字节，SQLite / PG 的 `text` 无此限制，三库行为因此不同。超限时的处理是「拒绝写入 + 记录错误 + 保留上一版本」，不是截断成非法 JSON。
- 来自上游的 JSON（模型目录、价格源、回调）一律视为**不可信输入**：存原文，展示时转义，出站地址做 SSRF 校验（`CODE_STYLE.md` 第 5 条、计划书 §10.3）。

### 3.5 软删除与唯一约束的通用模板

底座有两种做法：

| 底座做法 | 证据 | 三库可移植性 |
|---|---|---|
| A. `gorm.DeletedAt` + 普通索引 | `new-api/model/user.go:106`、`model/token.go:32`、`model/redemption.go:25`、`model/passkey.go:42`、`model/twofa.go:24` | ✅ 完全可移植 |
| B. `gorm.DeletedAt` + **部分唯一索引** `uniqueIndex:...,where:deleted_at IS NULL` | `new-api/model/prefill_group.go:80`；PG 侧的索引修复逻辑在 `new-api/model/prefill_group_migration.go:87`（读取 `pg_get_expr(index_meta.indpred, ...) = '(deleted_at IS NULL)'`）与 `model/prefill_group_migration_test.go:162`（`CREATE UNIQUE INDEX ... WHERE deleted_at IS NULL`） | ⚠️ **未核实** MySQL 行为；MySQL 不支持带 `WHERE` 的唯一索引 |

**本草案一律采用 A + 显式墓碑列**，不采用 B：

```
软删除列：deleted_at  SOFTDEL
墓碑列：  alive_flag  BIGINT   -- 0 = 存活；被软删除时置为本行 ID（唯一值）
```

- 「存活期间唯一」的约束写成 `uniqueIndex(..., alive_flag)`。
- 为什么这样可行：存活行 `alive_flag = 0`，同一业务键下最多只能有一行 `alive_flag = 0`，唯一索引因此阻止重复；删除时 `UPDATE ... SET deleted_at = <ts>, alive_flag = id`，各墓碑行取值互不相同，永不互相冲突，也永不与未来的新行冲突（新行又是 `0`）。
- 删除必须是一条语句完成，且 `WHERE id = ? AND alive_flag = 0` 保证重复删除不改变墓碑值。
- 用 `NULL` 做墓碑（或把 `deleted_at` 放进唯一键）**不可行**：三库都把多个 `NULL` 视为互不相同，唯一索引会形同虚设。

**唯一索引长度**：MySQL 在 `COMPACT` 行格式下索引前缀上限 767 字节、`DYNAMIC` 下 3072 字节（这是通用 MySQL 知识，**未在本仓库核实**）。因此凡是**参与唯一索引的字符串**，要么是长度 ≤ 191 的短标识，要么改用其 `HASH64` 摘要列。本草案对长字符串统一使用 `*_hash` 列进唯一索引，原字符串另存。

### 3.6 保留期（依据计划书 §12）

| 数据 | 保留期 | 载体 |
|---|---|---|
| 请求元数据 | 默认 30 天 | `apigx_requests` / `apigx_attempts` / `apigx_reservations` / `apigx_idempotency_records` |
| 聚合数据 | 默认 180 天 | `apigx_quality_buckets` |
| 计价 / 授权快照 | 保留至所引用记录到期 | `apigx_price_rules` / `apigx_price_rule_items` / `apigx_fx_rates` / `apigx_grants` / `apigx_access_policies` |
| 尚未结束的任务及其必要凭据引用 | **不提前清理** | `apigx_async_tasks` / `apigx_upstream_object_refs` |
| 媒体产物（暂存） | 终态后默认 24 小时 | `apigx_upstream_object_refs.purge_after` |
| 审计事件 | 长于请求元数据（具体天数待运营确定） | `apigx_audit_events` |

保留期数值落在 `apigx_platform_settings`，不写死在代码里。

---

## 4. 实体 → 表 → 底座关联总览

```
底座 users ──1:1── apigx_user_profiles
             └──1:N── apigx_api_keys ──1:1── 底座 tokens（影子令牌，key = 摘要）

底座 users ──1:N── apigx_share_groups ──1:N── apigx_channels ──1:N── apigx_model_offerings
                                                                        │
apigx_canonical_models ──1:N── apigx_model_offerings ───────────────────┘
        ▲                                     │
        └── apigx_model_aliases               ├── apigx_access_policies（策略）
                                              ├── apigx_grants（授权）
                                              ├── apigx_price_rules ── apigx_price_rule_items
                                              └── apigx_capacity_policies
apigx_channels ──1:N── apigx_upstream_keys ──1:N── apigx_key_model_capabilities
                            └── apigx_upstream_accounts（共享限额归属）

一次调用：
apigx_requests ──1:N── apigx_attempts ──1:N── apigx_usage_events
      │                    │                        ▲
      │                    └── apigx_reservations ──┘（幂等预留/释放）
      └── 关联 底座 logs（通过 logs.request_id）

异步：
apigx_async_tasks ──1:N── apigx_upstream_object_refs
                  └──1:1── 底座 tasks（影子任务）

贯穿：apigx_audit_events（只读留痕）、apigx_idempotency_records、apigx_platform_settings
```

---

## 5. 逐表定义

> 每表统一给出：用途 / 关联 / 字段表 / 主键 / 唯一约束 / 索引（含它服务的查询模式）/ 软删除 / 不可变快照字段 / **绝不回写历史的字段**。

### 5.1 `apigx_user_profiles` —— User（平台侧属性）

**用途**：承载平台独有的用户属性，与底座 `users`（`new-api/model/user.go:79-115`）一对一。**不新建用户表**，不复制身份、密码、邮箱、会话（`CODE_STYLE.md` 第 9 条「不另起第二套用户系统」）。

**关联**：`user_id` = 底座 `users.id`（1:1）。`users.deleted_at` 是用户生命周期的唯一权威。

| 字段 | 类型 | 用途 |
|---|---|---|
| `user_id` | `ID`（`gorm:"primaryKey;autoIncrement:false"`） | 与底座 `users.id` 同值 |
| `trust_level` | `ENUM` | 0=待审核 / 1=普通 / 2=可信分享者 / 3=管理员指定；用于 §12「可信分享者策略」 |
| `sharing_enabled` | `BOOL` | 是否允许发布分享资源（审核策略开关） |
| `default_route_mode` | `STR(16)` | Key 未指定时的默认路由偏好：`pinned`/`latency`/`throughput`/`reliability`/`auto` |
| `default_allow_fallback` | `BOOL` | 默认是否允许「指定来源失败后自动回退」（§9.1） |
| `budget_limit_mode` / `budget_limit_micro` | `ENUM` / `MICRO` | 用户级每日 USD 估算预算（四态，§3.3） |
| `freeze_state` | `ENUM` | 0=正常 / 1=管理员封禁 / 2=风控冻结。与底座 `users.status` 并存，**不替代**它 |
| `frozen_at` / `frozen_reason` | `TS` / `STR(255)` | 冻结时间与原因（不含凭据） |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | 跟随底座用户的软删除语义 |

- **主键**：`user_id`。
- **唯一约束**：主键即唯一；不额外建。
- **索引**：`index(trust_level, sharing_enabled)` —— 服务「可信分享者列表 / 待审核用户列表」。
- **软删除**：是（`deleted_at`）。用户删除后本行保留 `user_id` 供历史引用，不物理删。
- **不可变快照字段**：无。
- **绝不回写历史的字段**：本表字段全部是「当前状态」，任何变更都**不得**回写 `apigx_requests` / `apigx_attempts` / `apigx_usage_events` / `apigx_async_tasks` 已写入的行。尤其 `trust_level` 下调**不得**追溯性地改变历史请求的可见性或归属。

---

### 5.2 `apigx_api_keys` —— APIKey

**用途**：平台 API Key 的权威记录：持有者、可访问范围、默认策略、额度与到期时间（计划书 §7 第 1 行）。

**关联**：`user_id` → 底座 `users.id`；`token_id` → 底座 `tokens.id`（影子令牌，复用底座鉴权与计费链路，不新起第二条鉴权路径）。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | 稳定内部 ID（Grant、请求归属、轮询索引都引用它） |
| `user_id` | `FK` | 持有者 |
| `token_id` | `FK` | 底座 `tokens.id`；唯一 |
| `name` | `STR(64)` | 展示名（可重名，不作授权依据，`CODE_STYLE.md` 第 1 条） |
| `key_prefix` | `STR(12)` | 明文前 8 位，仅供界面识别；**不含完整凭据** |
| `key_hash` | `HASH64` | **SHA-256(secret) 十六进制**。不可恢复；字符集 `[0-9a-f]`，天然不含 `-`（§2.4） |
| `scope_mode` | `ENUM` | 0=全部已授权供给 / 1=白名单 / 2=固定单一来源（硬限制） |
| `pinned_offering_id` | `FK` | `scope_mode=2` 时必填（非 0） |
| `allowed_model_ids` | `TEXT` | JSON 数组：允许的 `apigx_canonical_models.id`（`scope_mode=1` 用） |
| `allowed_owner_user_ids` | `TEXT` | JSON 数组：允许的分享者（可信分享者限定，§10.3） |
| `allow_fallback` | `BOOL` | 是否允许回退；硬限制，请求参数不能放宽（§9.1） |
| `default_route_mode` | `STR(16)` | 默认路由偏好（偏好，不是硬限制） |
| `rate_limit_mode` / `rate_limit_rpm` | `ENUM` / `BIGINT` | Key 级 RPM（四态） |
| `budget_limit_mode` / `budget_limit_micro` | `ENUM` / `MICRO` | Key 级每日 USD 预算（四态） |
| `status` | `ENUM` | 0=待激活 / 1=启用 / 2=禁用 / 3=已撤销 / 4=已过期 |
| `expires_at` | `TS` | 0=不过期 |
| `last_used_at` | `TS` | |
| `revoked_at` / `revoked_by_user_id` | `TS` / `FK` | |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(key_hash)`；`uniqueIndex(token_id)`。两者都不含 `alive_flag`——摘要与底座令牌行都**永不复用**，软删除后继续占用唯一键是正确的。
- **索引**：`index(user_id, status)` 服务「我的 Key 列表 / 按状态筛选」；`index(status, expires_at)` 服务过期扫描。
- **软删除**：是。撤销（`revoked_at`）与软删除是两件事：撤销后行保留，用于历史归属；软删除只用于误建的清理。
- **不可变快照字段**：`key_hash`、`created_at`、`user_id`、`token_id`。
- **绝不回写历史的字段**：`name`（改名不回写历史请求里的 key 名称快照）、`scope_mode` / `pinned_offering_id` / `allowed_*` / `allow_fallback`（收窄授权**不得**追溯性地使历史请求变成「越权」或改归属；只影响新请求）、`status`（撤销不影响历史对象的读取授权，按 §10.3 生命周期表处理）。

**影子令牌行的约定**（写入底座 `tokens`）：
- `key` = 同 `key_hash`（见 §6.2 的方案选择，此处假设已实现哈希查找分支）；
- `group` **不写入任何分享分组标识**（`CODE_STYLE.md` 第 9 条），取值策略由容量/路由设计另行决定（**未核实**：底座是否有其它逻辑依赖 `tokens.group` 非空）；
- `model_limits_enabled` / `model_limits` 由平台侧权威记录派生，不作为授权来源；
- `unlimited_quota` / `remain_quota` 在 `usage_only` 免费模式下**不承担准入判断**（计划书 §6、§12「没充值的合法用户可以调用」），准入由 `apigx_capacity_policies` + `apigx_reservations` 负责。

---

### 5.3 `apigx_share_groups` —— ShareGroup

**用途**：「分享者 + 分组名称 + 简介 + 默认策略 + 发布状态」（计划书 §7 第 2 行）。分组**允许重名**（`CODE_STYLE.md` 第 1 条），因此名称不是唯一键。

**关联**：`owner_user_id` → 底座 `users.id`；被 `apigx_channels.share_group_id`、`apigx_access_policies`（`scope_type=0`）、`apigx_grants`（`resource_type=0`）、`apigx_invites`（`target_scope_type=0`）引用。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | 稳定内部 ID（授权与历史归属一律用它） |
| `public_id` | `STR(32)` | 对外稳定标识（展示、URL、日志用），永不复用 |
| `owner_user_id` | `FK` | 分享者；**第一版不可变更**（§7「需另设迁移流程，第一版不开放直接修改 owner 的接口」） |
| `name` | `STR(64)` | 展示名，可重名；更名不影响任何授权 |
| `description` | `TEXT` | 简介（不可信内容，渲染前转义） |
| `default_visibility` | `ENUM` | 0=私有 / 1=受限 / 2=公开 |
| `default_policy_id` | `FK` | → `apigx_access_policies.id`（该分组的默认策略行） |
| `publish_status` | `ENUM` | 0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停 / 4=已退役 |
| `alive_flag` | `BIGINT` | 软删除墓碑（§3.5） |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | 退役用软删除，保留被引用 ID（§7） |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(public_id)`。**`(owner_user_id, name)` 不唯一**——分组可重名。若产品后续要求「同一分享者下名称唯一」，须加 `alive_flag` 的复合唯一键（`uniqueIndex(owner_user_id, name, alive_flag)`），但那会与「可重名」冲突，需先改产品决策。
- **索引**：`index(owner_user_id, publish_status)` 服务「我的分组列表 + 状态筛选」；`index(publish_status, default_visibility)` 服务广场聚合查询（公开/受限可见性筛选）。
- **软删除**：是。
- **不可变快照字段**：`public_id`、`owner_user_id`、`created_at`。
- **绝不回写历史的字段**：`name`（分组更名**不得**改变已有授权或历史请求归属，§7）、`description`、`default_visibility`（收紧父分组策略**不得**追溯撤销已生成的 Grant；仅影响新授权与新请求准入）、`owner_user_id`（不可变）。

---

### 5.4 `apigx_channels` —— Channel（分享渠道）

**用途**：「owner、分组、协议、Base URL、Key 池策略、并发/RPM/TPM/任务上限、共享限额池、健康状态」（计划书 §7 第 3 行）。

**关联**：`owner_user_id` → `users.id`；`share_group_id` → `apigx_share_groups.id`；`base_channel_id` → 底座 `channels.id`（影子渠道，承载 relay 适配器与协议配置）；`capacity_policy_id` → `apigx_capacity_policies.id`（`scope_type=2`）；`upstream_account_id` 默认池 → `apigx_upstream_accounts.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | 稳定内部 ID |
| `public_id` | `STR(32)` | 对外稳定标识 |
| `owner_user_id` | `FK` | 分享者；不可变（同 §5.3） |
| `share_group_id` | `FK` | 所属分享分组 |
| `name` | `STR(64)` | 展示名 |
| `base_channel_id` | `FK` | 底座 `channels.id`；唯一的影子渠道行 |
| `protocol` | `STR(32)` | 客户端协议族（`openai` / `anthropic` / `gemini` / …，取值对齐底座 `constant` 包） |
| `base_url` | `STR(512)` | 上游 Base URL。**不可信输入**：出站前做 SSRF 校验（协议白名单、解析后地址校验、阻断环回/内网/云元数据，§10.3） |
| `auth_target` | `STR(256)` | 认证目标（域名/路径），与 `base_url` 一同参与「资源修订」判定 |
| `key_pool_mode` | `ENUM` | 渠道内选 Key 策略：0=轮询 / 1=随机 / 2=加权 / 3=最低占用。**使用者不可见**（§9.2「渠道选择与 Key 选择分别配置」） |
| `capacity_policy_id` | `FK` | 渠道级容量策略（并发/RPM/TPM/任务上限/预算） |
| `default_upstream_account_id` | `FK` | 该渠道默认归属的上游账户池（共享限额） |
| `health_state` | `ENUM` | 0=未知 / 1=健康 / 2=降级 / 3=不健康 / 4=冷却中 |
| `health_reason` | `STR(128)` | 脱敏原因码（不含地址细节与凭据，§9.1） |
| `health_updated_at` | `TS` | |
| `revision` | `BIGINT` | 资源修订号，初始 1 |
| `verified_at` | `TS` | 最近一次连通性验证时间 |
| `alive_flag` | `BIGINT` | 软删除墓碑 |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(public_id)`；`uniqueIndex(base_channel_id)`（一个影子渠道只对应一个分享渠道）。
- **索引**：`index(share_group_id, publish_status)`（见下）→ 实际为 `index(share_group_id, health_state)` 服务「分组下的渠道列表 + 健康筛选」；`index(owner_user_id, health_state)` 服务分享者控制台；`index(health_state, revision)` 服务调度候选筛选。
  > 说明：`publish_status` 落在 `apigx_model_offerings`（供给级发布），渠道级的可见状态由所属供给的 `access_default` 与分组默认策略求交决定，不在本表冗余。
- **软删除**：是（退役用软删除，保留被请求/任务/价格/计量记录引用的稳定 ID）。
- **不可变快照字段**：`public_id`、`owner_user_id`、`base_channel_id`、`protocol`、`created_at`。`base_url` / `auth_target` **可改但视为新修订**：变更时 `revision++`、`verified_at=0`，并把状态打回 `pending_review`，同时使旧亲和缓存失效（§10.3「渠道 Base URL、认证目标或模型归属变更视为新的资源修订」）。
- **绝不回写历史的字段**：`name`、`base_url`、`auth_target`、`key_pool_mode`、`health_state`、`owner_user_id`。历史 `apigx_attempts` 记的是 `selected_channel_id` 与快照值，**不因渠道改名/改址/改健康状态而改变**。

---

### 5.5 `apigx_upstream_accounts` —— 上游账户池（派生表，服务 §9.2 共享限额）

**用途**：计划书 §9.2 要求「同一上游账户下的多条渠道可共享额度与熔断范围，避免改名或多 Key 绕过同一限额」，且 §7 在 UpstreamKey 行内提到「上游账户池」。落表以便多 Key / 多渠道共同引用同一限额。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `owner_user_id` | `FK` | 分享者（账户归属） |
| `provider` | `STR(32)` | 厂商/服务商标识 |
| `account_label` | `STR(64)` | 脱敏展示标识（不含凭据、不含完整账号） |
| `account_key_hash` | `HASH64` | 由分享者声明的关联标识派生的摘要，用于去重；**绝不由凭据明文派生**（避免把凭据塞进可枚举的哈希空间） |
| `capacity_policy_id` | `FK` | 该账户池的共享限额（`scope_type=4`） |
| `circuit_state` | `ENUM` | 0=关闭 / 1=半开 / 2=打开（熔断范围按账户池共享，§9.5） |
| `circuit_opened_at` | `TS` | |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(owner_user_id, provider, account_key_hash, alive_flag)`。
- **索引**：`index(owner_user_id, provider)` 服务「我的上游账户列表」。
- **软删除**：是。
- **不可变快照字段**：`owner_user_id`、`provider`。
- **绝不回写历史的字段**：`account_label`、`capacity_policy_id`。历史 `apigx_attempts.upstream_account_id` 只记 ID，不记 label。

---

### 5.6 `apigx_upstream_keys` —— UpstreamKey

**用途**：「渠道、稳定 ID、名称、secret_ref 与凭据版本、启停/冷却/到期、权重、独立限额、上游账户池及价格配置」（计划书 §7 第 4 行）。

**关联**：`channel_id` → `apigx_channels.id`；`upstream_account_id` → `apigx_upstream_accounts.id`（0=未归属）；`capacity_policy_id` → `apigx_capacity_policies.id`（`scope_type=3`，独立限额）；`price_rule_id` → `apigx_price_rules.id`（Key 级价格配置）。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | 稳定内部 ID（`apigx_attempts` / `apigx_async_tasks` 引用它，永不变） |
| `channel_id` | `FK` | 所属分享渠道 |
| `stable_key_id` | `STR(32)` | 渠道内稳定标识：轮询/权重/统计都不依赖导入行号（`CODE_STYLE.md` 第 1 条） |
| `name` | `STR(64)` | 展示名 |
| `secret_ref` | `STR(255)` | 指向受保护凭据存储的**引用**，不含明文。主密钥与数据库分离（§10.3） |
| `credential_version` | `BIGINT` | 凭据版本号，轮换时递增（不新建行） |
| `credential_started_at` | `TS` | 当前版本启用时间 |
| `status` | `ENUM` | 0=启用 / 1=禁用 / 2=冷却 / 3=已过期 / 4=鉴权失效 / 5=已撤销 |
| `weight` | `BIGINT` | 权重（仅用于选 Key，与渠道权重不混用，§9.3） |
| `cooldown_until` | `TS` | 冷却截止 |
| `expires_at` | `TS` | 0=不过期 |
| `upstream_account_id` | `FK` | 上游账户池归属 |
| `capacity_policy_id` | `FK` | Key 独立限额（四态） |
| `price_rule_id` | `FK` | Key 价格配置（0=继承供给/渠道） |
| `discovery_state` | `ENUM` | 0=未发现 / 1=已发现 / 2=发现失败 / 3=手工录入（§8.1） |
| `last_ok_at` / `last_error_at` | `TS` | |
| `last_error_code` | `STR(64)` | 脱敏错误码 |
| `alive_flag` | `BIGINT` | 软删除墓碑 |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(channel_id, stable_key_id, alive_flag)` —— 保证同渠道内稳定 ID 有效唯一，退役后同一 `stable_key_id` 可被新行复用。
- **索引**：`index(channel_id, status, weight)` 服务「渠道内合格 Key 的池策略选择（轮询/随机/加权/最低占用）」；`index(upstream_account_id, status)` 服务账户池限额与熔断范围判定；`index(status, cooldown_until)` 服务冷却到期扫描；`index(credential_version)` 不建（无此查询模式）。
- **软删除**：是。**Key 退役用软删除 + `status`**，绝不物理删除（§7「保留被请求、任务、价格及计量记录引用的稳定 ID」）。
- **不可变快照字段**：`channel_id`、`stable_key_id`、`created_at`。**`secret_ref` 与 `credential_version` 的关系**：轮换 = `credential_version+1` + `secret_ref` 更新 + `credential_started_at` 更新；**不新建行**，因此历史 `apigx_attempts.upstream_key_credential_version` 仍能指回「当时用的是哪一版」。
- **绝不回写历史的字段**：`name`、`weight`、`price_rule_id`、`capacity_policy_id`、`status`、`credential_version`。**关键**：Key 轮换/停用/移除**不得**把历史 `apigx_async_tasks` / `apigx_attempts` 改绑到池里的下一把 Key（§9.2、§10.3）。

---

### 5.7 `apigx_key_model_capabilities` —— KeyModelCapability

**用途**：「Key 支持的上游模型和能力、发现/验证状态及时间」（计划书 §7 第 5 行）。§8.1 要求广场分别标注「分享者声明」「目录发现」「连通性验证」——本表是这三类证据的唯一落点。

**关联**：`upstream_key_id` → `apigx_upstream_keys.id`。**刻意不直接挂 `canonical_model_id`**：本表记录的是「上游自称什么」，归一由 `apigx_model_aliases` 负责，避免把「目录返回同名」当成模型真实性认证（§8.1）。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `upstream_key_id` | `FK` | |
| `capability_key_hash` | `HASH64` | `SHA-256(upstream_model_id + "|" + capability)`；进唯一索引用，绕开长字符串前缀限制（§3.5） |
| `upstream_model_id` | `STR(191)` | 上游模型 ID（不可信输入，存原文） |
| `capability` | `ENUM` | 能力枚举：1=对话 / 2=向量 / 3=图片生成 / 4=图片编辑 / 5=语音识别 / 6=语音生成 / 7=重排 / 8=视频 |
| `discovery_source` | `ENUM` | 0=手工输入 / 1=目录接口 / 2=主动探测 |
| `evidence_state` | `ENUM` | 0=分享者声明 / 1=目录发现 / 2=连通性验证通过 / 3=验证失败 |
| `discovered_at` / `verified_at` | `TS` | |
| `last_error_code` | `STR(64)` | 脱敏 |
| `raw_metadata` | `TEXT` | 上游返回的原始描述 JSON（不可信，展示转义） |
| `raw_metadata_bytes` | `INT` | 写入时的字节数（用于 MySQL `text` 上限监控，§3.4） |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(capability_key_hash)`。
- **索引**：`index(upstream_key_id, capability)` 服务「某 Key 在给定能力下支持哪些模型」（路由过滤第一步）；`index(evidence_state, capability)` 服务广场证据标注与「待验证」队列。
- **软删除**：无。发现结果可重建；Key 退役后本表行保留（用于历史统计口径），不物理删。
- **不可变快照字段**：`upstream_model_id`、`capability`、`discovery_source`、`discovered_at`。同一 (Key, 模型, 能力) 的**重新验证只更新 `evidence_state` / `verified_at` / `last_error_code`**，不改写来源与发现时间。
- **绝不回写历史的字段**：`evidence_state`（证据降级/升级不得回写历史请求或历史质量桶；只影响后续路由与展示）。

---

### 5.8 `apigx_canonical_models` —— CanonicalModel

**用途**：「厂商、标准 ID、版本、类型、能力和描述来源」（计划书 §7 第 6 行）。由**平台维护**；分享者只能提交映射建议或发布自定义模型，不能编辑全局标准模型（§7）。

**关联**：`vendor_id` **软引用**底座 `vendors.id`（`new-api/model/vendor_meta.go:15`）；被 `apigx_model_offerings`、`apigx_model_aliases`、`apigx_requests`、`apigx_usage_events` 引用。

> **为什么另建而不复用底座 `models` 表**（`new-api/model/model_meta.go:35-61`）：底座 `models` 是「模型广场元数据」表，其唯一键是 `(model_name, deleted_at)`（`:37`、`:48`），没有对外 `api_model_id`、没有类型/变体/合并语义、也没有「平台维护 vs 分享者自定义」的边界。计划书 §8.2 要求「每个标准模型具有稳定的内部 ID 和对外 `api_model_id`」，需要一个显式的 id 层。
> **未核实**：是否需要与底座 `models` 做双向同步（例如让底座模型广场也显示 APIGX 标准模型）。第一版建议不做同步，二者独立。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | **稳定内部 ID**；Grant、请求归属、历史计量一律用它 |
| `api_model_id` | `STR(191)` | **对外标准 ID**，客户端从可用目录/调用示例获取（§8.2）。发布后永不复用 |
| `vendor_id` | `FK` | 软引用底座 `vendors.id`；0=未匹配到已知厂商 |
| `vendor_label` | `STR(64)` | 厂商展示名快照（上游厂商字段不可信，可能与底座厂商列表不一致） |
| `model_family` | `STR(128)` | 模型族名（如 `gpt`、`claude`） |
| `version_label` | `STR(64)` | 版本标识（如 `4o`、`3-5-sonnet`）。**不通过删除版本号/日期/量化后缀来强行归并**（§8.2） |
| `variant` | `STR(64)` | 变体（如 `latest`、`preview`、`thinking`）。动态 `latest` 别名必须在此标注（§8.2） |
| `modality_type` | `ENUM` | 主类型：1=对话 / 2=向量 / 3=图片 / 4=语音识别 / 5=语音生成 / 6=重排 / 7=视频 |
| `capabilities` | `TEXT` | JSON：平台级**参考**能力（工具调用、视觉输入、上下文长度、JSON 格式等）。供给的实际能力以 `apigx_model_offerings.capabilities` 为准（§8.2） |
| `description_source` | `ENUM` | 0=平台维护 / 1=上游声明 / 2=外部来源 |
| `description` | `TEXT` | 描述（不可信内容，渲染前转义） |
| `status` | `ENUM` | 0=草稿 / 1=生效 / 2=已弃用 / 3=已合并 |
| `merged_into_id` | `FK` | 合并目标；**仅作路由与展示提示**，不改变已有授权或历史归属（§7） |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(api_model_id)`。**刻意不含 `alive_flag`**：标准模型用 `status` 退役，`api_model_id` 永不复用，否则旧客户端会静默打到不同模型。
- **索引**：`index(vendor_id, model_family)` 服务「按厂商/族聚合展示」；`index(status, modality_type)` 服务「按类型列目录」；`index(merged_into_id)` 服务合并链解析。
- **软删除**：**无**。标准模型是长期引用目标，退役用 `status`；`deleted_at` 不设，避免与「已有授权/历史不改」原则产生歧义。
- **不可变快照字段**：`id`、`api_model_id`、`created_at`。
- **绝不回写历史的字段**：`api_model_id`、`vendor_label`、`model_family`、`version_label`、`variant`。历史 `apigx_requests.api_model_id` / `apigx_usage_events.api_model_id` 是**快照列**：模型合并、别名调整**不得**改写它们（§7、§11 验收「模型合并、别名歧义、分组更名 → 不扩大授权、不重写历史」）。

---

### 5.9 `apigx_model_aliases` —— ModelAlias

**用途**：「上游模型 ID 或 New API 本地价格 ID 到标准模型的映射、范围和确认状态」（计划书 §7 第 7 行）。§8.4 要求通过独立映射连接标准模型，并保留厂商入口、地区和服务档位。

**关联**：`target_canonical_model_id` → `apigx_canonical_models.id`。`scope_type` + `scope_id` 指向分组/渠道/供给。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `scope_type` | `ENUM` | 0=平台全局 / 1=厂商 / 2=渠道 / 3=供给 |
| `scope_id` | `FK` | 对应范围 ID；`scope_type=0` 时为 0 |
| `source` | `ENUM` | 0=上游目录 / 1=New API 本地定价 / 2=分享者建议 |
| `source_ref` | `STR(255)` | 本地配置来源标识（不可信，展示时转义） |
| `external_model_id` | `STR(191)` | 上游模型或 New API 本地价格配置中的模型 ID（原文） |
| `normalized_model_id` | `STR(191)` | 归一化后的匹配键（大小写/空白归一；**不做版本号或日期剥离**，§8.2） |
| `alias_key_hash` | `HASH64` | `SHA-256(scope_type|scope_id|source|normalized_model_id)`；进唯一索引 |
| `target_canonical_model_id` | `FK` | 映射目标 |
| `relation` | `ENUM` | 0=完全匹配 / 1=已确认别名 / 2=候选（仅提示，不自动路由） |
| `confirmation_state` | `ENUM` | 0=待确认 / 1=已确认 / 2=已拒绝 |
| `confirmed_by_user_id` | `FK` | 确认人；分享者只能确认自己范围内的映射（§7） |
| `confirmed_at` | `TS` | |
| `region_label` | `STR(64)` | 厂商入口/地区；**同模型跨地区报价不互相覆盖**（§8.3） |
| `tier_label` | `STR(64)` | 服务档位（Standard/Batch 等，§8.3） |
| `evidence` | `TEXT` | 证据 JSON（来源、抓取时间、页完整性标记；不可信） |
| `alive_flag` | `BIGINT` | 软删除墓碑 |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(alias_key_hash, alive_flag)` —— 同一范围内同一来源的同一上游 ID 只能有一条有效映射。
  > 注意 `region_label` / `tier_label` **不在唯一键内**：它们通过 `apigx_price_rules.conditions` 与 `apigx_price_rule_items` 承载多地区/多档位配置，避免同模型变体互相覆盖。
- **索引**：`index(scope_type, scope_id, confirmation_state)` 服务「某范围待确认映射列表」；`index(target_canonical_model_id, confirmation_state)` 服务「标准模型的反向映射」；`index(source, normalized_model_id)` 服务同步时的批量匹配。
- **软删除**：是。
- **不可变快照字段**：`scope_type`、`scope_id`、`source`、`external_model_id`、`created_at`。
- **绝不回写历史的字段**：`target_canonical_model_id`、`relation`。**别名调整不得改写已有请求与计量的 `canonical_model_id` 归属**（§7）。歧义时返回错误而非任选一条：`confirmation_state` 不是「已确认」的映射**不得**参与严格自动路由（§8.2）。

---

### 5.10 `apigx_model_offerings` —— ModelOffering

**用途**：「标准模型与渠道的关联、上游 ID、实际能力、访问策略、价格版本」（计划书 §7 第 9 行）。这是**「指定来源」的粒度**，也是 `X-APIGX-Offering` 请求头的取值来源（§9.1）。

**关联**：`channel_id` → `apigx_channels.id`；`canonical_model_id` → `apigx_canonical_models.id`；`capacity_policy_id` → `apigx_capacity_policies.id`（`scope_type=5`）；`price_rule_id` → `apigx_price_rules.id`（当前生效价格版本指针）；被 `apigx_grants`（`resource_type=1`）、`apigx_invites`（`target_scope_type=1`）、`apigx_quality_buckets`、`apigx_attempts`、`apigx_usage_events`、`apigx_async_tasks` 引用。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | 稳定内部 ID |
| `public_id` | `STR(32)` | **对外 offer id**，`X-APIGX-Offering` 用它（§9.1） |
| `channel_id` | `FK` | 所属渠道 |
| `canonical_model_id` | `FK` | 标准模型 |
| `upstream_model_id` | `STR(191)` | 转发时替换成的上游模型 ID（§8.2「转发器再转换为所选供给的上游模型 ID」） |
| `offering_key_hash` | `HASH64` | `SHA-256(channel_id|canonical_model_id|upstream_model_id)`；进唯一索引 |
| `capabilities` | `TEXT` | JSON：**该供给的实际能力**（工具调用、视觉、上下文长度、JSON、音色、视频规格等，§8.2「同一模型支持的能力可能因渠道而异」） |
| `access_default` | `ENUM` | 0=私有 / 1=受限 / 2=公开；与分组默认策略**求交**，不能放宽父级（§7） |
| `capacity_policy_id` | `FK` | 供给级容量策略 |
| `price_rule_id` | `FK` | 当前生效价格版本（指向 `apigx_price_rules.id`，可空=未定价） |
| `status` | `ENUM` | 0=草稿 / 1=待审核 / 2=已发布 / 3=已暂停 / 4=已退役 |
| `revision` | `BIGINT` | 资源修订号 |
| `verified_at` | `TS` | 最近连通性验证 |
| `alive_flag` | `BIGINT` | 软删除墓碑 |
| `created_at` / `updated_at` | `TS` | |
| `deleted_at` | `SOFTDEL` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(public_id)`；`uniqueIndex(offering_key_hash, alive_flag)`。
- **索引**：`index(canonical_model_id, status, access_default)` 服务**广场聚合主查询**（同一标准模型下的可见供给）；`index(channel_id, status)` 服务分享者控制台；`index(status, price_rule_id)` 服务「未定价资源」筛选（§8.4 第 8 点）。
- **软删除**：是。
- **不可变快照字段**：`public_id`、`channel_id`、`canonical_model_id`、`created_at`。
- **绝不回写历史的字段**：`upstream_model_id`（变更 = 新的资源修订：`revision++` + 回到 `pending_review` + 旧亲和缓存失效，§10.3）、`capabilities`、`access_default`、`price_rule_id`、`status`。历史 `apigx_attempts` / `apigx_usage_events` 记的是当时的 `upstream_model_id` 与 `price_rule_id` 快照。

---

### 5.11 `apigx_access_policies` —— AccessPolicy

**用途**：「公开、私有或受限策略」（计划书 §7 第 10 行前半）。可设在分组（默认值）与供给（进一步收紧）两级。

**关联**：`owner_user_id` → `users.id`；`scope_type`+`scope_id` → `apigx_share_groups.id` 或 `apigx_model_offerings.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `owner_user_id` | `FK` | 策略归属（服务端按登录用户校验归属，§6） |
| `scope_type` | `ENUM` | 0=分享分组默认策略 / 1=供给策略 |
| `scope_id` | `FK` | |
| `visibility` | `ENUM` | 0=私有（仅本人）/ 1=受限（仅获授权者可见）/ 2=公开（可浏览） |
| `allow_auto_route` | `BOOL` | 是否允许进入自动路由候选（与「可见」不同：可见 ≠ 可调用，§2/§7） |
| `require_trusted_sharer` | `BOOL` | 是否只允许可信分享者范围内的用户调用 |
| `policy_version` | `BIGINT` | 权限版本号；每次变更 +1。用于「权限变更通过版本和缓存失效传播」与「每次出站提交前重验当前状态」（§10.3） |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(scope_type, scope_id)` —— 一个范围一份当前策略；历史版本由 `policy_version` 递增 + `apigx_audit_events` 留痕，不在此表堆叠。
- **索引**：`index(owner_user_id, scope_type)` 服务「我的策略列表」；`index(scope_type, visibility)` 服务可见性过滤。
- **软删除**：无。策略随 scope 生命周期；scope 软删除后策略行保留（历史的 `policy_version` 仍可被 `apigx_async_tasks.policy_version` 引用）。
- **不可变快照字段**：`scope_type`、`scope_id`、`created_at`。
- **绝不回写历史的字段**：`visibility`、`allow_auto_route`、`require_trusted_sharer`。**收紧私有化不得追溯撤销已生成的 Grant，也不得改写历史请求归属**（§7、§11 验收「私有/受邀模型 → 未授权用户在页面、API、计数和任务接口均无法访问」是**当前**语义，不是改历史）。已有对象的读取授权来自创建时形成的用户所有权（§10.3）。

---

### 5.12 `apigx_grants` —— Grant

**用途**：「用户授权、来源、到期和撤销状态」（计划书 §7 第 10 行后半）。资源邀请兑换和指定用户都生成受资源范围约束的 Grant（§7）。

**关联**：`subject_id` → 按 `subject_type` 指向 `users.id` / `apigx_api_keys.id` / `apigx_invites.id`；`resource_id` → 按 `resource_type` 指向 `apigx_share_groups.id` / `apigx_model_offerings.id`；`source_invite_id` → `apigx_invites.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `subject_type` | `ENUM` | 0=用户 / 1=平台 Key / 2=邀请 |
| `subject_id` | `FK` | |
| `resource_type` | `ENUM` | 0=分享分组 / 1=模型供给 |
| `resource_id` | `FK` | |
| `scope_kind` | `ENUM` | 0=指定用户 / 1=邀请兑换 / 2=所有者本人 |
| `source_invite_id` | `FK` | **`0` 表示非邀请来源（不是 NULL）**——三库把多个 `NULL` 视为互不相同，会破坏唯一约束 |
| `granted_by_user_id` | `FK` | |
| `granted_at` | `TS` | |
| `expires_at` | `TS` | 0=不过期。已生成 Grant 有**独立**到期时间，不随邀请到期而变（§7） |
| `gift_version` | `BIGINT` | 授予时的 `apigx_access_policies.policy_version` 快照，用于审计「授予时是什么策略」 |
| `revoked_at` | `TS` | 0=未撤销 |
| `revoked_by_user_id` | `FK` | |
| `revoke_reason` | `STR(255)` | 脱敏原因 |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(subject_type, subject_id, resource_type, resource_id, source_invite_id)` —— 防止同一邀请被并发兑换生成多条 Grant，也防止重复授予同一 (主体, 资源) 组合。
  > 该唯一约束**不含 `alive_flag`**：Grant 用 `revoked_at` 表达撤销，行本身是历史证据，不软删除；重新授予需要新的 `source_invite_id` 或显式的「重新授予」流程（此时应改由 `scope_kind` 区分，取值策略待产品确认 → **未核实**：重复授予同一组合的期望语义）。
- **索引**：`index(subject_type, subject_id, revoked_at, expires_at)` 服务**准入校验**（§9.2 第一步「用户和平台 Key 权限」）；`index(resource_type, resource_id, revoked_at)` 服务「某供给/分组的授权列表」（分享者视角）；`index(source_invite_id)` 服务「撤销邀请时选择是否连带撤销其 Grant」（§7）。
- **软删除**：无。Grant 是不可删的历史证据。
- **不可变快照字段**：`subject_type`、`subject_id`、`resource_type`、`resource_id`、`scope_kind`、`source_invite_id`、`granted_by_user_id`、`granted_at`、`gift_version`。**只允许** `revoked_at` / `revoked_by_user_id` / `revoke_reason` 从「未撤销」单向推进一次。
- **绝不回写历史的字段**：全部授权列。**撤权不改变历史请求与计量记录的归属**；历史任务的读取授权来自创建时形成的用户所有权，不因普通分享授权到期被转交给其他人（§10.3）。

---

### 5.13 `apigx_invites` —— Invite

**用途**：「注册邀请或资源邀请、Token 哈希、使用次数、有效期与目标范围」（计划书 §7 第 11 行）。§10.1 要求「注册邀请码与资源访问邀请码使用**独立类型和校验逻辑**」「邀请码保存哈希，支持到期、次数限制、撤销和并发兑换时的原子扣减」。

**关联**：`target_scope_type`+`target_scope_id` → `apigx_share_groups.id` / `apigx_model_offerings.id`（资源邀请）；`created_by_user_id` → `users.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `invite_type` | `ENUM` | 0=注册邀请 / 1=资源访问邀请。**两类共用表但校验逻辑独立**（§10.1） |
| `token_hash` | `HASH64` | SHA-256 十六进制，不存明文；不可恢复 |
| `token_prefix` | `STR(12)` | 明文前 8 位，仅供界面识别 |
| `target_scope_type` | `ENUM` | 0=无（注册邀请）/ 1=分享分组 / 2=模型供给 |
| `target_scope_id` | `FK` | |
| `use_limit_mode` | `ENUM` | 四态（未配置/禁用/继承默认/不限）+ 明确值，§3.3 |
| `max_uses` | `BIGINT` | `use_limit_mode=4` 时有效且 `> 0` |
| `used_count` | `BIGINT` | 原子递增 |
| `expires_at` | `TS` | 0=不过期 |
| `status` | `ENUM` | 0=有效 / 1=已撤销 / 2=已用尽 / 3=已过期 |
| `created_by_user_id` | `FK` | |
| `revoked_at` / `revoked_by_user_id` | `TS` / `FK` | |
| `revoke_grants` | `BOOL` | 撤销邀请时**是否连带撤销由它产生的 Grant**（§7 明确要求可选项） |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(token_hash)`。
- **索引**：`index(target_scope_type, target_scope_id, status)` 服务「某分组/供给的邀请列表」；`index(invite_type, status, expires_at)` 服务「注册邀请有效性检查 / 过期扫描」。
- **软删除**：无。撤销用 `status` + `revoked_at`，行保留作为兑换归因证据。
- **并发兑换的原子扣减**（**三库通用写法**，仿底座 `new-api/model/quota_reserve.go:144-149`）：
  ```
  UPDATE apigx_invites SET used_count = used_count + 1, updated_at = ?
   WHERE id = ? AND status = 0
     AND (use_limit_mode = 3 OR used_count < max_uses)
  ```
  > **勘误（2026-09-16）**：此处原写作 `use_limit_mode = 1`，与 §3.3 的四态定义冲突——§3.3 规定 `1 = 禁用`、`3 = 明确不限`。本 SQL 的语义是「不限次数或未达上限则允许」，因此必须用 `3`；用 `1` 会把「禁用」当成「不限」，等于把被禁用的邀请当成无限次可用。该字段本身不在本表实现范围内，但**服务端实现时必须按本节修正后的取值**。
  以 `RowsAffected == 1` 判定成功，否则拒绝。**不得**用「先 SELECT 再 UPDATE」——并发下会突破次数上限。
  ```
  同时在 Go 侧校验：expires_at = 0 OR expires_at > now
  ```
- **不可变快照字段**：`invite_type`、`token_hash`、`target_scope_type`、`target_scope_id`、`created_by_user_id`、`created_at`。
- **绝不回写历史的字段**：`target_scope_id` 指向的资源改名/合并后，邀请仍指向同一稳定 ID；**不得**把历史 Grant 的 `source_invite_id` 改绑到别的邀请。

---

### 5.14 `apigx_fx_rates` —— PriceSnapshot 的汇率版本（派生表，服务 §8.5）

**用途**：计划书 §8.5「汇率必须保存来源、生效时间及版本，不能将某个固定汇率写死为永久规则」；§12「管理员维护有依据的版本化汇率」。**不引入未经选择的第三方汇率依赖**（§8.5）。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `base_currency` | `CUR3` | 固定 `USD` |
| `quote_currency` | `CUR3` | 原币种，如 `CNY` |
| `rate_micro` | `MICRO` | 1 USD = `rate_micro / 1e6` 原币（如 7.123456 CNY → `7123456`） |
| `conversion_direction` | `ENUM` | 显式记录换算方向（0 = `USD = quote / rate`），避免方向歧义（§8.5 要求「明确方向」） |
| `effective_from` | `TS` | |
| `effective_to` | `TS` | 0=至今；被封版时写一次 |
| `source_label` | `STR(64)` | 依据来源（如 `admin`），不含凭据 |
| `note` | `STR(255)` | 说明 |
| `created_by_user_id` | `FK` | |
| `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(quote_currency, effective_from)` —— 同一币种同一生效时刻只有一版。
- **索引**：`index(quote_currency, effective_from)` 服务「取某币种在某时刻生效的汇率」。
- **软删除**：无。历史汇率必须可查，否则历史成本无法复核。
- **不可变快照字段**：**整行几乎不可变**：`base_currency`、`quote_currency`、`rate_micro`、`conversion_direction`、`effective_from`、`source_label`、`created_at` 一旦写入永不修改。
- **唯一允许的变更**：`effective_to` 从 `0` 单向改为一个更大的时间戳（封版），**不允许改回**。
- **绝不回写历史的字段**：整行。**汇率更新绝不改写历史估算**（§8.5）；各消费行（`apigx_price_rules` / `apigx_usage_events` / `apigx_attempts` / `apigx_async_tasks`）额外冗余 `fx_rate_micro` 快照列，即使本表行被归档也不影响历史复算。

---

### 5.15 `apigx_price_rules` —— PriceRule / PriceSnapshot（价格版本，不可变）

**用途**：「来源、原始记录、原币种、USD 价格、汇率快照、单位、倍率口径、条件、人工覆盖、生效时间」（计划书 §7 第 12 行）。§8.3 要求把分享者同步结果保存到**其有权管理的模型供给及必要的 Key 价格配置**，生成**不可变价格版本**；§8.4 要求「人工覆盖优先且锁定」。

**关联**：`scope_type`+`scope_id` → `apigx_model_offerings.id` / `apigx_upstream_keys.id` / `apigx_channels.id`；`fx_rate_id` → `apigx_fx_rates.id`；被 `apigx_price_rule_items` 引用；被 `apigx_model_offerings.price_rule_id`、`apigx_attempts.price_rule_id`、`apigx_usage_events.price_rule_id`、`apigx_async_tasks.price_rule_id` 引用。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `scope_type` | `ENUM` | 0=供给 / 1=上游 Key / 2=渠道 |
| `scope_id` | `FK` | |
| `version` | `BIGINT` | 该 scope 内单调递增的版本号 |
| `source` | `ENUM` | 0=人工覆盖 / 1=上游渠道报价 / 4=New API 本地配置；2/3/5/6 为历史保留值，不进入 APIGX 自动匹配 |
| `source_ref` | `STR(255)` | 来源 URL 或标识（不可信；出站前 SSRF 校验，展示转义） |
| `currency` | `CUR3` | 原币种 |
| `pricing_mode` | `ENUM` | 0=分项绝对单价 / 1=按次（`model_price`）/ 2=旧倍率兼容导入（§8.3 的换算表） |
| `legacy_ratios` | `TEXT` | JSON：旧倍率原文（`model_ratio`、`completion_ratio`、`image_ratio`、`audio_ratio` 等），**仅作兼容导入证据**，不参与运行时计算 |
| `fx_rate_id` | `FK` | → `apigx_fx_rates.id`；0=无需换算（币种为 USD） |
| `fx_rate_micro` | `MICRO` | **快照**：换算时使用的汇率；USD 来源时为 `1000000` |
| `usd_converted` | `BOOL` | 是否已换算到 USD |
| `manual_override` | `BOOL` | 是否人工覆盖（人工覆盖优先，同步不得改写，§8.4 第 6 点） |
| `locked` | `BOOL` | 人工覆盖锁定 |
| `conditions` | `TEXT` | JSON：条件（分辨率/质量档位/上下文阶梯/服务档位/是否 Batch/缓存条件/地区/档位），§8.4 第 4 点 |
| `validation_state` | `ENUM` | 0=待校验 / 1=已接受 / 2=已拒绝 / 3=冲突（§8.4 第 2 点「异常批次保留但不覆盖当前有效版本」） |
| `validation_note` | `STR(255)` | 脱敏说明 |
| `raw_record` | `TEXT` | 来源原始记录（含分页完整性标记；不可信） |
| `raw_record_bytes` | `INT` | 写入字节数（MySQL `text` 上限监控） |
| `created_by_user_id` | `FK` | 0=系统同步 |
| `effective_from` | `TS` | |
| `effective_to` | `TS` | 0=至今 |
| `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(scope_type, scope_id, version)`。
- **索引**：`index(scope_type, scope_id, effective_from)` 服务「取当前生效价格版本」；`index(source, effective_from)` 服务「按来源查看同步历史」；`index(validation_state, created_at)` 服务「待校验价格队列」。
- **软删除**：无。价格版本是历史证据，永不删除（§12「计价/授权快照保留至所引用记录到期」）。
- **不可变快照字段**：**除 `effective_to` 外，整行不可变**。`source`、`currency`、`fx_rate_id`、`fx_rate_micro`、`conditions`、`raw_record`、`effective_from`、`version` 全部一次写入永不修改。
- **唯一允许的变更**：`effective_to` 从 `0` 单向改为更大值（被新版本取代时封版一次），以及 `validation_state` / `validation_note` 由待校验推进为终态一次。新价格 = **新行 + `version+1`**，绝不原地改价。
- **绝不回写历史的字段**：整行，以及所有引用它的历史行（`apigx_attempts` / `apigx_usage_events` 里的 `unit_price_micro` / `currency` / `fx_rate_micro` / `cost_*` 都是快照）。
- **边界声明**（§8.3）：`/api/pricing` 的原生价格列表解析**不等于**导入分享者 Key 的实际组倍率；成本倍率必须对应实际 Key 的适用身份。`fx_rate_micro` 只是汇率快照，不代表渠道实付价。

---

### 5.16 `apigx_price_rule_items` —— 价格分项（派生子表，服务 §8.5）

**用途**：计划书 §8.5「估算基础为『各个不重叠计量分项的用量 × 对应单价之和』」，且「缓存 token 是否包含在输入总量中由适配器归一，避免重复计算」。分项如果不落表而塞进 JSON，就无法用 SQL 做「按请求数/按秒/按张」的聚合与对账，因此显式落表。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `price_rule_id` | `FK` | → `apigx_price_rules.id` |
| `dimension` | `ENUM` | 计量维度：0=输入 token / 1=输出 token / 2=缓存读 token / 3=缓存写 token / 4=推理 token / 5=图片张数 / 6=输入音频秒 / 7=输出音频秒 / 8=输出视频秒 / 9=请求次数 / 10=输入字符数 / 11=文档数 |
| `unit_scale` | `ENUM` | 单价计量单位：0=`/1M tokens` / 1=`/1K tokens` / 2=`/张` / 3=`/秒` / 4=`/分钟` / 5=`/次` / 6=`/1K 字符` / 7=`/文档` |
| `unit_price_micro` | `MICRO` | **原币种**绝对单价（micro 单位） |
| `currency` | `CUR3` | 原币种 |
| `usd_unit_price_micro` | `MICRO` | 换算后 USD 单价；未换算时为 0 |
| `usd_state` | `ENUM` | 0=已换算 / 1=待汇率 / 2=不适用（原币种即 USD） |
| `counted_in_dimension` | `INT` | 该分项是否已包含在另一个分项内（-1 = 独立分项）。用于表达「缓存 token 包含在输入总量中」，避免重复计费（§8.5） |
| `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(price_rule_id, dimension)`。
- **索引**：`index(dimension, currency)` 服务跨供给的横向价格对比；`index(usd_state)` 服务「待换算队列」（§8.5「缺少有效汇率时保留原币种金额，USD 金额标为待换算并计入未估算数量，不能当作 0 USD 汇总」）。
- **软删除**：无。
- **不可变快照字段**：全部（随父行一起不可变）。
- **绝不回写历史的字段**：全部。

**「价格未知」与「价格为零」分别存储**（§8.4 第 8 点）：本表**没有任何行** = 未知；有行且 `unit_price_micro = 0` = 明确零价。两者在业务层用不同的 `cost_state` 表达（见 §5.20）。**不允许**用「单价 0」表示未知。

---

### 5.17 `apigx_capacity_policies` —— CapacityPolicy

**用途**：「限制维度、配置版本」（计划书 §7 第 13 行）；§9.2 的全部容量维度与「未配置/禁用/继承默认/明确不限」四态（§3.3、`CODE_STYLE.md` 第 11 条）。

**关联**：`owner_user_id` → `users.id`；`scope_type`+`scope_id` 指向用户 / 平台 Key / 渠道 / 上游 Key / 上游账户池 / 供给。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `owner_user_id` | `FK` | 配置归属（服务端按登录用户校验，防越权改他人限额） |
| `scope_type` | `ENUM` | 0=用户 / 1=平台 Key / 2=渠道 / 3=上游 Key / 4=上游账户池 / 5=供给 |
| `scope_id` | `FK` | |
| `policy_version` | `BIGINT` | 配置版本；调度实时读取生效版本（§9.2「配置修改：调度实时读取生效版本」） |
| `max_concurrency_mode` / `max_concurrency` | `ENUM` / `BIGINT` | 最大并发请求数；**所有实例共享占用数，不能各自单独计算**（§9.2） |
| `rpm_mode` / `rpm_limit` | `ENUM` / `BIGINT` | 每分钟请求数 |
| `tpm_mode` / `tpm_limit` | `ENUM` / `BIGINT` | 每分钟 token 数 |
| `max_running_tasks_mode` / `max_running_tasks` | `ENUM` / `BIGINT` | 最大运行中异步任务数；**提交成功后持续占用到终态**（§9.2） |
| `daily_request_mode` / `daily_request_limit` | `ENUM` / `BIGINT` | 每日请求数 |
| `daily_budget_mode` / `daily_budget_micro` | `ENUM` / `MICRO` | 每日 USD 估算预算（§10.2「分享者可以限制每日参考成本或渠道成本预算」） |
| `max_input_tokens_mode` / `max_input_tokens` | `ENUM` / `BIGINT` | 单次输入上限 |
| `max_output_tokens_mode` / `max_output_tokens` | `ENUM` / `BIGINT` | 单次输出上限（**可执行的**，不能只是展示值，§9.2） |
| `max_image_side_mode` / `max_image_side` | `ENUM` / `BIGINT` | 图片边长上限 |
| `max_audio_seconds_mode` / `max_audio_seconds` | `ENUM` / `BIGINT` | 音频时长上限（秒） |
| `max_video_seconds_mode` / `max_video_seconds` | `ENUM` / `BIGINT` | 视频时长上限（秒） |
| `queue_mode` | `ENUM` | 0=不排队（满载即 429） / 1=有限短等待（§12 默认 0） |
| `queue_max_wait_ms` | `BIGINT` | 排队上限；`queue_mode=0` 时置 0 |
| `limit_timezone` | `STR(64)` | 日限额划分时区，默认 `Asia/Shanghai`（§8.5、§12） |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(scope_type, scope_id)` —— 一个范围一份当前策略；版本靠 `policy_version` 递增。
- **索引**：`index(owner_user_id, scope_type)` 服务配置管理界面；`index(scope_type, scope_id)` 已由唯一键覆盖。
- **软删除**：无。随 scope 生命周期；scope 软删除后策略行保留供审计。
- **不可变快照字段**：`scope_type`、`scope_id`、`created_at`。
- **绝不回写历史的字段**：**全部维度列**。§9.2「下调并发后停止新增，已有请求按既定策略结束」：已有请求/任务持有的是 `apigx_reservations.policy_version` 快照，**策略变更不得追溯改写已有预留或历史用量**。

---

### 5.18 `apigx_reservations` —— Reservation

**用途**：「并发或运行任务租约、RPM/TPM 与预算预留、归还和过期状态」（计划书 §7 第 13 行）。这是把「持久预算与请求状态由 PostgreSQL 事务维护，Redis 管并发/速率预留」（§9.2）中的**持久侧意图**落表。

**关联**：`request_id` → `apigx_requests.id`；`attempt_id` → `apigx_attempts.id`（0=请求级预留）；`async_task_id` → `apigx_async_tasks.id`；`capacity_policy_id` → `apigx_capacity_policies.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `reservation_uid` | `STR(64)` | 幂等 ID；归还、续租、对账全部靠它（§9.2「用唯一尝试 ID 保证归还幂等」） |
| `request_id` | `FK` | |
| `attempt_id` | `FK` | 0=请求级 |
| `async_task_id` | `FK` | 0=非任务 |
| `capacity_policy_id` | `FK` | |
| `policy_version` | `BIGINT` | **快照**：预留时的配置版本 |
| `scope_type` / `scope_id` | `ENUM` / `FK` | 冗余便于聚合与对账 |
| `dimension` | `ENUM` | 0=并发 / 1=RPM / 2=TPM / 3=运行任务 / 4=USD 预算 / 5=请求数 |
| `amount_micro` | `MICRO` | 预留额（USD 预算用 micro；计数类用整数×1e6 以统一类型） |
| `settled_amount_micro` | `MICRO` | **实际用量调整后的值**（追加，不覆盖 `amount_micro`） |
| `state` | `ENUM` | 0=已持有 / 1=已结算 / 2=已释放 / 3=已过期 / 4=对账中 |
| `cycle_key` | `STR(16)` | 准入日（`Asia/Shanghai` 的 `YYYY-MM-DD`）；**跨日完成的结算回到原预留周期**（§8.5） |
| `acquired_at` | `TS` | |
| `expires_at` | `TS` | 租约到期（长流用续租，§9.2 第 2 点） |
| `renewed_at` | `TS` | |
| `released_at` | `TS` | |
| `lease_owner_node` | `STR(128)` | 实例标识，用于进程异常后的恢复与对账 |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(reservation_uid)`；`uniqueIndex(request_id, dimension, scope_type, scope_id, attempt_id)` —— 防止同一请求对同一维度重复预留。
- **索引**：`index(state, expires_at)` 服务**对账/恢复扫描**（进程在步骤之间退出后的补偿，§9.2）；`index(scope_type, scope_id, cycle_key, dimension)` 服务**日额度累计**（`SUM(amount_micro)`）。
- **软删除**：无。按保留期（请求元数据 30 天）清理；清理在 `state` 已终态后进行。
- **不可变快照字段**：`reservation_uid`、`request_id`、`attempt_id`、`async_task_id`、`capacity_policy_id`、`policy_version`、`scope_type`、`scope_id`、`dimension`、`cycle_key`、`acquired_at`、`created_at`。
- **仅允许的单向推进**：`state`（`0 → {1,2,3,4}`）、`renewed_at`、`released_at`、`settled_amount_micro`（**只增不改**，保留首次估算的 `amount_micro` 作为证据）。
- **绝不回写历史的字段**：`amount_micro`、`cycle_key`、`policy_version`。**关键**：租约到期**不证明**上游已停止（§9.2）——`state` 必须先进入 `4 = 对账中`，查询或人工确认后才释放，不得直接判为已释放；超时仍记入服务质量降级。
- **注意**：本表是**持久侧**。Redis 侧的并发/速率计数器不在此表中；Redis 状态丢失或数据库写入失败时**暂停新准入**，恢复并对账后再开放（§9.2）。

---

### 5.19 `apigx_requests` —— Request

**用途**：「一次用户请求」的权威记录（计划书 §7 第 14 行前半；§8.5「一次用户调用对应一个 Request」）。

**关联**：`user_id` → `users.id`；`api_key_id` → `apigx_api_keys.id`；`token_id` → 底座 `tokens.id`；`canonical_model_id` → `apigx_canonical_models.id`；`idempotency_record_id` → `apigx_idempotency_records.id`；**与底座 `logs` 通过 `log_request_id` 关联**（`new-api/model/log.go:78` 的 `logs.request_id`）。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `request_uid` | `STR(64)` | 对外**脱敏**请求 ID（§9.1「返回脱敏请求 ID」）。建议由 `common.NewRequestId()`（`new-api/common/utils.go:281-283`）生成 |
| `log_request_id` | `STR(64)` | 与底座 `logs.request_id` 对齐，用于把平台明细与底座用量日志对上（**未核实**：底座是否允许外部预设该值，见 §9） |
| `user_id` | `FK` | 使用者 |
| `api_key_id` | `FK` | |
| `token_id` | `FK` | 影子令牌（快照） |
| `canonical_model_id` | `FK` | |
| `api_model_id` | `STR(191)` | **快照**：当时的对外标准 ID |
| `requested_model` | `STR(191)` | 客户端原始 `model` 字符串（不可信输入，存原文） |
| `endpoint` | `STR(128)` | 请求路径 |
| `protocol` | `STR(32)` | 客户端协议族 |
| `capability` | `ENUM` | 能力枚举（同 §5.7） |
| `route_mode` | `ENUM` | 0=指定来源 / 1=延迟优先 / 2=吞吐优先 / 3=可靠性优先 / 4=综合自动 |
| `pinned_offering_id` | `FK` | 客户端显式指定的供给；0=未指定 |
| `resolved_scope_kind` | `ENUM` | 权威路由决定来源：0=请求头显式指定 / 1=Key 固定来源 / 2=Key 默认偏好 / 3=系统默认 auto（§9.1 的有效选择顺序） |
| `allow_fallback` | `BOOL` | **快照**：本次是否允许回退（硬限制） |
| `admission_state` | `ENUM` | 0=已准入 / 1=容量不足拒绝 / 2=鉴权拒绝 / 3=参数冲突拒绝 / 4=预算拒绝 |
| `admission_cycle_key` | `STR(16)` | 准入日（§8.5） |
| `client_ip_hash` | `HASH64` | 脱敏 IP 摘要（不存原始 IP） |
| `idempotency_record_id` | `FK` | 0=非幂等请求 |
| `admission_at` | `TS` | 「提交准入完成」是「请求是否已接收」的边界（§10.3） |
| `started_at` / `first_output_at` / `completed_at` | `TS` | 延迟口径（§9.4「记录网关收取请求、排队、上游开始、首个有效输出和完成的时刻」） |
| `queue_ms` | `BIGINT` | 排队耗时（**计入用户总耗时**，§9.2 第 6 点） |
| `total_ms` | `BIGINT` | 总耗时 |
| `attempt_count` | `INT` | 实际上游尝试次数（上游请求数按 Attempt 计，§8.5） |
| `final_state` | `ENUM` | 0=成功 / 1=失败 / 2=已取消 / 3=结果未知 / 4=部分成功 |
| `failure_class` | `ENUM` | 用户请求级失败归因（§9.4 失败归因；把「平台自身拒绝/用户参数错误/用户主动取消」与上游故障分开） |
| `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(request_uid)`。**幂等唯一键不放在本表**：非幂等请求没有幂等键，若在本表建 `(user_id, api_key_id, idempotency_key_hash)` 唯一索引，空字符串会互相冲突。因此由 `apigx_idempotency_records` 承担（§5.26）。
- **索引**：`index(user_id, created_at)` 服务「我的请求列表」；`index(api_key_id, created_at)` 服务「按 Key 查请求」；`index(canonical_model_id, created_at)` 服务「按模型统计」；`index(log_request_id)` 服务与底座 `logs` 关联；`index(admission_state, admission_cycle_key)` 服务「按日统计拒绝原因」。
- **软删除**：无。按保留期（默认 30 天）清理（§12）。
- **不可变快照字段**：`request_uid`、`requested_model`、`api_model_id`、`canonical_model_id`、`route_mode`、`pinned_offering_id`、`resolved_scope_kind`、`allow_fallback`、`admission_cycle_key`、`admission_at`、`client_ip_hash`、`idempotency_record_id`、`created_at`。
- **仅允许的单向推进**：`started_at` / `first_output_at` / `completed_at` / `queue_ms` / `total_ms` / `attempt_count` / `final_state` / `failure_class` 从「未知」推进到终态一次（幂等 CAS）。
- **绝不回写历史的字段**：`api_model_id`、`canonical_model_id`（模型合并/别名调整不改写，§7）、`requested_model`、`route_mode`、`resolved_scope_kind`。**不记录请求正文**（§10.3「平台默认只记录请求元数据和用量，不保存提示词、输出文本或原始音视频」）。

---

### 5.20 `apigx_attempts` —— Attempt

**用途**：「每次上游尝试，渠道和 Key 策略、实际渠道及 Key ID、结果及耗时」（计划书 §7 第 14 行后半）。§6 架构约束要求「一次请求只有一个权威路由决定」「实际 Key 同样由该调度流程决定，不能在原生 `GetNextEnabledKey` 中再次抽选」——**本表就是这条不变量的落地证据**。

**关联**：`request_id` → `apigx_requests.id`；`offering_id` → `apigx_model_offerings.id`；`selected_channel_id` → `apigx_channels.id`；`base_channel_id` → 底座 `channels.id`；`upstream_key_id` → `apigx_upstream_keys.id`；`upstream_account_id` → `apigx_upstream_accounts.id`；`price_rule_id` → `apigx_price_rules.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `attempt_uid` | `STR(64)` | 幂等 ID（§9.2「用唯一尝试 ID 保证归还幂等」） |
| `request_id` | `FK` | |
| `attempt_index` | `INT` | 第几次上游尝试（第一版最多 2，§12「安全重试最多一次，即总共两次上游尝试」） |
| `selected_channel_id` | `FK` | **权威路由决定的实际渠道**（不是「候选池」） |
| `base_channel_id` | `FK` | 底座 `channels.id`（快照） |
| `offering_id` | `FK` | 供给（快照） |
| `upstream_key_id` | `FK` | **实际使用的 Key 稳定 ID**（不是池内下一把） |
| `upstream_key_credential_version` | `BIGINT` | **凭据版本快照** |
| `upstream_account_id` | `FK` | 上游账户池快照（共享限额归属） |
| `upstream_model_id` | `STR(191)` | **快照**：实际发给上游的模型 ID |
| `price_rule_id` | `FK` | **快照**：该次实际 Key 的价格版本（§11 验收「按实际执行 Key 的价格版本估算及预留，不借用池内最低价」） |
| `fx_rate_micro` | `MICRO` | **汇率快照** |
| `reservation_uid` | `STR(64)` | 容量预留的幂等引用 |
| `state` | `ENUM` | 0=待发送 / 1=已发送 / 2=成功 / 3=上游错误 / 4=超时 / 5=已取消 / 6=结果未知 |
| `failure_class` | `ENUM` | 0=无 / 1=上游超时 / 2=连接错误 / 3=上游 5xx / 4=上游鉴权失败 / 5=上游不可用 / 6=上游限流 / 7=平台拒绝 / 8=用户参数错误 / 9=用户取消（§9.4 失败归因） |
| `retry_reason` | `ENUM` | 0=无 / 1=Key 鉴权失效 / 2=Key 冷却 / 3=渠道故障 / 4=容量不足（换 Key 也属于上游重试，**共用同一重试预算**，§9.5） |
| `http_status` | `INT` | 上游 HTTP 状态（脱敏，不含响应体） |
| `started_at` / `first_output_at` / `completed_at` | `TS` | |
| `ttft_ms` | `BIGINT` | 首个**有效**输出延迟；心跳/空事件不算（§9.4，由适配器判定） |
| `generation_ms` | `BIGINT` | 生成时段耗时 |
| `output_tokens` | `BIGINT` | 适配器可见的输出 token（不可见时置 0 并在 `apigx_usage_events.trust` 标注） |
| `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(attempt_uid)`；`uniqueIndex(request_id, attempt_index)`。
- **索引**：`index(request_id, attempt_index)` 已由唯一键覆盖；`index(selected_channel_id, started_at)` 服务**质量桶聚合**（§9.4 按供给/渠道滚动窗口统计）；`index(upstream_key_id, started_at)` 服务**分享者按 Key 下钻**（§9.2「分享者可进一步下钻到各 Key 统计」）；`index(state, started_at)` 服务「结果未知」对账扫描。
- **软删除**：无。按保留期清理。
- **不可变快照字段**：**本表是全库最不可变的一张表**。`attempt_uid`、`request_id`、`attempt_index`、`selected_channel_id`、`base_channel_id`、`offering_id`、`upstream_key_id`、`upstream_key_credential_version`、`upstream_account_id`、`upstream_model_id`、`price_rule_id`、`fx_rate_micro`、`reservation_uid`、`retry_reason`、`started_at`、`created_at` 全部一次写入永不修改。
- **仅允许的单向推进**：`state`、`first_output_at`、`completed_at`、`ttft_ms`、`generation_ms`、`output_tokens`、`failure_class`、`http_status` 从「未知」推进到终态一次。
- **绝不回写历史的字段**：上述全部快照列。**关键**：Key 轮换、停用或移除后**不得**把历史 Attempt 改绑到池里的下一把 Key（§10.3）；Key 重试成功**不得**掩盖前一次失败（§9.2「对外质量统计保留每次上游尝试结果」）；超时**不得**改判为按时成功（§9.4）。

---

### 5.21 `apigx_usage_events` —— UsageEvent

**用途**：「分项用量、可信度、成本估算状态、价格快照与归属」（计划书 §7 第 15 行）。§8.5 要求「重复事件按稳定事件 ID 幂等处理；后续补到准确 usage 时追加校正事件，保留原估算和修订轨迹，不覆盖历史证据或重复扣预算」。

**关联**：`request_id` / `attempt_id` / `async_task_id` 三选一（按事件来源）；`price_rule_id` + `price_rule_item_id` → 价格版本与分项快照；`user_id`（使用者）与 `owner_user_id`（分享者）双归属。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `event_uid` | `STR(64)` | **稳定事件 ID**，幂等去重（§8.5） |
| `kind` | `ENUM` | 0=用量 / 1=调整 / 2=校正（校正指向原事件，不覆盖） |
| `correction_of_event_id` | `FK` | 0=非校正；校正事件指向被修订的原事件，形成修订轨迹 |
| `request_id` | `FK` | 0=非同步请求来源 |
| `attempt_id` | `FK` | 0=请求级或任务级 |
| `async_task_id` | `FK` | 0=非任务 |
| `user_id` | `FK` | 使用者（归属） |
| `owner_user_id` | `FK` | 分享者（分享者消耗视图的归属依据，§3.4） |
| `api_key_id` | `FK` | |
| `canonical_model_id` | `FK` | |
| `api_model_id` | `STR(191)` | **快照** |
| `offering_id` | `FK` | |
| `channel_id` / `upstream_key_id` / `upstream_account_id` | `FK` / `FK` / `FK` | 实际执行归属（**快照**） |
| `dimension` | `ENUM` | 计量维度（同 §5.16） |
| `quantity_milli` | `BIGINT` | 用量，**千分之一最小单位**（1 token = 1000；1 张 = 1000；1 秒 = 1000；1 次 = 1000），用整数表达小数秒 |
| `unit_scale` | `ENUM` | 该 `dimension` 的计量单位（同 §5.16） |
| `trust` | `ENUM` | 0=上游上报 / 1=本地估算 / 2=未知 / 3=不可得（§8.5「不伪装为实际计量」） |
| `price_rule_id` / `price_rule_item_id` | `FK` / `FK` | **价格快照**引用（0=未定价） |
| `unit_price_micro` | `MICRO` | **快照**：该分项单价（原币种） |
| `currency` | `CUR3` | **快照** |
| `fx_rate_micro` | `MICRO` | **快照** |
| `cost_micro` | `MICRO` | **快照**：原币种成本 |
| `cost_usd_micro` | `MICRO` | **快照**：USD 成本 |
| `cost_state` | `ENUM` | 0=参考成本已估 / 1=渠道成本已估 / 2=**未定价** / 3=待换算（缺汇率） / 4=**明确零价** |
| `cycle_key` | `STR(16)` | 结算归属周期（跨日结算回到原预留周期，§8.5） |
| `occurred_at` | `TS` | 事件时间（UTC） |
| `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(event_uid)`。
- **索引**：`index(request_id)`、`index(attempt_id)`、`index(async_task_id)` 服务「某请求/尝试/任务的用量明细」；`index(owner_user_id, occurred_at)` 服务**分享者消耗视图**；`index(channel_id, occurred_at)` 与 `index(upstream_key_id, occurred_at)` 服务渠道/Key 用量统计；`index(cost_state, occurred_at)` 服务**覆盖率统计**（§8.5「页面同时展示可估算总额、未估算请求数与覆盖率」）；`index(cycle_key, dimension)` 服务日预算对账。
- **软删除**：无。按保留期清理；价格与归属快照保留至所引用记录到期（§12）。
- **不可变快照字段**：**整行不可变**（除 `id` 外全部一次写入）。校正靠**追加新行**（`kind=2` + `correction_of_event_id`），不改原行。
- **绝不回写历史的字段**：全部成本、价格、汇率、归属、用量与可信度字段。
- **两条硬约束**：
  1. **「价格未知」不得累计成 0**：`cost_state=2` 的行 `cost_usd_micro` 必须为 0，同时进入「未估算数量」；统计页的「可估算总额」**不得**把这些行当 0 加进总额（§8.4 第 8 点、§8.5）。
  2. **流式中间 usage 与最终累计 usage 不相加**：由适配器区分后只落一条最终事件（§8.5「不能相加造成双计」）；如确有独立收费的查询操作，另记 `kind=0` 事件并注明。

---

### 5.22 `apigx_quality_buckets` —— QualityBucket

**用途**：「供给、能力、地区或部署点、时间桶、样本量、延迟及吞吐分布」（计划书 §7 第 16 行）。§9.4 要求「至少按『模型供给 × 能力 × 部署点』区分，并保留任务规格或输入输出长度分桶」「同时保存分钟桶，按所需精度聚合并标记更新时间」。

**关联**：`offering_id` → `apigx_model_offerings.id`。**不引用底座 `perf_metrics`**（维度不匹配，见 §2.6）。

> **已知偏差（需产品确认）**：计划书 §9.4 明确要求「延迟 P50」「吞吐中位数」「Wilson 置信下界」。用**纯 SQL 聚合列**只能得到均值与计数，分位数与置信下界需要 TDigest / HDR 直方图之类的近似结构，而 `TEXT` 直方图列在三库上无法直接被 SQL 聚合。本草案的做法是：**桶内存 sum/count/分位预计算列，分位数由写入侧（Go）用环形缓冲计算后写入**。**未核实**：底座的 `perf_metrics`（`new-api/model/perf_metric.go:11-23`）是否已有可复用的分位计算设施；若有，应复用它而不是自研。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `bucket_key_hash` | `HASH64` | `SHA-256(offering_id|capability|deploy_point|spec_bucket|window_kind|bucket_ts|aggregator_version)`；进唯一索引 |
| `offering_id` | `FK` | 供给 |
| `capability` | `ENUM` | 能力 |
| `deploy_point` | `STR(64)` | 地区/部署点；无则为空串 |
| `spec_bucket` | `STR(32)` | 任务规格或输入/输出长度分桶键；空串=不适用（§9.4） |
| `window_kind` | `ENUM` | 0=分钟桶 / 1=小时桶（6 小时窗口由分钟桶聚合，§9.4） |
| `bucket_ts` | `TS` | 桶起始时间（UTC epoch 秒） |
| `aggregator_version` | `BIGINT` | 指标配置版本；Key 模式/可用 Key 集合/定价配置明显变化时新建版本，避免失效 Key 的旧数据代表当前池（§9.3） |
| `natural_request_count` | `BIGINT` | 自然调用样本数 |
| `probe_count` | `BIGINT` | 主动探测样本数（**与自然调用分别统计**，§9.4） |
| `complete_success_count` | `BIGINT` | **完整成功**数（同步正确结束 / 流式完整结束 / 异步终态成功；HTTP 200 或「任务已创建」不算，§9.4） |
| `timeout_count` | `BIGINT` | 超时事件数（超时进入窗口，不因迟到结果改判，§9.4） |
| `upstream_failure_count` | `BIGINT` | 上游故障数 |
| `cancelled_count` | `BIGINT` | 用户取消（**单列**，§9.4） |
| `platform_reject_count` | `BIGINT` | 平台自身拒绝（**单列**，§9.4） |
| `latency_ms_sum` / `latency_ms_p50` | `BIGINT` / `BIGINT` | 总耗时分布 |
| `ttft_ms_sum` / `ttft_ms_p50` | `BIGINT` / `BIGINT` | 首字延迟分布 |
| `output_tokens_sum` / `generation_ms_sum` | `BIGINT` / `BIGINT` | 文本吞吐（有效输出 token/s，§9.4） |
| `sample_ok_count` | `BIGINT` | 合格尝试总数（可靠性分母，§9.4） |
| `reliability_lower_bound_ppm` | `BIGINT` | 可靠性置信下界（百万分之一）；样本不足时置 0 并由 `natural_request_count` 判定「样本不足」 |
| `updated_at` | `TS` | 标记更新时间（§9.4） |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(bucket_key_hash)`。
  > `bucket_key_hash` 存在的理由：`deploy_point STR(64)` + `spec_bucket STR(32)` 在 utf8mb4 下合计最坏 384 字节，叠加 `offering_id`(8) + `capability`(4) + `window_kind`(4) + `bucket_ts`(8) + `aggregator_version`(8) = 416 字节，虽在 `DYNAMIC` 行格式的 3072 字节内，但超出 `COMPACT` 的 767 字节边界。用固定 64 字节摘要可完全绕开该风险（§3.5）。
- **索引**：`index(offering_id, capability, window_kind, bucket_ts)` 服务**滚动窗口查询**（§9.4 的 `[当前时间-6h, 当前时间)`）；`index(bucket_ts)` 服务保留期清理与按时间范围扫描；`index(aggregator_version, bucket_ts)` 服务版本切换时的混合状态识别。
- **软删除**：无。按聚合保留期（默认 180 天）清理。
- **不可变快照字段**：桶身份列全部不可变：`offering_id`、`capability`、`deploy_point`、`spec_bucket`、`window_kind`、`bucket_ts`、`aggregator_version`。
- **仅允许的变更**：计数/求和列**只允许原子累加**，不允许覆盖式写回（写法仿底座 `new-api/model/perf_metric.go:33-48` 的 `clause.OnConflict` + `gorm.Expr("col + ?")`）；`reliability_lower_bound_ppm` 由重算过程覆盖。
- **绝不回写历史的字段**：桶身份列。**关键**：
  - 迟到结果进入其**事件时间**所在的桶，**不改写其他桶**；超时结果**不改成按时成功**（§9.4）。
  - 异步任务最终状态按**结束时间**进入窗口，运行中任务显示 pending；**不提前计成成功或失败**（§9.4）。
  - **没有样本的区间显示「无数据」，不补零、不沿用旧的 100%**（§9.4）。
  - 新渠道显示「样本不足」，**不是 0% 或 100% 可靠**（§9.3、§9.4）。

---

### 5.23 `apigx_async_tasks` —— AsyncTask

**用途**：「平台任务 ID、上游任务 ID、用户、原渠道与 Key/凭据版本、分享者与权限版本快照、价格版本、状态、结果引用」（计划书 §7 第 17 行）。§10.3 的生命周期表要求这些快照在轮换/撤权/封禁后**仍能定位原任务**。

**关联**：`base_task_id` → 底座 `tasks.id`（影子任务，复用底座轮询与结算链路）；`channel_id` / `upstream_key_id` / `upstream_account_id` / `share_group_id` / `owner_user_id` / `price_rule_id` 均为**快照**；`result_ref_id` → `apigx_upstream_object_refs.id`。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `public_task_id` | `STR(64)` | 平台任务 ID（对外；底座同类 ID 形如 `task_<32 字符>`，`new-api/model/task.go:188-191`） |
| `base_task_id` | `FK` | 底座 `tasks.id` |
| `upstream_task_id` | `STR(191)` | 上游任务 ID（**私有，不出公共 DTO**，§10.3） |
| `user_id` | `FK` | 任务所有者（历史访问授权来自创建时的所有权，§10.3） |
| `api_key_id` | `FK` | |
| `offering_id` | `FK` | 原供给 |
| `channel_id` | `FK` | **原渠道快照**——后续查询/取消/下载**不参与普通轮询**（§9.2） |
| `upstream_key_id` | `FK` | **原 Key 快照** |
| `upstream_key_credential_version` | `BIGINT` | **凭据版本快照**——轮换后原版本只为绑定任务保留必要的查询/取消能力（§10.3） |
| `upstream_account_id` | `FK` | 上游账户池快照 |
| `share_group_id` | `FK` | **分享分组快照** |
| `owner_user_id` | `FK` | **分享者快照** |
| `grant_version` | `BIGINT` | **权限版本快照** |
| `policy_version` | `BIGINT` | **容量策略版本快照** |
| `price_rule_id` | `FK` | **价格版本快照** |
| `fx_rate_micro` | `MICRO` | **汇率快照** |
| `capacity_policy_id` | `FK` | |
| `reservation_uid` | `STR(64)` | 运行任务槽位的预留引用（**提交成功后持续占用到终态**，§9.2） |
| `action` | `STR(40)` | 任务类型（对齐底座 `tasks.action`，`new-api/model/task.go:60`） |
| `capability` | `ENUM` | 能力 |
| `status` | `ENUM` | 0=待提交 / 1=运行中 / 2=成功 / 3=失败 / 4=已取消 / 5=结果未知 / 6=对账中（§9.2：超时/断网/租约到期先进入 `5/6`，查询或人工确认后才释放） |
| `status_reason` | `STR(128)` | 脱敏原因码 |
| `client_disconnected` | `BOOL` | 客户端断连；**提交响应结束 ≠ 释放槽位**（§9.2） |
| `submit_at` / `start_at` / `finish_at` / `timeout_at` | `TS` | 超时线用于质量窗口（§9.4「超过适配器服务时限且结果仍未知的尝试，以超时事件进入服务质量窗口」） |
| `billing_settled` | `BOOL` | 是否已形成一份任务用量结算（§8.5「异步任务提交、轮询和终态只能形成一份任务用量结算」） |
| `settled_usage_event_id` | `FK` | 结算指向的 `apigx_usage_events.id`；`billing_settled=1` 时非 0 |
| `idempotency_record_id` | `FK` | 幂等创建记录 |
| `poll_failures` | `INT` | 连续无法识别的轮询结果计数 |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(public_task_id)`；`uniqueIndex(base_task_id)`。
- **索引**：`index(user_id, status, created_at)` 服务「我的任务列表」；`index(status, timeout_at)` 服务**超时扫描**；`index(channel_id, status)` 服务分享者侧任务统计；`index(upstream_key_id, status)` 服务「Key 轮换/停用/移除时提示对未完成任务的影响」（§9.2）。
- **软删除**：无。按任务生命周期与保留期处理；**尚未结束的任务及其必要凭据引用不提前清理**（§12）。
- **不可变快照字段**：`public_task_id`、`base_task_id`、`upstream_task_id`、`user_id`、`channel_id`、`upstream_key_id`、`upstream_key_credential_version`、`upstream_account_id`、`share_group_id`、`owner_user_id`、`grant_version`、`policy_version`、`price_rule_id`、`fx_rate_micro`、`action`、`submit_at`。
- **仅允许的单向推进**：`status`、`status_reason`、`start_at`、`finish_at`、`client_disconnected`、`billing_settled`、`settled_usage_event_id`、`poll_failures`、`result_ref_id`。
- **绝不回写历史的字段**：上述全部快照列。**关键**：
  - **不随机换 Key**：只有适配器确认可共享时才允许换 Key（§9.2）；默认把历史任务改绑到池里的下一把 Key 是**禁止**的。
  - **不跨用户/账户读取任务**：`upstream_task_id` 不得用于跨用户访问（§9.1「猜到上游 ID 不能跨用户访问」）。
  - **不重复创建任务**：结果未知的图片/视频任务优先查询原任务，不在另一渠道重提（§9.5）。
  - 分享者普通暂停/撤权的语义按 §10.3 生命周期表：新请求停止，已提交任务在结果保留期内仍可被**其所有者**查询/取消/读取结果，但**不能创建衍生生成任务**。

---

### 5.24 `apigx_upstream_object_refs` —— UpstreamObjectRef

**用途**：「用户、平台对象 ID、上游对象 ID、对象类型、原渠道/Key/账户、访问期限；用于文件、会话、缓存和任务产物」（计划书 §7 第 18 行）。§9.1 要求「文件、会话、缓存和异步任务只接受属于当前用户的平台代理对象引用」。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `ref_uid` | `STR(64)` | **对外平台对象 ID**（客户端只用它，永不用上游 ID） |
| `user_id` | `FK` | 所有者（**访问校验的唯一依据**） |
| `object_type` | `ENUM` | 0=文件 / 1=会话 / 2=缓存 / 3=任务产物 |
| `upstream_object_id` | `STR(191)` | 上游对象 ID（**私有，不出公共 DTO**） |
| `channel_id` | `FK` | **原渠道快照** |
| `upstream_key_id` | `FK` | **原 Key 快照** |
| `upstream_key_credential_version` | `BIGINT` | **凭据版本快照** |
| `upstream_account_id` | `FK` | 上游账户池快照 |
| `offering_id` | `FK` | 0=不属于特定供给 |
| `storage_backend` | `ENUM` | 0=仅上游 / 1=平台私有对象存储 |
| `storage_uri` | `STR(512)` | 平台私有存储路径（**不含任何凭据**） |
| `access_policy` | `ENUM` | 0=仅所有者鉴权访问 / 1=允许签名下载 |
| `signed_url_ttl_seconds` | `BIGINT` | 签名下载有效期上限；默认 ≤ 600 秒（§12「签名下载若启用最长 10 分钟且可撤销」） |
| `size_bytes` | `BIGINT` | |
| `mime_type` | `STR(128)` | 不可信输入，响应时按白名单处理 |
| `content_sha256` | `HASH64` | 内容摘要（完整性；不用于鉴权） |
| `state` | `ENUM` | 0=有效 / 1=已过期 / 2=已清理 / 3=已撤销 |
| `expires_at` | `TS` | 访问期限 |
| `purge_after` | `TS` | 清理时间（终态后默认 24 小时，§12） |
| `created_at` / `updated_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(ref_uid)`；`uniqueIndex(channel_id, object_type, upstream_object_id)` —— 同一渠道内同类型上游对象只登记一条，防止跨用户重复登记导致归属歧义。
- **索引**：`index(user_id, object_type, state)` 服务「我的文件/会话/缓存列表」与**对象归属校验**（§9.1）；`index(state, purge_after)` 服务清理扫描；`index(expires_at, state)` 服务过期扫描。
- **软删除**：无。用 `state`（含 `2=已清理`）表达生命周期，保留「曾经存在过什么」的证据。
- **不可变快照字段**：`ref_uid`、`user_id`、`object_type`、`upstream_object_id`、`channel_id`、`upstream_key_id`、`upstream_key_credential_version`、`upstream_account_id`、`created_at`。
- **仅允许的单向推进**：`state`、`expires_at`、`purge_after`、`signed_url_ttl_seconds`、`access_policy`。
- **绝不回写历史的字段**：上游 ID 与凭据归属列。**关键**：凭据轮换或移除后**不得**改绑 `upstream_key_id`；`access_policy` 收紧**不得**使已发出的签名下载失效之外的语义变化（签名下载本身可撤销，§12）。

---

### 5.25 `apigx_audit_events` —— AuditEvent

**用途**：「分享配置、权限、价格、凭据轮换与管理员操作的**脱敏**记录」（计划书 §7 第 19 行）。§10.3 与 `new-api/AGENTS.md` 的认证安全条款要求「审计事件不得含凭据」。

**关联**：`actor_user_id` / `actor_api_key_id` 指向操作者；`target_type`+`target_id` 指向被操作对象；`request_id` 与底座 `logs.request_id`（`new-api/model/log.go:78`）及 `apigx_requests.request_uid` 对齐。

> **为什么不写底座 `audit_logs`**：见 §2.6。补充理由——`AuditLog` 的结构里已有 `token_ref` 这类「凭据指纹」概念（`new-api/model/audit_log.go:25` 注释：「TokenRef identifies a PAT generation without storing its bearer credential」），可以借鉴其**只存指纹不存凭据**的做法，但表的归属与生命周期仍应独立。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `event_uid` | `STR(64)` | 事件幂等 ID |
| `actor_user_id` | `FK` | 0=系统 |
| `actor_role` | `ENUM` | **快照**：事件发生时的角色（不随后续改角色而变） |
| `actor_api_key_id` | `FK` | 0=非 Key 操作 |
| `actor_credential_ref` | `HASH64` | 操作者凭据的**指纹**（绝不是凭据本身） |
| `category` | `STR(24)` | `share_config` / `access` / `price` / `credential_rotation` / `admin` / `retention` |
| `action` | `STR(64)` | 动作码 |
| `target_type` | `STR(32)` | 目标类型 |
| `target_id` | `FK` | 目标稳定 ID |
| `target_public_id` | `STR(64)` | 目标对外标识（**不用名称**，改名不影响可读性） |
| `before_digest` / `after_digest` | `HASH64` | 变更前后摘要（脱敏，可比较但不可逆） |
| `change_summary` | `TEXT` | 字段级变更摘要（**脱敏；绝不含密码、验证码、恢复码、私钥、可用会话或认证令牌、上游 Key 明文**） |
| `request_id` | `STR(64)` | 关联请求 |
| `ip_hash` | `HASH64` | 脱敏 IP 摘要 |
| `user_agent` | `STR(255)` | |
| `occurred_at` / `created_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(event_uid)`。
- **索引**：`index(actor_user_id, occurred_at)` 服务「某人的操作记录」；`index(target_type, target_id, occurred_at)` 服务「某对象的历史变更」；`index(category, occurred_at)` 服务按类别审计。
- **软删除**：无。审计保留期长于请求元数据。
- **不可变快照字段**：**整行不可变**。
- **绝不回写历史的字段**：整行。**并且本表自身绝不允许写入凭据**（`new-api/AGENTS.md`「Authentication audit events MUST exclude passwords, verification codes, recovery codes, private keys, and usable session or authentication tokens」）。

---

### 5.26 `apigx_idempotency_records` —— 幂等记录（派生表，服务 §9.5）

**用途**：§9.5「支持的创建端点以『用户 + 平台 Key 授权范围 + 幂等键 + 请求摘要』识别同一次创建，同键不同参数返回冲突；请求摘要仅作校验，不保存明文内容」。

| 字段 | 类型 | 用途 |
|---|---|---|
| `id` | `ID` | |
| `user_id` | `FK` | |
| `api_key_id` | `FK` | |
| `operation` | `STR(32)` | 操作类型（如 `chat` / `image` / `audio.speech` / `video.create` / `file.upload`） |
| `idempotency_key_hash` | `HASH64` | 客户端幂等键的摘要 |
| `request_digest` | `HASH64` | 请求摘要（**仅作校验，不保存明文内容**） |
| `state` | `ENUM` | 0=进行中 / 1=已完成 / 2=同键不同参数冲突 / 3=上游结果未知 |
| `request_id` | `FK` | 0=未生成请求 |
| `async_task_id` | `FK` | 0=非任务 |
| `response_ref` | `STR(255)` | 结果引用（**不含凭据**） |
| `created_at` / `completed_at` | `TS` | |

- **主键**：`id`。
- **唯一约束**：`uniqueIndex(user_id, api_key_id, operation, idempotency_key_hash)` —— 并发下由数据库唯一键保证只有一次创建成功；随后比对 `request_digest`，不同则返回**冲突**（`state=2`）。
- **索引**：`index(state, created_at)` 服务「进行中/未知」的对账扫描。
- **软删除**：无。按请求元数据保留期清理。
- **不可变快照字段**：`user_id`、`api_key_id`、`operation`、`idempotency_key_hash`、`request_digest`、`created_at`。
- **绝不回写历史的字段**：全部身份与摘要列。
- **边界声明**（§9.5）：**上游不支持幂等或结果无法查询时，状态保留为「未知」并要求查询/人工处理**，不承诺凭平台幂等键就能消除上游重复执行。`state=3` 的行不得被自动重试改写为「已完成」。

---

### 5.27 `apigx_platform_settings` —— 平台级设置（派生表）

**用途**：承载平台级可配置默认值（保留期天数、默认时区、签名下载 TTL 上限、冷启动流量比例、自动路由权重、探测预算等，见计划书 §12 的开发基线表）。

> **为什么不写底座 `options`**：底座 `options` 表由 `Option{Key, Value}` 两张列构成（`new-api/model/option.go:21-24`），主键迁移逻辑在 `new-api/model/option_primary_key_migration.go:20-44`；其内存映射由 `InitOptionMap()`（`new-api/model/option.go:33`）按**已知键白名单**构建。**未核实**：未注册的键是否会被保留并回写。为避免与上游加载语义耦合、也避免为新增设置修改上游文件，平台设置另建表。

| 字段 | 类型 | 用途 |
|---|---|---|
| `setting_key` | `STR(128)`（**主键**，`gorm:"primaryKey;autoIncrement:false"`） | 键名，避开保留字 `key`（§3.1） |
| `setting_value` | `TEXT` | 值（JSON 或标量字符串） |
| `value_version` | `BIGINT` | 值版本（配置变更用版本传播，§10.3） |
| `updated_by_user_id` | `FK` | 0=系统默认 |
| `updated_at` | `TS` | |

- **主键**：`setting_key`。
- **唯一约束**：主键即唯一。
- **索引**：无额外索引（按主键点查）。
- **软删除**：无。删除即移除键。
- **不可变快照字段**：无。
- **绝不回写历史的字段**：**全部设置项**。保留期、时区、权重等配置变更**只影响未来行为**，不得追溯改写历史计量、历史周期归属或历史质量桶。

---

## 6. 与底座现有表的关联与新增外键

### 6.1 关联总表

| 底座表 | 我们的关联方式 | 需要给底座表加列吗 |
|---|---|---|
| `users` | `apigx_user_profiles.user_id`、`apigx_api_keys.user_id`、`apigx_share_groups.owner_user_id`、`apigx_channels.owner_user_id`、`apigx_upstream_accounts.owner_user_id`、`apigx_capacity_policies.owner_user_id`、`apigx_grants.subject_id`、`apigx_requests.user_id`、`apigx_usage_events.user_id/owner_user_id`、`apigx_async_tasks.user_id/owner_user_id`、`apigx_audit_events.actor_user_id` | **否** |
| `tokens` | `apigx_api_keys.token_id`（唯一）+ 每个平台 Key 一行影子令牌；`apigx_requests.token_id` 快照 | **否** |
| `channels` | `apigx_channels.base_channel_id`（唯一）+ 每个分享渠道一行影子渠道；`apigx_attempts.base_channel_id` 快照 | **否** |
| `abilities` | 影子渠道的模型能力行由底座在渠道保存时维护（`new-api/model/ability.go:18-26`）；**APIGX 的路由不依赖 `abilities`**，仅用于让底座原生链路可用 | **否** |
| `tasks` | `apigx_async_tasks.base_task_id`（唯一）+ 每个平台异步任务一行影子任务 | **否** |
| `logs` | `apigx_requests.log_request_id` ↔ `logs.request_id`（`new-api/model/log.go:78`）。**弱关联**：`logs` 可能位于独立的日志库甚至 ClickHouse（`new-api/model/main.go:230-272`、`:395-410`），因此**不得**依赖它与主库做 JOIN 或事务 | **否** |
| `options` | **不写**。平台设置走 `apigx_platform_settings`（§5.27）。特别地：**绝不调用 `PATCH /api/option/model_pricing` 修改全站模型价格**（§8.3「普通分享者不能借同步接口修改全站模型价格」） | **否** |
| `perf_metrics` | **不复用**（维度不匹配，§2.6）。APIGX 质量统计走 `apigx_quality_buckets` | **否** |
| `audit_logs` | **不写**（§2.6、§5.25）。运维若需要统一视图，做只读聚合，不做双写 | **否** |
| `models` / `vendors` | `apigx_canonical_models.vendor_id` 软引用 `vendors.id`；**不与 `models` 双向同步**（第一版） | **否** |
| `subscription_plans` / `top_ups` / `redemptions` | **不使用**。计划书明确不做充值/交易；免费模式改的是结算路径而不是数值（§6、`CODE_STYLE.md` 第 12 条） | **否** |
| `quota_data` | **不写**。APIGX 的聚合走 `apigx_quality_buckets` 与 `apigx_usage_events` | **否** |

> 上表「是否需要加列」一列**全部为「否」**，这是本草案的一个刻意选择：底座 `channels` / `tokens` / `tasks` 三个扩展点各自通过我们表里的 `base_*_id` 唯一列**反查**（`uniqueIndex(base_channel_id)` / `uniqueIndex(token_id)` / `uniqueIndex(base_task_id)`），无需在底座表上开反向指针列。好处是**底座表结构零改动**，上游 `AutoMigrate`（`new-api/model/main.go:337-373`）不会与我们的列发生任何 schema 协商，升级冲突面降到最小。

### 6.2 唯一的例外：平台 API Key 的哈希查找（必须做的一次决策）

如 §2.4 所述，底座令牌查找是明文等值查询（`new-api/model/token.go:290`），因此「只存摘要」无法仅靠新增表达成。两个可选方案：

| 方案 | 做法 | 代价 | 评估 |
|---|---|---|---|
| **A（推荐）** | 在鉴权入口增加一个**哈希回退分支**：先按提交值等值查（保持上游行为不变），未命中时再按 `SHA-256(提交值)` 十六进制查一次 | 需要修改上游文件（`new-api/model/token.go` 的 `GetTokenByKey`，或 `middleware/auth.go` 的取 key 流程），**必须先登记到 `docs/stage0/upstream-diff.md`**（`upstream-diff.md` §一「先登记再改」） | 满足 §10.3「保存安全校验摘要而非可恢复明文」；摘要为 64 位十六进制，天然不含 `-`，与 `middleware/auth.go:303-304` 的切分逻辑兼容；`tokens.key` 列宽 `varchar(128)`（`new-api/model/token.go:17`）放得下 |
| **B（阶段 1 降级）** | `tokens.key` 存原密钥（底座现状），`apigx_api_keys.key_hash` 只作二次校验 | **不满足** §10.3；数据库泄露即可直接冒用所有平台 Key | 只能在「先打通链路、后续补」时短期使用，必须作为技术债显式登记，并在阶段 1 验收前消除 |

**建议**：采用方案 A，并在接入时**先写上游差异台账再改代码**。方案 A 的具体插入点、以及它对 `middleware/auth.go:385-405` 第二处取 key 分支的影响，需要在实际编码时用三库实例验证（本文档**未执行**）。

---

## 7. 迁移与兼容方案

### 7.1 迁移顺序

底座现有顺序（`new-api/model/main.go:320-393`，`migrateDB()`）：

```
migrateTokenKeyUniqueness      → 改 tokens.key 唯一约束
migratePrefillGroupUniqueness  → 改 prefill_groups 唯一索引
migrateSubscriptionPlanPriceAmount
migrateTokenModelLimitsToText
migrateOptionPrimaryKey
DB.AutoMigrate(底座 34 个模型)
InitializeUserAuthVersions / InitializeExternalIdentityClaims
（SQLite）ensureSubscriptionPlanTableSQLite  或  （其它）AutoMigrate(&SubscriptionPlan{})
```

**我们把 `apigx_*` 的 `AutoMigrate` 追加在 `DB.AutoMigrate(底座模型…)` 成功返回之后**（即 `new-api/model/main.go:373` 之后），理由是：底座的 `users` / `tokens` / `channels` / `tasks` 必须先存在，影子行才有引用目标。

平台表的建表顺序（按引用依赖，4 个批次）：

| 批次 | 表 | 依赖前置 |
|---|---|---|
| P1 | `apigx_fx_rates`、`apigx_canonical_models`、`apigx_upstream_accounts`、`apigx_idempotency_records`、`apigx_platform_settings` | 无（仅依赖底座 `users` / `vendors`） |
| P2 | `apigx_user_profiles`、`apigx_share_groups`、`apigx_capacity_policies` | P1 + 底座 `users` |
| P3 | `apigx_upstream_keys`、`apigx_key_model_capabilities`、`apigx_channels` | P2（+ 底座 `channels`） |
| P4 | `apigx_price_rules`、`apigx_price_rule_items`、`apigx_model_aliases`、`apigx_model_offerings` | P1–P3 |
| P5 | `apigx_access_policies`、`apigx_grants`、`apigx_invites`、`apigx_api_keys` | P2–P4（+ 底座 `tokens`） |
| P6 | `apigx_requests`、`apigx_attempts`、`apigx_reservations`、`apigx_upstream_object_refs` | P4–P5（+ 底座 `tasks` 在 P6 之后由 `apigx_async_tasks` 使用） |
| P7 | `apigx_async_tasks`、`apigx_usage_events`、`apigx_quality_buckets`、`apigx_audit_events` | P6 |

> 批次只表达**逻辑依赖与代码组织**。`GORM AutoMigrate` 的调用可以一次传入全部模型；顺序之所以仍然写明，是为了：① 代码评审时能判断漏了谁；② 未来若引入数据库级外键约束，顺序直接可用；③ 数据回填脚本可以按批次单独重跑。

**迁移之后才允许执行的动作**（顺序不可颠倒）：

1. 建表 + 索引（`AutoMigrate`）。
2. **数据回填**（仅升级情形需要，见 §7.4）。
3. 写 `apigx_platform_settings` 默认值（幂等 upsert）。
4. 打开准入开关（默认关闭，见 §7.5）。

### 7.2 幂等策略

| 层次 | 做法 | 依据 |
|---|---|---|
| 建表 / 加索引 | 依赖 `AutoMigrate` 的既有 schema 探测：它先比较再决定是否 DDL。**不手写无条件 `CREATE TABLE`** | `new-api/model/main.go:337-373` |
| 需要手写 SQL 时 | 一律先探测再执行：`Migrator().HasTable`（`new-api/model/main.go:589`、`:644`）、`Migrator().HasColumn`（`:593`）、`Migrator().GetIndexes`（`new-api/model/option_primary_key_migration.go:47`） | 上游既有模式 |
| 列类型变更 | SQLite **不支持** `ALTER COLUMN`，必须早退（`new-api/model/main.go:581-582`、`:635-638`）。我们**不设计任何列类型变更**，因此不需要这条路径 | 上游既有约束 |
| 数据回填 | 每条回填语句用「自然键 + `clause.OnConflict{DoNothing}`」或「条件 UPDATE + `RowsAffected` 判定」，可重复执行 | 仿 `new-api/model/system_instance.go:61-69` |
| 默认设置写入 | `clause.OnConflict{DoNothing}`，只在键不存在时插入 | 同上 |
| 一次性的临界区操作 | 若真需要，用上游已有的加锁模式（MySQL `GET_LOCK` / PG `pg_advisory_xact_lock` / 其它走事务）——但**本草案不需要**，因为不涉及重建表或改唯一约束 | `new-api/model/option_primary_key_migration.go:82-116` |
| 幂等验证方式 | 连续启动两次，第二次**不应产生任何 DDL**（把 GORM 切到 `Debug` 观察 SQL）。这一点必须在三库上各验一次 | `docs/stage0/base-lock.md` §6 的三库要求、`new-api/AGENTS.md` |

**注意一致性风险**：`migrateDB()` 只在**主节点**执行（`new-api/model/main.go:215-217`），从节点假定表已存在。因此**多实例部署时必须先让主节点完成迁移再放流量**，否则从节点会在表不存在时报错。

### 7.3 情形一：全新数据库

1. 底座第一次启动：`InitDB` → `migrateDB()` 创建全部底座表并建 root 用户（`new-api/model/main.go:70-91` 的 `createRootAccountIfNeed` 与 `:337-373`）。
2. 紧接着执行我们的 `apigx_*` `AutoMigrate`（§7.1 顺序）。因为所有表都是新建的，**唯一索引不会遇到历史数据冲突**，这是最简单的情形。
3. 写入 `apigx_platform_settings` 默认行（保留期 30/180 天、`Asia/Shanghai`、签名下载 ≤600 秒、冷启动流量比例、自动路由初始权重等，取自计划书 §12 的开发基线表）。使用 `OnConflict{DoNothing}`。
4. 不需要任何回填。
5. 三库各自验证：全新 SQLite 文件、全新 MySQL 库、全新 PostgreSQL 库各启动两次，确认第二次无 DDL 产出。

**SQLite 特有提醒**：上游为一张表（`subscription_plans`）走了手写 DDL + `PRAGMA table_info` 加列（`new-api/model/main.go:499-576`），原因是该表涉及 SQLite 不友善的类型协商。**本草案刻意避开这条路径**：不使用 `decimal` 列、不使用 `ALTER COLUMN`、不使用部分索引，因此**不需要**为 SQLite 写专用建表分支。若实现过程中发现某一列类型被 SQLite 拒绝，正确做法是改列类型（回到 §3.2 的别名表），而不是给 SQLite 加专用 DDL。

### 7.4 情形二：从底座现有库升级

前提：库里已有 `users` / `tokens` / `channels` / `logs` / `options` / `tasks` / `perf_metrics` 等底座表与真实数据。

1. **升级前强制检查**（放在我们迁移函数的最前面，参考 `ensureUserQuotaColumns` 的位置与风格，`new-api/model/main.go:279-306`）：
   - `users` / `tokens` / `channels` / `tasks` 四张表存在（不存在说明不是可升级的底座库，直接报错拒绝启动）；
   - `users.quota` 等钱包列为 64 位（底座自己会在 `InitDB` 早期检查并拒绝 32 位 schema，见 `new-api/model/main.go:204-206`、`:279-306`）；
   - 迁移涉及的底座表名与我们的 `apigx_*` 不冲突（§2.5 的同名前缀规则）。
2. **加表不加列**：**底座表结构零改动**（§6.1）。升级风险因此集中在「新表创建」与「数据回填」两件事上。
3. **数据回填的策略（默认 fail-closed）**：
   - **不自动把底座既有 `channels` 纳入分享**。底座既有渠道多半是管理员自用渠道（含管理员自己的上游凭据），自动纳管会把管理员的私密资源变成潜在分享资源。只有经 APIGX 侧显式创建的渠道才会有 `apigx_channels` 行。
   - **不自动把底座既有 `tokens` 纳入平台 Key**。既有 token 没有 `scope_mode` / `allowed_model_ids` / `allow_fallback` 这些硬限制语义，自动纳管等于**凭空扩大授权**。它们保持「未纳管」状态，在 APIGX 准入链路上**默认拒绝**（§9.2「无法安全判断时，默认停止新增调用并返回明确错误」）。
   - 若产品决定要纳管存量资源，必须走一个**显式的、需要分享者逐个确认的回填流程**：为每个候选项生成 `status=0 待审核` 的行，由所有者确认后才能进入 `published`。回填脚本以底座行 `id` 为自然键，用 `OnConflict{DoNothing}` 保证可重跑。
4. **回填必须可中断、可重跑、分批**：每批一个事务，记录批次进度到 `apigx_platform_settings`（键如 `migration.backfill.batch`），出现中断时从上次批次继续而不是从头开始。**不设计**「先 DELETE 再 INSERT」这类非幂等步骤。
5. **升级窗口**：建新表与建索引在 MySQL / PostgreSQL 上会对表加短暂的元数据锁。**未执行验证**：实际锁定时长与是否需要维护窗口。建议在低峰执行并先做备份（§11 验收「数据库备份恢复和升级回滚」）。
6. **升级后的幂等验证**：连续重启两次，第二次不得产生 DDL。
7. **回滚**：因为底座表结构零改动，回滚 = **停止写入 `apigx_*` 表 + 可选地 DROP 全部 `apigx_*` 表**。底座功能完全不受影响。这与「优先顺序：新增文件 > 扩展上游文件的扩展点 > 修改上游既有逻辑」（`docs/stage0/upstream-diff.md` §规则 2）一致——本草案在 schema 层实现了最低冲突面。
   *例外*：`apigx_api_keys` 与底座 `tokens` 是**行级**关联（影子令牌），DROP 我们的表会让这些影子令牌变成「没有平台作用域的原生令牌」。因此回滚脚本必须**同时禁用（或删除）对应的影子令牌行**，或者按方案 A 实现在 `GetTokenByKey` 里的哈希分支时把分支与表一起回滚。这一点必须在回滚预案里显式写明。

### 7.5 与 `AutoMigrate` 共存的规则（写代码时必须遵守的清单）

| # | 规则 | 理由 |
|---|---|---|
| 1 | 新表模型放**独立包/文件**（扩展点集中），不散落进上游 `new-api/model/*.go` | `docs/stage0/upstream-diff.md` §规则「扩展点集中」 |
| 2 | `apigx_*` 的 `AutoMigrate` 只**追加**调用，不改上游已有的 `AutoMigrate` 参数列表 | 降低升级冲突面 |
| 3 | 不修改上游模型的任何 gorm tag | 否则会触发上游表的 `ALTER TABLE` |
| 4 | 迁移函数**只在主节点**执行（与上游一致） | `new-api/model/main.go:215-217` |
| 5 | 迁移失败**必须让启动失败**，不吞错 | `new-api/main.go:314-318` 的 `common.FatalLog` 语义 |
| 6 | 不使用 `AUTO_INCREMENT` / `SERIAL` 字面量，主键交给 GORM | `new-api/AGENTS.md` |
| 7 | 布尔字段不用 `default:true`（也不用 `default:1` 替代） | `new-api/AGENTS.md`（§2.2） |
| 8 | 行锁一律用 `lockForUpdate(tx)`，不写 `clause.Locking{...}` | `new-api/model/locking.go:20-25`、`new-api/AGENTS.md` |
| 9 | 原生 SQL 里引用 `group` / `key` 列要走 `commonGroupCol` / `commonKeyCol`；布尔字面量走 `commonTrueVal` / `commonFalseVal` | `new-api/model/main.go:22-24`、`:43-64`（本草案通过命名绕开保留字，因此理论上不需要，但**若将来新增此类列名，必须走共享变量**） |
| 10 | JSON 的 marshal/unmarshal 走 `common.Marshal` / `Unmarshal` / `UnmarshalJsonStr` / `DecodeJson` | `new-api/common/json.go:66/42/46/50`、`CODE_STYLE.md` 第 18 条 |
| 11 | 配额换算走 `common.QuotaFromFloat` / `QuotaRound` / `QuotaFromDecimal` 及 `*Checked` 变体，禁止裸 `int(...)` 强转 | `new-api/common/quota_math.go:112/119/133/140/152/159/172`、`CODE_STYLE.md` 第 20 条 |
| 12 | 迁移必须在**全新库**与**从底座旧库升级**两种情形各跑一次，并连续启动两次验幂等 | `CODE_STYLE.md` 第 17 条、`new-api/AGENTS.md` |

### 7.6 附录：如果将来真的需要给底座表加列，三库各自的写法

本草案**不需要**（§6.1），但把上游已验证的写法记在这里，免得将来重新摸索。

| 操作 | SQLite | MySQL ≥ 5.7.8 | PostgreSQL ≥ 9.6 |
|---|---|---|---|
| 加列 | `ALTER TABLE t ADD COLUMN c <type> [DEFAULT x]`（上游写法见 `new-api/model/main.go:571`） | `ALTER TABLE t ADD COLUMN c <type> [DEFAULT x]`（同标准语法） | `ALTER TABLE t ADD COLUMN c <type> [DEFAULT x]`（同标准语法） |
| 改列类型 | **不支持**，必须早退（`new-api/model/main.go:581-582`、`:635-638`）；SQLite 用类型亲和性兜底 | `ALTER TABLE t MODIFY COLUMN c <type>`（上游写法见 `new-api/model/main.go:617`） | `ALTER TABLE t ALTER COLUMN c TYPE <type> USING c::<type>`（上游写法见 `new-api/model/main.go:664-665`） |
| 幂等守卫 | `Migrator().HasColumn`（`new-api/model/main.go:593`）+ `HasTable`（`:589`、`:644`） | 同左 | 同左 |
| 类型探测 | SQLite 不需要（类型亲和性） | `SELECT COLUMN_TYPE FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`（上游写法 `new-api/model/main.go:610-613`） | `SELECT data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`（上游写法 `new-api/model/main.go:600-603`） |
| 加唯一约束 | 三库都必须先探测现有约束/索引再决定（PG 还要处理 constraint 与 index 两种形态，见 `new-api/model/token_migration.go:76-124`） | 同左 | 同左（`new-api/model/token_migration.go:30-52`） |

---

## 8. 交付前自检对照

| 自检项 | 结果 |
|---|---|
| 计划书 §7 的实体是否一个不漏？ | ✅ 23 个实体全部覆盖，映射见 §1.1；额外 5 张派生表已单独标注来源需求 |
| 每张表的每个字段是否都有类型与用途？ | ✅ §5 逐表给出字段表（字段 / 类型别名 / 用途），类型别名展开见 §3.2 |
| 三库兼容性是否逐条检查过（不出现 `SERIAL` / `AUTO_INCREMENT` / PG 专用操作符 / MySQL 专用函数）？ | ✅ §3.2 只用 GORM 原生类型 tag；`AUTO_INCREMENT` / `SERIAL` 明确禁止（§3.2 `ID` 行 + §7.5 规则 6）；规避 PG 专用操作符、部分索引、`type:json`（§0、§2.3、§3.5）；条件更新与 `OnConflict` 用 GORM 抽象而非方言 SQL（§5.13、§5.22） |
| 是否明确区分「可变字段」与「不可变快照字段」？ | ✅ §5 每表都有「不可变快照字段」与「仅允许的单向推进」两栏；「绝不回写历史的字段」单列 |
| 是否写明了迁移顺序与两种升级情形的做法？ | ✅ §7.1（7 批次顺序）、§7.2（幂等）、§7.3（全新库）、§7.4（旧库升级 + fail-closed 回填 + 回滚） |
| 是否留下了「未核实」与「未执行验证」的诚实声明？ | ✅ §0 顶部声明 + §9 逐条清单 |

---

## 9. 未核实清单与未执行验证声明

### 9.1 明确标注「未核实」的结论

| # | 未核实项 | 相关位置 | 需要的核实方式 |
|---|---|---|---|
| 1 | GORM v1.25.12 对本文档 §3.2 各 tag 在 SQLite / MySQL / PostgreSQL 上**实际生成的 DDL** | §3.2、§5 全表 | 在三库实例上跑一次 `AutoMigrate`，用 `Debug` 记录 SQL 与 `information_schema` 实测类型 |
| 2 | MySQL 上底座的**部分唯一索引**写法（`uniqueIndex:...,where:deleted_at IS NULL`）的实际行为 | §3.5、`new-api/model/prefill_group.go:80` | 在 MySQL 上实测该索引能否创建、是否生效；本草案因此**不采用**该模式 |
| 3 | 平台 Key 的摘要（64 位十六进制）写入 `tokens.key` 后，除鉴权查找路径之外**是否还有入口对 key 做长度/字符集校验** | §2.4、§6.2 | 逐一核对导入/导出/搜索/批量接口；本文档只核对了 `middleware/auth.go:290-306`、`:385-405` 与 `model/token.go:280-301`、`controller/token.go:326` |
| 4 | 底座是否允许**外部预设** `logs.request_id`（`ensureLogRequestId` 只在为空时生成，`new-api/model/log.go:95-99`；relay 链路是否从 context 读取该值**未逐一核对**） | §5.19 | 检索 relay 计费与日志生成路径，确认可通过 context 注入 |
| 5 | 底座 `options` 表对**未注册键**的加载/保留语义（`InitOptionMap` 的白名单行为） | §5.27 | 阅读 `new-api/model/option.go:33` 起的完整初始化与持久化路径 |
| 6 | 底座 `channels.ChannelInfo` 的 `type:json` 在三库上的实际落库形态 | §2.3 | 在 MySQL / SQLite 上实测；本文档仍统一用 `TEXT` 存 JSON |
| 7 | 是否**所有** relay 入口都统一经过 `middleware/auth.go` 的 TokenAuth（核对了 `:290-306` 与 `:385-405` 两处分支，未穷举全部入口） | §2.4、§6.2 | 全量检索 `Authorization` 解析点 |
| 8 | `tokens.group` 是否有其它逻辑依赖非空值（影响 §5.2 影子令牌的填写策略） | §5.2 | 检索 `ContextKeyTokenGroup` / `TokenAutoGroups` 的使用点 |
| 9 | 底座 `perf_metrics` 是否已有可复用的分位/直方图计算设施 | §5.22 | 阅读 `new-api/model/perf_metric.go` 的写入侧与调度侧 |
| 10 | `apigx_` 前缀是否与上游**未来**新增表名冲突 | §2.5 | 每次升级上游后重跑命名核对 |
| 11 | 重复授予同一 (主体, 资源) 组合的期望语义（影响 `apigx_grants` 唯一键设计） | §5.12 | 产品决策，不是源码问题 |
| 12 | 是否需要把 APIGX 标准模型同步到底座 `models` 模型广场 | §5.8 | 产品决策 |
| 13 | MySQL 的 `COMPACT` / `DYNAMIC` 行格式与 767 / 3072 字节索引前缀上限 | §3.5 | 属通用 MySQL 知识，**未在本仓库核实**；本草案通过 `HASH64` 摘要列完全规避该风险 |
| 14 | 升级建表/建索引在 MySQL / PostgreSQL 上的实际锁定时长与是否需要维护窗口 | §7.4 | 在真实数据量级上实测 |
| 15 | 从底座旧库升级时，存量 `channels` / `tokens` 的纳管策略是否需要例外 | §7.4 | 产品 + 运营决策 |

### 9.2 未执行验证声明

- **本草案未在任何数据库上执行过。** 没有创建过任何表、索引、约束；没有执行过 `AutoMigrate`；没有跑过 `ALTER TABLE`。
- **没有编译过底座，没有跑过底座的任何测试**，没有部署过，没有做过性能或安全验证。
- **没有在真实或替身上游上验证过任何一条路由、计量、容量或凭据行为。**
- **没有验证过三库兼容性。** `CODE_STYLE.md` 第 17 条与 `new-api/AGENTS.md` 要求的「全新库 + 旧版本升级库各跑一次 + 启动迁移幂等性验证」，本草案**均未执行**；§3 的全部类型选择都是源码层面的设计意图，不是实测结论。
- 计划书 §11 里的全部验收场景、§12 的全部开发基线数值，在本草案中只被当作**约束条件**引用，**没有任何一条被标记为已通过**。
- 本文件只创建 `docs/stage0/schema-draft.md` 一个文件；**未修改 `new-api/` 下任何文件**，未在 `new-api/docs/` 下新建任何文件。

### 9.3 进入实现前必须先闭环的三件事

1. **§6.2 的平台 Key 摘要方案决策**（方案 A 需要改上游文件并登记 `docs/stage0/upstream-diff.md`）。
2. **§9.1 第 1 项的 GORM DDL 实测**：在动手写 Go 模型之前，先用三库各跑一遍 §5 的表结构，确认没有哪一列/哪一个索引在三库中不可移植。
3. **§9.1 第 2 项的 MySQL 部分索引结论**：确认本草案的 `alive_flag` 墓碑方案在 MySQL 上确实可用（它不依赖任何方言特性，预期可用，但仍需实测）。
