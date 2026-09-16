# 领域表实现说明与验证记录（T1.3）

对象：`new-api/apigx/model/`（包 `apigxmodel`），27 张 `apigx_*` 表。
图纸：`docs/stage0/schema-draft.md`（§3 规约、§5.1–§5.27 逐表定义）。
代码：`base.go`（规约与常量）、`models_identity.go`（§5.1–§5.13）、`models_metering.go`（§5.14–§5.27）、`migrate.go`（迁移入口）。

## 1. 验证结果

| 项目 | 命令 / 判据 | 结果 |
|---|---|---|
| 建表数量 | `go test ./apigx/model/`（`TestMigrateCreatesAllDomainTables`，断言 = `ExpectedTableCount` 27） | ✅ 27 |
| SQLite 幂等 | `TestMigrateIsIdempotentOnSQLite`（连续两次迁移，表集合不变） | ✅ |
| 字段类型可移植守门 | `TestModelFieldTypesArePortable`（反射扫描，只允许 `int64/int/string/bool/gorm.DeletedAt`） | ✅ |
| PostgreSQL 全新库 | `tools/stage1-verify/migrate-pg-matrix.ps1` 情形 A（空库 → 27 张表） | ✅ |
| PostgreSQL 从底座旧库升级 | 同上情形 B（`apigx_dev`：27 张 apigx 表 + **37 张底座原生表原样保留**） | ✅ |
| PostgreSQL 幂等 | 上述两个子测试内部均**连续迁移两次**（`APIGX_TEST_PG_DSN` 驱动） | ✅ |
| 编译 / vet / 格式 | `go build ./...`、`go vet ./model/ ./apigx/...`、`gofmt -l apigx` | ✅ 全部干净 |

## 2. PostgreSQL 上实测到的 DDL 事实（与图纸 §3.2 的推断对照）

§3.2 当时对 GORM 生成的列类型标注为「**未核实**」。本次在真实 PostgreSQL 18.4 上实测：

| 别名 | 图纸推断（PG） | **实测（PG 18.4）** | 说明 |
|---|---|---|---|
| `ID` | `bigserial` | `bigint` + `nextval` 序列 | ✅ 等价；**绝不手写 `SERIAL`** 的约束成立 |
| `FK` / `BIGINT` / `MICRO` / `TS` | `bigint` | `bigint` | ✅ |
| `INT` | `int` | **`bigint`** | ⚠️ **偏差**：GORM 在 PG 上把 Go `int` 映射为 `bigint`（Go `int` 为 64 位）。**底座自身同样如此**（`channels.status/type/weight` 实测也是 `bigint`），因此这是与底座一致的行为，不是缺陷 |
| `ENUM`（Go `int`） | `int` | **`bigint`** | ⚠️ 同上 |
| `BOOL` | `boolean` | `boolean`（**12 列，0 列带默认值**） | ✅ 成功避开 `default:true` 在 MySQL/PG 间的归一化陷阱 |
| `STR(n)` | `varchar(n)` | `character varying(n)` | ✅ |
| `HASH64` | `char(64)` | `character(64)`（15 列） | ✅ |
| `CUR3` | `char(3)` | `character(3)`（5 列） | ✅ |
| `TEXT` | `text` | `text`（13 列） | ✅ |
| `SOFTDEL` | `timestamptz` | `timestamp with time zone`（**8 列**） | ✅ 恰好对应 8 张声明了软删除的表（13 张身份表中 5 张无软删除） |

**汇总**：`bigint 382 / varchar 77 / char 20 / text 13 / boolean 12 / timestamptz 8`，共 **132 个索引（其中 58 个唯一）**，27 个主键中 **24 个** 带自增序列 —— 另外 3 个是**有意设计**：`apigx_platform_settings.setting_key`（字符串自然键）、`apigx_quality_buckets.id`… 见下节、`apigx_user_profiles.user_id`（共用底座用户 ID，图纸明确 `autoIncrement:false`）。

### 需要修正图纸的地方

- §3.2 的「`INT` → PG `int`」应改为「PG 实测为 `bigint`（与底座一致）」。
- 结论：`INT` 与 `BIGINT` 在 PG 上**没有区别**；若确实需要 4 字节列，必须写 `type:integer` 之类的显式方言写法——本项目**不采用**（会破坏三库可移植），因此保持 Go `int` + `bigint` 生成的现状。

## 2.5 MySQL 实测（8.0.12，phpStudy 实例，3306）

| 项目 | 结果 |
|---|---|
| 全新库迁移 + 连续两次（幂等） | ✅ 27 张表 |
| 列类型分布 | `bigint 383 / varchar 77 / char 20 / text 13 / tinyint 12 / datetime 8` |
| 与 PostgreSQL 的对照 | 列数**逐类完全一致**（383/77/20/13/12/8），仅方言名不同：`boolean`→`tinyint`、`timestamptz`→`datetime` |
| `apigx_quality_buckets` | `id bigint(20)`（主键）+ `bucket_key_hash char(64)` ✅ |
| 底座在 MySQL 上启动 | ✅ `GET /api/status` → 200；库内 **64 张表全部 InnoDB**（37 底座原生 + 27 APIGX） |

### ⚠️ 一个必须记住的坑：默认引擎是 MyISAM

首次接入时**三库矩阵「通过」了，但通过得没有意义**：

