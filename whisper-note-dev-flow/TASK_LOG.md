# 任务清单

> 规则：顶部最新，只追加不删除历史。状态只使用 `[进行中]` / `[已完成]`。
> 规划依据：`docs/PROJECT_PLAN.md` v1.0 与 `docs/PLAN_REVIEW.md`（2026-09-16，结论：具备进入阶段 0 的条件）。
> 子任务通过验证后按 skill §7 打勾（不新增 PROJECT_STATUS 序号、不 commit）；整任务完成后按 §4 全量更新 + commit。

## [进行中] 阶段 1：文本共享闭环 (2026-09-16)

**本轮目标**：在阶段 0 冻结的设计上实现文本共享闭环——分享者能建分组/渠道/多 Key 并发布模型供给，使用者能用平台 Key 指定来源或自动调用，平台完成授权、免费计量与基础容量控制。

**验收标准**（计划书 §11 阶段 1）：
1. 分享者 A/B 提供同一标准模型，广场呈现**一个模型 + 两项授权供给**；
2. 使用者能指定 A 调用；A 能查看自己的消耗；**B 无法修改 A 的渠道或查看其私有凭据**；
3. **没有充值余额的合法用户可以调用**（`usage_only`）；
4. 私有 Key 所在渠道**不能被请求头切出授权范围**；
5. **满载时不会超发**；重试、流结束与进程中断**不重复计量**；
6. 本阶段界面只启用已完成的「指定供给 + Key 轮询/随机」；质量自动策略待阶段 2，**不用未实现的 `auto` 默认值伪装完成**。

**预计涉及模块**：
| 模块 | 文件 | 类型 | 说明 |
|---|---|---|---|
| 授权收敛 | `new-api/middleware/distributor.go` | 修改（已实施） | TD-08：pin/非 pin 共用同一授权函数 |
| 领域与接口 | `new-api/apigx/{domain,api,route,model}` | 新建 | 27 张表 + 分享者接口（受 owner 约束） |
| 计量模式 | `new-api/service/billing_session.go` 等 | 修改 | `usage_only` 资金来源 |
| 容量控制 | `new-api/apigx/capacity` | 新建 | 并发/RPM/TPM/预算预留与对账 |
| 验收脚本 | `tools/stage1-verify/` | 新建 | 阶段 1 的端到端验收 |
| 流程文档 | `whisper-note-dev-flow/` | 修改 | 按 skill §3/§4/§5/§7 同步 |

**任务清单**（工作包定义见 `docs/stage0/gate-review.md` 第 3 节）：
- [x] T1.2 授权收敛（TD-08）：pin / 非 pin 走同一授权函数 → `new-api/middleware/distributor.go`；回归测试 `middleware/task_plugin_origin_task_test.go`；运行时回归 `tools/stage1-verify/td08-pin-model-limit.mjs`。证据：修复前 pin 下未授权模型 HTTP 200 且到达上游，修复后 403 且 0 次出站；临时禁用修复时回归测试 FAIL（证明测试有效）；middleware 全量测试通过、全仓构建通过。台账登记 D-001/D-002。
- [ ] T1.1 平台 Key 接入（按底座现状：明文存储 + 等值查找；用 `authz.SecretView` 约束可见性；明文不进日志）
- [x] T1.3 领域表落地（**三库矩阵全部通过**）
  - [x] 27 张表代码：`new-api/apigx/model/{base,models_identity,models_metering,migrate}.go`（`apigx_` 前缀，包名 `apigxmodel`）；`ExpectedTableCount = 27` 作为契约常量
  - [x] 测试守门：`migrate_test.go`（建表数量 = 27 / SQLite 连续迁移幂等 / 反射扫描字段类型只允许 int64·int·string·bool·gorm.DeletedAt）；`migrate_matrix_test.go`（DSN 驱动的三库矩阵，未配置的方言**明确 SKIP 并打印原因**；MySQL 额外断言引擎必须是 InnoDB）
  - [x] **PostgreSQL 实测**：全新库 **27 张**；从底座旧库升级 **27 张 apigx + 37 张底座原生表原样保留**；两种情况均连续迁移两次验幂等
  - [x] **MySQL 8.0.12 实测**：全新库 27 张 + 幂等；列类型分布与 PG **逐类一致**（383/77/20/13/12/8，仅方言名不同：boolean→tinyint、timestamptz→datetime）；**底座真的跑在 MySQL 上**：`/api/status` 200，库内 64 张表全部 InnoDB（37 底座 + 27 APIGX）
  - [x] **SQLite 实测**：建表数量、幂等、字段类型可移植守门
  - [x] ⚠️ **踩到并修掉的关键坑**：phpStudy 的 `my.ini` 是 `default-storage-engine=MyIsam` → 27 张表全建成 MyISAM（无事务！）、底座自身 FATAL 起不来（MyISAM 索引上限 1000 字节）。已切到 InnoDB 重验，并**新增引擎守门断言**（反向验证：切回 MyISAM 时断言如实失败）。详见 `docs/dev-environment.md` §6
  - [x] 图纸勘误 2 处：§3.2 的「`INT` → PG `int`」被实测证伪（实为 `bigint`，底座自身亦然）；§5.13 的 `use_limit_mode = 1` 改为 `3`（原文把「禁用」当成「不限」）
  - [x] 接线：底座 `model/main.go` 的 `migrateDB()` 末尾调用 `apigxmodel.Migrate(DB)`（台账 D-003）
  - 记录：`docs/stage1/schema-impl-notes.md`（验证结果、DDL 事实、MySQL 章节、7 条实现期决策、未验证项、环境怪癖）
