# 相对上游的改动台账

这是「我们对 New API 到底改了什么」的唯一权威台账。配套脚本 `tools/dev/upstream-diff.ps1` 给出**实际差异**，本表给出**为什么改**；两者不一致即视为流程违规。

## 规则

1. **先登记再改**：任何改动上游已有文件的行为，都必须先在本表登记，再动代码。
2. **优先顺序**：新增文件 > 通过上游扩展点接入 > 修改上游既有逻辑。第三类是最高技术债，必须写明原因与上游升级时的冲突风险。
3. **不改上游标识**：`New API` / `QuantumNous` 相关的品牌、署名、模块路径、镜像名等一律保留，只增不改（依据 `new-api/AGENTS.md` 项目治理条款）。
4. **不写进上游文档目录**：新增设计文档一律放本仓库 `docs/`，不在 `new-api/docs/` 或 `new-api/plugins/**` 下新建文档。
5. **升级前必查**：升级上游版本时逐条核对本表，确认每条改动是否仍适用、是否被上游同名改动覆盖。

## 当前统计

- 基线提交：`69a50029819a26c53e6babd276d49cfe2f8880ad`
- 修改上游文件：**0** 个
- 新增未跟踪文件：**0** 个
- 核对时间：2026-09-16（`tools/dev/upstream-diff.ps1`）

## 一、修改上游已有文件的改动