- phpStudy 的 `my.ini` 写着 `default-storage-engine=MyIsam`，于是 27 张表**全部建成 MyISAM**（25 Dynamic + 2 Fixed）。
- 后果一：**底座自己 FATAL 起不来**——`Error 1071: Specified key was too long; max key length is 1000 bytes`，卡在它原生的 `passkey_credentials`（`credential_id varchar(512)` 上的唯一索引）。MyISAM 的索引上限是 1000 字节，InnoDB DYNAMIC 是 3072。
- 后果二（更严重）：**MyISAM 没有事务与行锁**。本项目的 Reservation 预留、Invite 的原子扣减、额度原子预留、幂等记录全都依赖事务语义——跑在 MyISAM 上这些保证会**静默失效**，而且测试照样"通过"。

**处理**：
1. 运行时切换 `SET GLOBAL default_storage_engine=InnoDB`（未改 phpStudy 的 `my.ini`，重启即失效——若希望长期生效需改配置文件）；
2. 重建测试库后重跑，矩阵在 InnoDB 上通过；
3. **新增守门断言**：MySQL 子测试读 `information_schema.tables.engine` 并要求 `InnoDB`，否则失败。反向验证过：把引擎临时切回 MyISAM，断言**如实失败**（`actual: "MyISAM"`）——不是永远绿的测试。

**结论（需求级）**：PostgreSQL 天然支持这些语义；**MySQL 部署必须使用 InnoDB**；SQLite 由其自身事务语义保证。这一点已写入 `docs/dev-environment.md`。

## 3. 实现期决策（与图纸的差异及裁决）

| # | 事项 | 图纸 | 实现 | 裁决与理由 |
|---|---|---|---|---|
| 1 | `apigx_quality_buckets` 主键 | 字段表同时列 `id`（`ID`）与 `bucket_key_hash`（`HASH64`），唯一约束 `uniqueIndex(bucket_key_hash)` | 初版把两者合并为一个 `id char(64)` 主键；**已按图纸拆回**：`id int64 primaryKey` + `bucket_key_hash string char(64) uniqueIndex` | **按图纸拆分**。理由：① 与其余 26 张表保持一致（bigint 代理主键）；② 64 字符字符串做主键在 MySQL/InnoDB 上是过宽的聚簇键；③ 字段数与图纸字段表一致（25 列） |
| 2 | `apigx_attempts` 的 `(request_id, attempt_index)` | `index(...)` 普通索引 | `uniqueIndex:uniq_attempt_request_index` | **保留实现**。该组合是一次请求内的尝试序号，天然唯一；唯一约束还能防止重复尝试被重复记账。建议回调图纸（改成唯一约束） |
| 3 | 索引命名 | 图纸只给列组合，未给名字 | `idx_<表缩写>_<列缩写>` / `uniq_...`，全部显式命名，最长 33 字符（≤40） | 接受。规则由实现方拟定，符合 §3.1「每条索引显式命名」 |
| 4 | `PriceRule` / `CapacityPolicy` 的索引 | 图纸列出的索引正好是唯一键最左前缀 | 未重复建 | 接受：同列同序的普通索引对唯一键是冗余的 |
| 5 | 软删除列 | §5.14–§5.27 逐表写明「软删除：无」 | 这 14 张表**没有** `deleted_at`/`alive_flag`；§3.5 墓碑模板只用于需要软删除的 8 张身份表 | 符合图纸：计量与计价表是追加式证据，不能软删除 |
| 6 | 枚举列 Go 类型 | 只给取值表，未给类型名 | 一律裸 `int` + `ENUM` tag，不新增导出类型/常量 | 接受：避免在领域层自行扩大 API 面；类型与常量留到 service 层 |
| 7 | 不可变快照的强制力 | 图纸要求「快照列不得回写历史」 | GORM v1.25 已不支持 `->:false` 之类的不可变 tag，因此**只能用逐字段中文注释承载**（注释写「快照」或「仅单向推进」） | **已知限制**：这是**注释级约束，不是数据库层强制**。运行时强制需要在 service 层实现（T1.6/T1.11 的职责） |

## 4. 未验证（必须如实记录）

1. ~~**MySQL 矩阵未执行**~~ → **已完成（2026-09-16）**：本机 phpStudy 的 MySQL 8.0.12（3306）上通过；细节与 MyISAM 坑见 §2.5。测试仍保留「缺 DSN 时明确 SKIP 并打印原因」的行为，任何一台机器上漏跑都会显式暴露。
2. **SQLite 只验证了「表数量 + 幂等 + 字段类型守门」**，未逐一核对 SQLite 上每列的实际亲和类型。
3. **未做旧版本兼容性验证**：`agipx_*` 表尚无历史版本，因此「从更早的 APIGX 版本升级」这一路径不存在，本轮只覆盖「全新库」与「从底座旧库升级」。
4. 全部结论基于**本机 PostgreSQL 18.4 + SQLite（glebarez 纯 Go 驱动）**；未在 PostgreSQL 9.6（底座声明的下限）上验证。

## 5. 过程中的环境怪癖（避免重复踩坑）

1. **PowerShell 的 `"$var?x"` 插值陷阱**：`"postgres://.../$db?sslmode=disable"` 会把 `$db?sslmode` 当成变量名展开成空串，DSN 直接变成 `database==disable`，报出看似「环境问题」的连接错误。写含 `?` 的 DSN 必须用单引号或 `${db}`。
2. **`DROP DATABASE` 可能永久阻塞**：本机 PG 实例上曾出现零连接仍无法 drop 的情况（`statement_timeout` 也不生效）。变通做法是换一个新库名，而不是纠缠 drop。
3. **`psql` 的 NOTICE 走 stderr**：在 `$ErrorActionPreference = 'Stop'` 下会被提升为终止错误（`drop database if exists` 的「不存在,跳过」就会触发）。脚本里必须临时放宽并用退出码判断成败。
4. 上述 1 与 3 已分别记入 `whisper-note-dev-flow/CODE_STYLE.md` 与相关脚本实现。