- [x] T1.4 分享者接口（分组 / 渠道 / Key 池 CRUD，受 owner 约束）→ `docs/stage1/share-api-notes.md`
  - [x] **前置：领域仓储层已就绪** → `new-api/apigx/model/{repo_share,repo_upstream}.go`：分组/渠道/上游账户池/上游 Key 的 CRUD 与不变量（软删除墓碑语义、**归属由服务端强制**、Key 池三条件筛选、参数校验）；测试 8/8（另有 5 个迁移用例）
  - [x] 测试发现并修掉一个**真实设计缺陷**：`base_channel_id` 上有唯一索引，但新建时它是 0 → **多条未绑定渠道会互相冲突**。已改为强制 `BaseChannelID > 0`（每分享渠道必须绑定一条底座渠道，转发链路也需要它）
  - [x] HTTP 接口层：`apigx/api/share.go` + `apigx/route/route.go`；上游接线 4 行（台账 D-012）；凭据加密 `apigx/secretbox`（AES-256-GCM，主密钥与库分离，缺密钥失败关闭）
- [ ] T1.5 多 Key 导入与池策略（稳定凭据 ID、逐 Key 状态与统计）
  - [x] **前置：Key 池数据层已就绪**：`CreateUpstreamKey` / `ListUpstreamKeysByChannel` / `AvailableUpstreamKeys`（启用+未到期+不在冷却三条件）/ `UpdateUpstreamKeyStatus` / `SetUpstreamKeyCooldown` / `DeleteUpstreamKey`；Key 归属经渠道传递并强制校验
  - [x] 单把 Key 的增删改查 + 状态/冷却 + 加密落库（端到端 16/16，PG 核对密文与墓碑）`n  - [ ] 待做：批量导入解析、池策略（轮询/随机/加权/剩余容量）、逐 Key 统计、凭据轮换与吊销
- [x] T1.6 免费计量 `usage_only`（落点在计费会话的**资金来源**维度）
  - [x] 实现：`service/apigx_usage_only.go`（模式开关 + `UsageOnlyFunding` + `newUsageOnlySession`）、`service/billing.go`（常量）、`service/billing_session.go`（NewBillingSession 分支 + reserveFunding 接住新资金来源）；台账 D-005/D-006/D-007
  - [x] 单测 4/4：资金源三方法必须空操作、模式开关默认关闭、`reserveFunding` 必须接住新来源、**未知来源仍须报错**（反向保护）
  - [x] **运行时 A/B 验收**（`tools/stage1-verify/usage-only.mjs`，同一底座只差一个环境变量）：
    阶段 A 默认 → 零余额 **403**、0 次出站、令牌闸门 401（3/3 通过，**默认行为零回归**）；
    阶段 B 开启 → 零余额 **200 且 1 次到达上游**、**钱包仍为 0（只记账）**、令牌额度 0 时**仍 401 且 0 次出站**、**成本仍记账**（used_quota 1725→3450）（5/5 通过）
  - [x] 明确未采用四条错误路径：不走 `FreeModel` 短路、不把价格/倍率设 0、不给超大余额、不另起计量
  - [x] 回归检查发现基线事实：`service` 包有 2 个**上游既存失败**（channel affinity 用例），已用原始提交的独立工作树对照确认与本次改动无关 → 验收标准应表述为「不新增失败」
  - 记录：`docs/stage1/usage-only-notes.md`
- [ ] T1.7 共享容量控制（**核心账本与「渠道并发」接线均已完成并验证；其余维度与候选重选未做，故未勾选**）
  - [x] 核心账本 `new-api/apigx/capacity/`：Lua 原子预留（先全查再全扣）＋幂等归还（按租约记录扣减）＋租约续租/过期回收；维度 key 覆盖用户 / 平台 Key / 渠道 / 供给 / 上游 Key / **上游账户池**，HTTP 槽位与异步任务槽位分开
  - [x] 测试 8/8 通过（真实 Redis，连续两次运行均通过）：**40 个并发抢 limit=5 → 恰好发放 5 个**；全有或全无；重复释放不变负；租约过期回收；续租语义；非法输入拒绝；**无 Redis 时拒绝准入**
  - [x] **接线完成**：`middleware/distributor.go` 在 `SetupContextForSelectedChannel` 之后、`c.Next()` 之前接入（台账 D-009/D-010）；`apigx/relayhook` 6/6 测试通过；**默认关闭**（`APIGX_CAPACITY_ENABLED`），关闭时既有链路行为不变
  - [x] **运行时 A/B 验收**（`tools/stage1-verify/capacity-limit.mjs`，替身握 1.5s 制造真实重叠）：默认关闭 → **200×3、上游 3 次**（2/2 通过）；上限=1 + 并发 3 → **200×1、429×2、上游仅 1 次、429 带 `Retry-After`**（4/4 通过）
  - [x] **槽位归还验证**：并发结束后 Redis `apigx:cap:*` **零残留**，紧接着再打一轮仍正常（无泄漏）
  - [x] 测试发现并修掉 3 个真实缺陷：`Usage` 把 `redis.Nil` 当错误；用例因 TTL 残留不自洽；**TTL 以秒下发导致亚秒 TTL 被截断、比续租间隔还短**；另修正 `Acquire` 的 `int`/`int64` 类型不匹配
  - [x] **多维度与四态**：`apigx/capacity/policy.go` 实现「未配置 / 禁用 / 继承 / 明确不限 / 明确值」五值语义与「模式+值列」配对校验，解析沿「最具体→最一般」链；接线层为**渠道 + 平台 Key + 用户**三个维度做**同一次原子预留**（任一维满即整体拒绝）。测试 12/12（capacity）+ 8/8（relayhook，含多维度用例）
  - [x] **多维度的运行时验证**：只设 `APIGX_TOKEN_CONCURRENCY_LIMIT=1`（**渠道不设限**）、并发 3 → **200×1 + 429×2、上游仅 1 次、Redis 零残留**（证明不是只有渠道维度在起作用）
  - [x] 修掉一处对外措辞误导：429 消息原写 "channel capacity…"，但超限可能是平台 Key 或用户维度，已改为中性措辞（精确维度只进内部日志）
  - [ ] **未完成**：① 候选重选（当前该渠道满载即 429）② **上游 Key / 上游账户池维度**（需渠道→Key/账户映射，仓储层未实现；这是防容量叠加的关键维度）③ RPM/TPM 与预算维度 ④ 上限来源改为 `apigx_capacity_policies` 并替换「未配置=不约束」的过渡行为 ⑤ 对账 ⑥ 异步任务槽位接入
  - 记录：`docs/stage1/capacity-notes.md`
- [ ] T1.8 模型获取与供给发布（一键获取 + 手工添加 + 待发布流程）
- [ ] T1.9 广场与详情（只启用已完成能力）
- [ ] T1.10 平台 Key 与指定来源（允许模型 / 允许来源 / 固定来源 / 回退）
- [ ] T1.11 用量记录（使用者视角：请求、模型、实际渠道、耗时、估算成本）
- [ ] T1.12 拒绝清单落地（错误码与不泄露：404 / 401 / 429 / 503）

**本阶段明确不做**：质量自动路由（`auto`）、价格同步与参考库、图片/语音/向量/重排/视频、公开注册。

**排期口径**：1 人主导 + AI 实现，粗估 5–8 周（见 `docs/stage0/gate-review.md` 第 4 节）；建议 T1.1–T1.4 完成后用实际速率重新外推。

## [已完成] 阶段 0：底座验证与设计冻结 (2026-09-16)

**完成情况**：11/11 子任务完成。闸门评审结论：**三条路径（免费计量 / 凭据选择 / 资源授权）均可在既有扩展点闭合，阶段 0 通过**（见 `docs/stage0/gate-review.md`）。

**下一轮（已规划，未启动）**：阶段 1 文本共享闭环 —— 12 个工作包 T1.1–T1.12 见 `docs/stage0/gate-review.md` 第 3 节。进入前必须先做三件事：拍板 TD-07（平台 Key 存储形态）、清理磁盘（D 盘仅剩约 5.3GB）、补齐两处未核实项。

**本轮目标**：不写业务功能，先锁定 New API 底座并冻结设计——固定提交与依赖记录、复用/修改清单、核心实体与唯一约束/迁移草案、权限与计量状态流、协议支持矩阵，并跑通最小后端验证路径，用受控上游替身证明归属隔离、定向渠道/Key 不被二次选择、无余额但正成本估算、预算与并发准入、任务/文件对象隔离可实现。

**验收标准**（计划书 §11 阶段 0，逐条可验证）：
1. 底座提交与依赖可复现：记录 commit、依赖清单、获取方式、相对上游的差异清单。
2. 受控上游替身中，用**两个账号、同一标准模型的两条渠道、至少三把测试 Key** 通过 5 项验证：①归属隔离 ②定向渠道/Key 不被二次选择 ③没有充值余额但成本估算为正 ④预算与并发准入生效 ⑤任务/文件对象跨用户不可访问。
3. 免费计量（`usage_only`）、凭据选择、资源授权三条路径能在既有扩展点闭合；**无法闭合则先改实现方案并重新验证**，不得扩页面或加协议掩盖。
4. 许可义务完整：AGPLv3、NOTICE、修改标注、版权与署名、对网络使用者的源码提供方式。
5. 不把管理员接口直接开放给普通用户；不另起第二套用户系统、路由器或密钥明文表。
6. 替身通过**不得**当作真实上游兼容证明；六类能力真实适配器留待阶段 3 联调。

**预计涉及模块**：
| 模块 | 文件 | 类型 | 说明 |
|---|---|---|---|
| 阶段 0 结论文档 | `docs/stage0/`（`base-lock.md`、`upstream-diff.md`、`reuse-modify.md`、`acl-state.md`、`metering-state.md`、`protocol-matrix.md`） | 新建 | 冻结设计产出，逐项对应下方子任务 |
| 架构与技术债 | `docs/architecture/baseline.md`、`docs/architecture/tech-debt.md` | 新建 | T0.11：模块边界、扩展点契约、可维护性约束、技术债台账与偿还计划 |
| 迁移草案 | `docs/stage0/schema-draft.md` | 新建 | §7 实体 → 表、主键/唯一约束、索引、软删除、快照不可变字段、迁移顺序 |
| 底座 fork | `new-api/`（目录名待定，阶段 0 固定提交） | 移植 | 沿用原仓库组件与测试设施，新增扩展点集中目录并记录相对上游改动 |
| 上游替身 | `tools/stub-upstream/` | 新建 | 可控上游：可注入延迟/失败/限流/usage，支撑 5 项验收 |
| 本地验证环境 | `deploy/local/redis.conf`、`deploy/local/init-db.sql`、`.env.example` | 新建 | Redis 与 PostgreSQL 本地配置、环境变量模板（不写死密钥） |
| 开发环境工具 | `tools/dev/env.ps1`、`tools/dev/init-db.ps1`、`tools/net/fetch.mjs` | 新建 | 环境引导、数据库初始化、沙箱内 HTTPS 下载助手 |
| 环境说明 | `docs/dev-environment.md` | 新建 | 本机环境现状、两个已踩的坑与绕行方案、验证证据 |
| 仓库基线 | `.gitignore` | 新建 | 排除 `.toolchain/`、`.env`、构建产物、`.codebase-memory/` |
| 验收脚本 | `tools/stage0-verify/` | 新建 | 脚本化复现两账号/两渠道/三 Key 场景，输出验收证据 |
| 流程文档 | `whisper-note-dev-flow/PROJECT_STATUS.md`、`TASK_LOG.md`、`CODE_STYLE.md` | 修改 | 按 skill §3/§4/§5/§7 同步 |

**任务清单**：
> 执行方式（2026-09-16）：T0.3–T0.7 与 T0.11 由任务看板 `apigx-stage0` 执行（已批准，成员 base-analyst / schema-architect / policy-architect / metering-architect）；T0.1、T0.2 由主流程完成；T0.8–T0.10 暂由主流程持有，阻塞于 PostgreSQL 接入，不放给成员。
- [x] T0.1 环境与出网就绪 → `docs/dev-environment.md`
  - [x] Go 1.27.1 便携工具链 → `.toolchain/go`；`go get` 实测拉取 pgx v5.11.0、go-redis v9.22.0 成功（经 `goproxy.cn`）
  - [x] Redis 5.0.14.1 便携版 → `.toolchain/redis`，配置 `deploy/local/redis.conf`；`redis-cli ping` → `PONG`
  - [x] Bun 1.4.2 便携版 → `.toolchain/bun`（底座前端规范要求 Bun）；安装目录与缓存下沉仓库内，绕开沙箱 EPERM
  - [x] 出网通道 → 沙箱内 Schannel 不可用（curl/PowerShell/.NET 的 HTTPS 全部失败），改用 `tools/net/fetch.mjs`（Node 自带 OpenSSL）+ Git 仓库级代理；`goproxy.cn`、GitHub API、`git ls-remote` 均通
  - [x] 仓库基线 → `git init -b main` + `.gitignore`（`.toolchain/`、`.env`、构建产物、`.codebase-memory/`）；GitHub 走仓库级 `http.https://github.com/.proxy`
  - [x] PostgreSQL 18.4 接入 → **工作区内独立实例**：数据目录 `.toolchain/pgdata`，127.0.0.1:**5433**，角色 `apigx`，库 `apigx_dev`；不需要系统超级用户密码、不改系统配置；管理脚本 `tools/dev/pg.ps1`；实测 `apigx @ apigx_dev | 18.4`
  - [x] 底座构建前提取证 → `go mod download` 通过；`relaykit` 独立构建（`GOWORK=off go build ./...`）通过；根模块 `go build ./...` 仅差前端产物 `web/dist`（前端构建中）
