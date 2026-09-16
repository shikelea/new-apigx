# APIGX 架构基线

阶段 0 · T0.11 产出（用户 2026-09-16 明确要求：初期就把整体架构规划与可维护性做对）。
输入：`docs/stage0/{reuse-modify,protocol-matrix,schema-draft,acl-state,metering-state}.md`、`docs/stage0/base-lock.md`、`docs/PROJECT_PLAN.md`、`whisper-note-dev-flow/CODE_STYLE.md`。

> **验证边界**：本文是设计结论汇总，**没有编译、没有运行、没有调用上游、没有跑测试**。
> 凡引用底座行为的地方都给出「文件:行号」，可逐条核对。

## 1. 架构定位

模块化单体，沿用底座的 Go 后端 + React 前端；共享领域逻辑集中在新增目录，协议转换复用独立模块 `relaykit/`。目标不是「最优雅的架构」，而是**在上游 20.8 万行 Go + 24 万行前端的现实约束下，做到改动可枚举、可回滚、可升级**。

## 2. 模块边界与依赖方向

```
                    ┌──────────────────────────────────────────┐
   HTTP   ────────► │ 上游 router/  ──(接线一行)──►  apigx/route │
                    └──────────────────────────────────────────┘
                                        │
        ┌───────────────────────────────┼───────────────────────────────┐
        ▼                               ▼                               ▼
   apigx/api/                    apigx/relayhook/                 apigx/capacity/
   分享者与使用者接口              与 relay 链路接线                 共享容量控制
        │                               │                               │
        └───────────────┬───────────────┴───────────────┬───────────────┘
                        ▼                               ▼
                  apigx/domain/                   apigx/pricing/
                  领域模型与不变量                  价格来源适配与价格版本
                        │
                        ▼
                  apigx/model/（GORM 实体，三库兼容）
```

**依赖方向规则（强制）**

| 规则 | 说明 |
|---|---|
| D1 | `apigx/domain` **不得** import `relay/`、`relaykit/`、gin 上下文或任何 HTTP 类型。领域层只依赖标准库、GORM 与 `common/` 中的纯工具。 |
| D2 | `apigx/*` 不得反向被上游包依赖，唯一例外是上游 `router/` 的接线一行与既定的注入点。 |
| D3 | `relaykit/` 必须保持**独立可构建**，禁止从 `apigx/*` 或根模块引入任何东西（底座硬要求，改动后须 `cd relaykit && GOWORK=off go build ./...`）。 |
| D4 | 计费与配额换算必须走底座既有入口（`common/quota_math.go` 的 `QuotaFromFloat` / `QuotaRound` / `QuotaFromDecimal` 及 `*Checked` 变体），`apigx/*` 不得自造换算函数或裸 `int()` 强转。 |
| D5 | JSON 序列化在根模块内必须走 `common.Marshal/Unmarshal/...`；`relaykit` 内走 `kitutil.*`。 |
| D6 | `apigx/*` 的新表必须同时兼容 SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6。 |

## 3. 扩展点清单与契约

每个扩展点都必须登记在 `docs/stage0/upstream-diff.md` 台账中。契约一旦冻结，改变它需要走一次评审。