| # | 路径 | 类型 | 改动内容 | 原因 / 计划书依据 | 上游升级风险 | 登记日期 | 状态 |
|---|---|---|---|---|---|---|---|
| D-001 | `new-api/middleware/distributor.go` | 必须修改 | 把 token「允许模型」硬限制的校验从**非 pin 分支内部**提取为共用函数 `enforceTokenModelLimit`，并提前到 pin / 非 pin 分支**之前**执行 | TD-08；计划书 §5.2 第 1 条「不能只删除管理员判断；指定、自动、亲和缓存、重试和任务原渠道锁定都必须经过同一资源授权检查」 | **中高**：`Distribute()` 是所有转发请求的必经入口（含任务原渠道 pin）。该文件在上游属高频改动区，升级时需重新核对 pin 分支与模型限制的相对位置 | 2026-09-16 | 已实施 |
| D-001-注 | 同上 | — | 仅影响**启用了 token 模型限制**的令牌（`token_model_limit_enabled=true`）；未启用限制的令牌行为完全不变 | 影响面收敛，便于回归 | — | 2026-09-16 | — |
| D-002 | `new-api/middleware/task_plugin_origin_task_test.go` | 追加 | 追加 2 个回归测试：`TestDistributePinnedRequestStillEnforcesTokenModelLimit`（反例：pin 下未授权模型必须 403）与 `TestDistributePinnedRequestAllowsAuthorizedModel`（正例：pin 下已授权模型仍放行） | D-001 的回归保护；底座 AGENTS.md 要求「扩展既有测试文件，不为小改动散落测试」 | 低：仅追加测试函数，不改上游既有用例 | 2026-09-16 | 已实施 |
| D-003 | `new-api/model/main.go` | 接线 | 在 `migrateDB()` 末尾（底座所有迁移完成之后）加 5 行：调用 `apigxmodel.Migrate(DB)`，失败即返回错误。另加 1 行 import | T1.3：让 APIGX 的 27 张领域表随底座启动创建；只增不改、幂等，不触碰底座表 | 低（新增调用点，不改既有迁移顺序）；升级时需核对 `migrateDB()` 末尾是否仍是合适的挂载点 | 2026-09-16 | 已实施 |
| D-005 | `new-api/service/billing.go` | 修改 | 新增常量 `BillingSourceUsageOnly = "usage_only"` | T1.6：免费共享模式需要独立的资金来源标识 | 低（仅新增常量） | 2026-09-16 | 已实施 |
| D-006 | `new-api/service/billing_session.go` | 必须修改 | ① `NewBillingSession` 最前面加分支：`IsUsageOnlyMode()` 时走 `newUsageOnlySession`（跳过 tryWallet 的余额前置）；② `reserveFunding` 的类型 switch 增加 `case *UsageOnlyFunding` 返回 nil | T1.6；计划书 §6「免费模式必须改结算路径而非数值」 | **中高**：计费会话是全站核心链路。已加 4 个单测 + 运行时 A/B 验收（默认行为零回归） | 2026-09-16 | 已实施 |
| D-009 | `new-api/middleware/distributor.go` | 必须修改 | 在 `SetupContextForSelectedChannel` 之后、`c.Next()` 之前插入容量钩子：`relayhook.Acquire` 取租约、`defer lease.Release()` 在下游全部结束后释放；满载 429、账本不可用 503。另加 1 行 import | T1.7 第二部分；计划书 §9.2（共享容量控制必须贯穿请求生命周期） | **中高**：`Distribute()` 是所有转发请求的必经入口（含任务原渠道 pin）。改动本身是「新增一个可开关的调用」，默认关闭时行为不变 | 2026-09-16 | 已实施（默认关闭，`APIGX_CAPACITY_ENABLED`） |
| D-012 | `new-api/router/api-router.go` | 接线 | 新增 4 行：APIGX 独立路由组 + `UserAuth` + `apigxroute.Register`；另加 1 行 import | T1.4：分享者接口必须独立于 `/channel` 那组管理员路由（渠道是全局资源、那组挂 AdminAuth） | 低（新增一组路由，不动既有路由） | 2026-09-16 | 已实施 |
| D-016 | `new-api/middleware/distributor.go` | 必须修改 | 在 `SetupContextForSelectedChannel` 设置完 Key 索引之后加 3 行：`relayhook.AcquireForAttempt(c, channel.Id)`，失败则返回错误；`Distribute` 尾部改为只 `defer relayhook.ReleaseFromContext(c)` | T1.7：底座的失败重试会**重新选渠道**并再次经过本函数，容量租约必须**按每次尝试**获取，否则重试换渠道后仍占用旧渠道的租约、新渠道完全没做容量检查 | **中**：该函数是选路后的必经点（初次与每次重试）。行为仍由默认关闭的开关控制 | 2026-09-17 | 已实施 |
| D-015 | `new-api/middleware/distributor.go` | 接线 | 在 `c.Next()` 之后、响应状态 < 400 的分支里（底座自己在此记录渠道亲和性）加 1 行：`channelpool.RecordSuccessFromContext(c, channel.Id)` | T1.5：成功路径的逐 Key 统计（否则 `LastOKAt` 永远为 0，分享者看不出哪把 Key 可用） | 低：复用底座**已有的成功点**，不新增钩子位置；默认关闭 | 2026-09-16 | 已实施（`APIGX_KEY_STATS_ENABLED`，默认关闭） |
| D-014 | `new-api/service/relay_error.go` | 接线 | 在 `ProcessChannelError` 的自动禁用块之后加 2 行：调用 `channelpool.RecordFailureFromContext(c, channelId, statusCode, errorCode)`；另加 1 行 import | T1.5：逐 Key 统计与 Key 级故障隔离需要请求失败时的结果 | **中**：`ProcessChannelError` 是所有渠道失败必经之路。改动是「新增一个默认关闭的调用」，关闭时不产生任何查询 | 2026-09-16 | 已实施（`APIGX_KEY_STATS_ENABLED`，默认关闭） |
| D-013 | `new-api/apigx/{secretbox,api,route}/**` | 新增 | 上游凭据加密封装（AES-256-GCM 信封、失败关闭）、分享者接口、路由入口 | T1.4/T1.5；计划书 §10.3（上游 Key 加密存储、主密钥与库分离） | 无（上游不存在同名路径） | 2026-09-16 | 已实施 |
| D-011 | `new-api/apigx/model/{repo_share,repo_upstream,repo_test}.go` | 新增 | 领域仓储层：分享分组/渠道/上游账户池/上游 Key 的 CRUD 与不变量（软删除墓碑、**服务端强制归属**、Key 池筛选、参数校验）；8 个测试 | T1.4/T1.5/T1.8 的共同地基；计划书 §9.2、§10.3 | 无（上游不存在同名路径） | 2026-09-16 | 已实施 |
| D-010 | `new-api/apigx/relayhook/{capacity_hook,capacity_hook_test}.go` | 新增 | 容量接线：`Acquire`/`Lease.Release`/长流续租协程/错误码映射（429+Retry-After、503）；为渠道+平台 Key+用户三维度做同一次原子预留 | T1.7 | 低（新包；不反向依赖 middleware，避免循环） | 2026-09-16 | 已实施 |
| D-008 | `new-api/apigx/capacity/{capacity,capacity_test}.go` | 新增 | 共享容量账本：Lua 原子预留/幂等归还/租约续租与过期回收；维度 key 覆盖用户·平台 Key·渠道·供给·上游 Key·上游账户池 | T1.7；计划书 §9.2（底座没有渠道/Key 级并发上限，阶段 0 已实测确认） | 无（上游不存在同名路径） | 2026-09-16 | 已实施（核心账本） |
| D-007 | `new-api/service/apigx_usage_only.go`、`apigx_usage_only_test.go` | 新增（同包） | 模式开关（`APIGX_USAGE_ONLY`）、`UsageOnlyFunding`、`newUsageOnlySession` 及其单测 | T1.6 | 低（新文件；与底座 `funding_source.go` 同包，便于实现 FundingSource 接口而不产生循环依赖） | 2026-09-16 | 已实施 |
| — | 补丁导出 | — | `deploy/patches/0001-*.patch`（D-001/D-002）、`0002-*.patch`（D-003/D-004）；fork 分支 `apigx`，HEAD `c96af9d` | 让工作区仓库可追溯 fork 状态，且能从固定提交复现 | — | 2026-09-16 | 已导出 |