- [x] T0.2 底座锁定与许可 → `docs/stage0/base-lock.md`
  - [x] 获取固定提交 `69a50029819a26c53e6babd276d49cfe2f8880ad` → `new-api/`（detached HEAD，2468 个受跟踪文件），锁定记录 `deploy/base.lock.json`
  - [x] 依赖清单与复现步骤：Go 直接依赖按用途归类（gin/gorm/pgx v5.9.2/go-redis **v8**/casbin/sobek/expr…）、前端 Bun+React19+Rsbuild2、关键文件 SHA256 指纹、完整复现命令
  - [x] 差异追踪机制：`tools/dev/upstream-diff.ps1`（已运行，实测差异 0 处）+ 台账 `docs/stage0/upstream-diff.md`
  - [x] 许可与署名义务逐条成表：AGPLv3 §7(b) 署名文案、原项目可见链接、§7(c) 标注修改、NOTICE/THIRD-PARTY 随附、网络服务源码提供、受保护标识只增不改
- [x] T0.3 复用/修改清单 → `docs/stage0/reuse-modify.md`（20 项逐条定位到「文件:行号」，含 4 项必须修改行为的最小侵入方案、扩展点目录建议、未核实清单）；同步更新 `docs/stage0/upstream-diff.md` 第二、三节
- [x] T0.4 核心实体与迁移草案 → `docs/stage0/schema-draft.md`（1454 行；含验证边界声明、底座既有约定核查、三库可移植字段规约、逐表定义、与底座表的关联、迁移与两种升级情形、自检对照、未核实清单）
- [x] T0.5 权限状态流 → `docs/stage0/acl-state.md`（916 行；含权限判定顺序、Grant 与两类邀请、单供给邀请边界、§10.3 生命周期状态机逐格落地、传播与出站前重验、错误码与不泄露、OWASP ASVS 覆盖、关键场景期望结果）
- [x] T0.6 计量与容量状态流 → `docs/stage0/metering-state.md`（635 行；含 Request/Attempt/UsageEvent 口径、usage_only 落点、幂等与校正、执行顺序与失败补偿、故障降级、长流与异步任务、多 Key 与共享账户池、预算上界、无法保证的维度、与底座计费不变量对照）
- [x] T0.7 协议支持矩阵 → `docs/stage0/protocol-matrix.md`（含客户端协议入口清单、40 个上游适配器目录与新增适配器的四处改动、六类能力 × 协议矩阵、能力差异细项、异步任务链路、有代码证据的不承诺项、阶段 3 联调清单）
- [x] T0.8 最小可运行后端路径 → 3/3 全部完成
  - [x] 底座构建 → `bun install`（1193 包）+ `bun run build`（`web/dist` 约 59MB）+ `go build ./...` 全绿；二进制 `.toolchain/bin/new-api.exe`（156MB）
  - [x] 底座启动与迁移 → 接工作区 PG 实例（127.0.0.1:5433）与本地 Redis 启动成功，日志：`using PostgreSQL as database` → `database migration started` → `Redis is enabled` → `ready in 1039 ms`；`GET /api/status` → **HTTP 200**；PostgreSQL 内自动建表 **37 张**
  - [x] **一条对话请求经替身上游返回（实测通过）**：`POST /v1/chat/completions` → **200**，返回体由替身产生（`stub reply to: stage0 ping` + 替身 usage）；替身取证显示 `upstreamKey=sha256:9d132c3297/…AAA1`（正是配置的那把上游 Key）、`platformHeaders={}`（**平台扩展头未泄漏到上游**）、上游收到的 `model=stub/model-x-202609`。脚本：`tools/stage0-verify/wire-base.mjs`（一键接线+验证）与 `chat-probe.mjs`（单请求探针）
  - 过程中确认的底座行为：dashboard 鉴权只认 `Authorization` 头（`middleware/auth.go:159`），登录返回的 `data.access_token` 是内部会话 JWT；建渠道需 `{mode:"single", channel:{...}}`（`controller/channel.go:652,764`）；令牌明文需 `POST /api/token/:id/key` 另取；渠道缓存默认 60s 同步，新建渠道后需等待