| # | 扩展点 | 位置 | 契约 | 调用方 | 上游升级冲突风险 |
|---|---|---|---|---|---|
| E1 | 路由接线 | 上游 `router/` 各文件末尾 | 每个上游路由文件只加**一行** `apigxroute.Register(router)` | 上游 `router/main.go` | 极低 |
| E2 | 授权收敛 | `middleware/distributor.go:48-78`（pin 分支）与 `:79-99`（非 pin 分支） | **两个分支共用同一个授权函数**；pin 分支也必须执行 token 模型限制等硬限制（当前跳过，见 TD-08） | 上游 distributor | **高**：pin 语义被任务锁、亲和、重试共用 |
| E3 | 渠道 + Key 注入 | `middleware/distributor.go:613 SetupContextForSelectedChannel`（内部 `:677` 调 `channel.GetNextEnabledKey()`） | 上下文携带「已确定的渠道 + 凭据引用」时，必须原样使用；只有未注入才回落到原生随机 | 上游 distributor、`model/channel.go:206` | **高**：`GetNextEnabledKey` 是全渠道共用路径 |
| E4 | 出站前剥离 | relay 出站请求构造处 | 所有 `X-APIGX-*` 头必须在出站前剥离；替换为内部上下文传递 | 上游 relay | 中 |
| E5 | 计费模式 | `service/billing_session.go:377-476`（`NewBillingSession` 的资金来源分支） | `usage_only` 作为新资金来源接入；调用链其余环节不变（见 metering-state §2.3） | 上游 billing session | **高**：计费是全站核心链路 |
| E6 | 计量事件 | 结算与用量写入处 | 追加式 UsageEvent、稳定事件 ID、幂等、校正不覆盖历史 | 上游 billing/settle | 中 |
| E7 | 价格保存目标 | `model/model_pricing_config.go:461 UpdateModelPricing` | 分享者同步结果**不得**写全站 Option；写入其有权管理的供给与 Key 价格配置 | 上游 ratio_sync | **高**：这是「单渠道同步不污染全局」的落点 |
| E8 | 质量统计维度 | `model/perf_metric.go:11-23`（唯一索引 `idx_perf_model_group_bucket`） | 扩展供给与 Key 策略版本维度；改唯一索引即改迁移（见 TD-11） | 上游统计采集 | **高** |
| E9 | 任务产物访问 | `middleware/task_artifact_access.go:174`、`service/task_artifact_access.go:32/48`、`controller/task.go:279` | 默认鉴权下载；签名下载可选、短期、可撤销 | 上游 task 路由 | 高（安全边界） |
| E10 | 注册准入 | `controller/user.go:215 Register` | 新增注册邀请校验；`aff_code` 的推广归因语义保持不变 | 上游注册接口 | 中 |

## 4. 可维护性约束

1. **显式表达四态**：`未配置 / 禁用 / 继承默认 / 明确不限` 必须分别存储（`CODE_STYLE.md` 第 11 条）。`allowed_models` 的「不限」用显式哨兵，不得用空集合或 NULL 表示（`acl-state.md` R-HARD-01a）。
2. **名称不作授权依据**：一律用稳定 ID；展示名变化不回写历史（`CODE_STYLE.md` 第 1 条）。
3. **不可变快照**：价格、汇率、所有权在创建时绑定到 Request / Attempt / AsyncTask，之后不更新（`CODE_STYLE.md` 第 6 条）。
4. **软删除 + 稳定 ID**：被引用过的对象只软删除（`CODE_STYLE.md` 第 2 条）。
5. **秘密分级**：平台 Key 只存不可恢复摘要、上游 Key 加密且主密钥与库分离；日志、错误、监控标签不带凭据（`CODE_STYLE.md` 第 4 条）。
6. **不可信输入**：上游名称/描述/URL、外部 Markdown、回调一律不可信；渲染转义、出站 SSRF 校验（`CODE_STYLE.md` 第 5 条）。
7. **一次请求一个权威决定**：转发器不得二次选择渠道或 Key（`CODE_STYLE.md` 第 7 条）。
8. **错误处理**：对外错误码与不泄露规则按 `acl-state.md` §6；未授权或不存在的供给统一 404；返回脱敏请求 ID。
9. **可测试性**：领域逻辑不依赖 gin 上下文（D1）以便单测；外部依赖（上游、Redis、时钟、汇率）必须可注入或可替身。
10. **命名**：新增表统一 `apigx_` 前缀；新增 Go 包统一放在 `apigx/` 下；前端新模块 `web/src/features/apigx-*/`。

## 5. 测试策略

| 层次 | 覆盖什么 | 工具与约束 |
|---|---|---|
| 领域单元测试 | 授权判定、容量预留/归还、计量口径、价格换算 | Go 标准测试 + `testify`（`require` 建场、`assert` 断言，底座规范）。不依赖数据库与网络 |
| 契约测试 | 供给能力过滤、路由排序、错误码映射 | 表驱动，输入输出明确 |
| 替身集成 | 出站行为、Key 不被二次抽选、扩展头剥离、故障与延迟路径 | `tools/stub-upstream/server.mjs`（已可用：故障注入 503/429/401、ttft、hang + Key 指纹与扩展头取证） |
| 数据库兼容矩阵 | 新增表与迁移在三种库上同时可用 | **SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6 各跑一遍**：全新库 + 从底座旧库升级 + 连续启动两次验幂等。单库通过不算通过（底座 `AGENTS.md`） |
| 缓存相关 | 并发预留、租约、故障降级 | `alicebob/miniredis/v2`（底座已有依赖）用于单测；**真实验收必须用真 Redis** |
| 验收脚本 | 阶段 0 的 5 项验收（两账号 / 两渠道 / 三把 Key） | 脚本化落位 `tools/stage0-verify/`，输出可复核证据 |