## 二、新增文件（不修改上游）

新增代码集中在 `new-api/apigx/`（新目录，不改上游既有文件）与 `new-api/web/src/features/apigx-*/`。
目录划分与理由见 `docs/stage0/reuse-modify.md` 第 4 节。

| 目录 / 文件 | 用途 | 对应任务 | 登记日期 |
|---|---|---|---|
| `new-api/apigx/domain/` | 共享领域模型与不变量（分享分组、供给、Grant、容量与计量） | T0.4 / T0.6 | 2026-09-16 |
| `new-api/apigx/api/` | 分享者与使用者接口（受 owner 约束，独立鉴权中间件） | T0.5 | 2026-09-16 |
| `new-api/apigx/route/` | 路由注册入口，上游 `router/` 只加一行调用 | T0.3 | 2026-09-16 |
| `new-api/apigx/relayhook/` | 与 relay 链路的接线：路由决定注入、Key 注入、计量钩子、扩展头剥离 | T0.6 | 2026-09-16 |
| `new-api/apigx/capacity/` | 共享容量控制（并发/速率/预算预留/任务槽位/对账） | T0.6 | 2026-09-16 |
| `new-api/apigx/pricing/` | 价格来源适配（模眼、ModelRadar）与渠道价格版本 | 阶段 2 | 2026-09-16 |
| `new-api/apigx/model/` | 新增 GORM 实体（三库兼容） | T0.4 | 2026-09-16 |
| `new-api/web/src/features/apigx-*/` | 前端新功能模块，复用既有组件与 i18n | 阶段 1 | 2026-09-16 |

## 三、允许直接沿用、不改动的上游能力

位置已由 T0.3 精确定位（依据 `docs/stage0/reuse-modify.md`）：

| 能力 | 上游位置（文件:行） | 我们的用法 |
|---|---|---|
| 厂商适配器与协议转换 | `relay/`、`relaykit/`（独立模块，`go.mod` 有 `replace ... => ./relaykit`） | 直接复用，逐端点核验；保持 `relaykit` 可独立构建 |
| 模型目录发现 | `controller/channel.go:1411 FetchModels`、`controller/channel_upstream_update.go:364/467/513/858/914` | 复用发现与「新增/变更/消失」差异流程，外层加 owner 约束 |
| 价格同步抓取与预览 | `controller/ratio_sync.go:211 FetchUpstreamRatios`、`router/api-router.go:245` | 复用抓取、多来源预览、差异确认与版本冲突；仅改保存目标 |
| 计费预留/结算框架 | `service/billing_session.go:27/43/84/128/154/196/252/306/377` | 复用框架，新增 `usage_only` 资金来源 |
| 用户级模型限流 | `middleware/model-rate-limit.go:169`、`setting/rate_limit.go:62` | 复用（注意：它不是渠道并发/TPM 上限） |
| 性能统计采集 | `model/perf_metric.go:11-23/25/29/51` | 复用采集，扩展供给与 Key 策略版本维度 |
| 多 Key 启停与轮询基础 | `model/channel.go:182/206/673/725/738` | 复用选择逻辑，补策略、容量与逐 Key 统计 |
| 任务归属校验 | `controller/task.go:233 getTaskForArtifactRequest` | 复用，另在产物访问上收紧默认策略 |
| 账号会话与令牌校验 | `middleware/auth.go` 主体 | 复用，不另起第二套用户系统 |
| 管理后台组件 | `web/src/components/` | 前端复用，不另起样式体系 |