- [x] T0.9 上游替身与 5 项验收脚本 → 报告见 `docs/stage0/acceptance-report.md`
  - [x] 替身上游 `tools/stub-upstream/server.mjs`：OpenAI 兼容端点（`/v1/models`、`/v1/chat/completions` 同步+SSE、`/v1/embeddings`、`/v1/images/generations`、`/v1/audio/transcriptions`、`/v1/rerank`、`/v1/video/generations` 占位）+ 故障注入（status 503/429/401、ttft、分片间隔、hang）+ 取证记录（上游 Key 指纹计数、到达的 `X-APIGX-*` 头、请求体、上游实际收到的模型名）
  - [x] 5 项验收脚本 `tools/stage0-verify/acceptance.mjs`（可重复执行，含会话与令牌明文缓存以避开限流）：**①归属隔离** 用户/令牌面 PASS（B 看不到 root 令牌、管理面 403、读他人令牌明文被拒）+ 渠道归属 GAP（底座无 owner 字段）；**②定向渠道/Key 不被二次抽选 PASS**（定向渠道 A 6/6 成功，替身只看到 A 的两把 Key 3/3 轮换，渠道 B 的 Key 零出现）；**③无余额但正成本估算**：正成本估算 PASS（单次扣 1725 ≈ ＄0.00345）+ 零余额被 403 `insufficient_user_quota` 拦截 → GAP（usage_only 需移除该前置）；**④预算与并发准入**：预算准入 PASS（额度 20000 下并发 6 个恰好 1 个通过、剩余 18275 未被击穿、被拒请求未出站）+ 并发上限 GAP（8 并发到达时间跨度仅 170ms，无渠道/Key 级并发上限）；**⑤任务/文件对象隔离**：路由鉴权 PASS（401/404 正确）+ 端到端归属未取证（无真实异步任务，属阶段 3）