**禁止**：只为覆盖率数字的测试、随机输入的伪压力测试、只断言「没有 panic」的 smoke 测试（底座 `AGENTS.md` 测试条款）。

## 6. 上游升级策略

**触发条件**（满足任一即评估升级）：上游发布安全修复；我们需要的新协议适配器只在上游较新提交中存在；上游修掉了我们正在规避的缺陷。

**升级检查清单**（顺序执行，全部通过才算升级成功）：
1. 读上游变更说明，与本项目 `docs/stage0/upstream-diff.md` 台账逐条核对冲突；
2. `tools/dev/upstream-diff.ps1` 确认本地改动清单与台账一致（未登记差异视为流程违规）；
3. 逐条复核扩展点 E1–E10 是否仍适用（尤其 E2/E3/E5/E7/E8 五个高风险点）；
4. `cd new-api/relaykit && GOWORK=off go build ./...`（模块独立性）；
5. 三库迁移矩阵回归（全新库 + 旧库升级 + 幂等）；
6. 替身 5 项验收回归；
7. 更新 `deploy/base.lock.json` 的 commit 与指纹，并记录升级原因。

**回滚**：底座是独立 git 仓库且我们只在其上做「新增文件 + 少量接线」，回滚 = 切回锁定提交并重新应用 `apigx/` 目录；因此 **`apigx/` 目录必须能在不依赖本地未提交改动的情况下重建**。数据库回滚按新增表独立处理（新增表不删，只停用）。

## 7. 与阶段 0 各产出的一致性核对

| 主题 | 各文档结论 | 是否一致 | 取舍 |
|---|---|---|---|
| 数据库范围 | base-lock §7-1 建议「三库可移植、PG 作生产首选」；schema-draft §1.2 采纳并落实到字段类型 | ✅ 一致 | **用户 2026-09-16 决定：按底座规矩走三库兼容，不收窄为只支持 PostgreSQL。** 所有新增表与迁移一律三库可移植、三库验证 |
| 分享分组与用户分组 | reuse-modify §1 与 schema-draft §1.2 均要求独立成表 | ✅ 一致 | 禁止写入 `users.group` / `tokens.group` / `channels.group` |
| 免费计量落点 | metering-state §2 落在 `BillingSession` 的**资金来源**维度 | ✅ 与 base-lock §7-2 一致 | 新增 `UsageOnlyFunding`，不动价格数值 |
| 指定来源授权 | reuse-modify §3.1 要求扩展 Pin 机制；acl-state §1.5 指出**当前 pin 分支跳过 token 模型限制** | ✅ 互相补强 | 收敛为单一授权函数（E2）；这是必须在写代码前修掉的既有缺陷 |
| Key 选择 | reuse-modify §1.2 与 acl-state S12 均要求「唯一权威决定 + 注入」 | ✅ 一致 | 落到 E3 |
| 能力声明 | protocol-matrix §4 指出**底座缺少统一的供给能力声明** | ⚠️ 新增需求 | 必须由我们新增供给层能力记录（`schema-draft` 的 `KeyModelCapability` / `ModelOffering`） |
| 计费不变量 | metering-state §10 与 base-lock §6 一致，并特别指出 `TrustQuota` 旁路会连令牌预扣一起跳过 | ✅ 一致 | APIGX 容量预留**不得**以 `effectiveQuota` 为前提 |

**未发现互相矛盾的结论。** 数据库口径已由用户决定（三库兼容，见 TD-02），无待拍板项。

## 8. 未核实声明

- 本文及所引用的全部阶段 0 文档**都没有经过运行验证**：没有编译、没有部署、没有调用上游、没有跑测试、没有在真实数据库上执行迁移。
- `acl-state.md` 自述未读取 `service/authz` 的 `Permission` 语义与 Casbin 策略；`protocol-matrix.md` 列出的「未核实」项（能力字段、任务取消、部分协议矩阵格）同样未核实。
- 上述「未核实」项在进入实现前必须补齐，不得凭印象编码。