- [x] T0.10 免费计量改造点定位与阶段 0 闸门评审 → `docs/stage0/gate-review.md`
  - [x] 余额前置检查定位：`service/billing_session.go:385-401`（`tryWallet` 的 `userQuota <= 0` 直接 403，实测对应 `insufficient_user_quota`）
  - [x] 三条路径判定：**免费计量「可闭合」**（扩展点为 `NewBillingSession` 的资金来源维度，陷阱是 FreeModel 短路与 TrustQuota 旁路）；**凭据选择「可闭合」**（pin 机制可用，实测定向不被二次抽选，前提是先修 TD-08）；**资源授权「可闭合但代价最高」**（需新增 owner 与分享者接口，最大风险是 TD-07）
  - [x] **闸门结论：阶段 0 通过，可进入阶段 1**，附带三个前置条件（TD-07 决策、TD-08 修复、容量控制与计费预留解耦）
  - [x] 阶段 1 详细任务 12 个工作包（T1.1–T1.12）+ 明确不做清单
  - [x] 单人排期重估（1 人 + AI）：阶段 1 **5–8 周**、阶段 2 3–5 周、阶段 3 4–7 周（受真实凭据支配）、阶段 4 1.5–3 周；建议在 T1.1–T1.4 完成后用实际速率重新外推
- [x] T0.11 架构与技术债基线（用户 2026-09-16 要求：初期就做好整体架构规划与可维护性）
  - [x] 模块边界与依赖方向 → `docs/architecture/baseline.md` §2：`apigx/{domain,api,route,relayhook,capacity,pricing,model}` 分层，6 条强制依赖方向规则（含 `relaykit` 独立、禁止自造配额换算、根模块 JSON 走 `common.*`）
  - [x] 扩展点清单与契约 → 同上 §3：E1–E10，逐条给出位置、契约、调用方与上游升级冲突风险（E2/E3/E5/E7/E8 标为高风险）
  - [x] 可维护性约束 → 同上 §4：四态显式表达、稳定 ID、不可变快照、软删除、秘密分级、错误码与不泄露、可测试性、命名
  - [x] 测试策略 → 同上 §5：领域单测 / 契约测试 / 替身集成 / 三库兼容矩阵 / miniredis / 验收脚本，含禁止的伪测试类型
  - [x] 上游升级策略 → 同上 §6：触发条件 + 7 步检查清单 + 回滚方式（`apigx/` 可独立重建）
  - [x] 一致性核对 → 同上 §7：核对 7 个主题，未发现互相矛盾；唯一需拍板项为「数据库是否只支持 PostgreSQL」
  - [x] 技术债台账 → `docs/architecture/tech-debt.md`：16 条，其中 TD-07/TD-08/TD-09/TD-10/TD-11 为**写第一行业务代码前必须解决**的高优先级项

**阶段 0 完成后的下一轮（已规划，未启动）**：阶段 1 文本共享闭环 —— 见 `PROJECT_STATUS.md` 路线图；其详细子任务在 T0.10 闸门评审通过后写入本文件顶部。
