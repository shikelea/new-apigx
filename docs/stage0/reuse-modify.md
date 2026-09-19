# 底座复用 / 修改清单（文件与函数级）

阶段 0 · T0.3 产出。对象：New API 固定提交 `69a50029819a26c53e6babd276d49cfe2f8880ad`（本地 `new-api/`）。
依据：`docs/PROJECT_PLAN.md` §5（开源选型与复用边界）、§6（架构约束）、§9.2、§11 阶段 0；`docs/stage0/base-lock.md` §6（底座强制规范）、§7（冲突点）。

> 说明：本文所有「文件:行号」均来自本仓库 `new-api/` 固定提交处的实际源码，可逐条打开核对。
> **本次只做源码阅读，没有编译、没有运行、没有调用任何上游。**

## 1. 三条最重要的结论（先看这个）

### 1.1 底座已经有「固定来源」抽象，我们要扩展它，而不是删判断

指定渠道在底座里的实现是**约束 + Pin**，不是散落的 if：

| 位置 | 内容 |
|---|---|
| `middleware/auth.go:536-551` | Token 字符串带后缀时，解析出渠道 ID；**仅管理员**可加 pin：`service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId, Source: PinSourceToken, Rank: PinRankToken, RetryMode: PinRetrySingleAttempt})`；非管理员直接 403「普通用户不支持指定渠道」，并回一个 `specific_channel_version` 头 |
| `service/channel_select.go:15` | `GetChannelConstraints(c)` —— 请求级约束容器 |
| `middleware/distributor.go:48-78` | `constraints.ResolvedPin()`：有 pin 就按 pin 取渠道（`model.CacheGetChannel`），校验 `Status == ChannelStatusEnabled`、`model.ChannelSatisfiesFilters`，失败即中止 |
| `middleware/distributor.go:79-186` | 无 pin 才走随机选路：`service.CacheGetRandomSatisfiedChannel(&service.RetryParam{...})`（`:160`） |

**改动要点**：不要删除 `IsAdmin` 判断就宣称支持「普通用户指定来源」。正确做法是新增一个 Pin 来源（如 `PinSourceOffering`）与「APIGX 资源授权校验」，把「指定来源」纳入同一套约束机制；`IsAdmin` 判断保留（管理员的原语义不破坏），普通用户走新的授权路径。
**为什么**：计划书 §5「必须明确修改的底座行为」第 1 条明确要求「不能只删除管理员判断」，且指定、自动、亲和缓存、重试、任务原渠道锁定都必须经过同一资源授权检查。

### 1.2 Key 的选择发生在渠道确定之后——这正是「不得二次抽选」的注入点

| 位置 | 内容 |
|---|---|
| `middleware/distributor.go:613` | `SetupContextForSelectedChannel(...)`：渠道定下来后写上下文（`ContextKeyChannelId` 等） |
| `middleware/distributor.go:677` | 在其中调用 `key, index, newAPIError := channel.GetNextEnabledKey()` —— **Key 在这里才被选出** |
| `model/channel.go:206` | `GetNextEnabledKey()` 实现 |
| `model/channel.go:252` | `selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]` —— 随机抽选就发生在函数内部 |
| `model/channel.go:637-650` | `channelStatusLock` / `GetChannelPollingLock(channelId)` —— 选择过程互斥锁 |

**结论**：底座当前的顺序是「先定渠道（或 pin 渠道）→ 再由转发链随机定 Key」。计划书 §6 要求「一次请求只有一个权威路由决定」「实际 Key 同样由该调度流程决定，转发器不能在原生 `GetNextEnabledKey` 中再次抽选」。
**改动要点**：由 APIGX 调度在渠道确定后、出站前一次性决定「渠道 + Key」，并把结果**注入**到这条路径（例如上下文里带确定的 Key 引用，`SetupContextForSelectedChannel` 优先使用它，`GetNextEnabledKey` 仅在未注入时才走原生随机）。已预留的额度、价格快照与实际使用的 Key 才会一致。
**注意**：`GetChannelPollingLock` 只保护「选择过程」，**不是并发上限**，不能当作容量控制使用。

### 1.3 免费模式的开关在「资金来源」，不在价格

| 位置 | 内容 |
|---|---|
| `service/billing_session.go:27` | `type BillingSession`；`:377 NewBillingSession(...)` |
| `service/billing_session.go:154 / 196 / 252 / 306` | `Reserve` / `preConsume` / `reserveFunding` / `reserveToken` |
| `service/billing_session.go:43 / 84 / 128` | `Settle` / `Refund` / `NeedsRefund` |
| `service/funding_source.go:42 / 57 / 67` | `WalletFunding.PreConsume / Settle / Refund` |
| `service/funding_source.go:96 / 114 / 121` | `SubscriptionFunding.PreConsume / Settle / Refund` |

**结论**：底座的计费会话已经是「预留 → 结算 → 退款」的框架，且**资金来源被抽象成了 funding source**（钱包 / 订阅）。
**改动要点**：`usage_only`（免费共享模式）应当作为**新的资金来源/模式**接入这条链路：保持鉴权、额度与计量，去掉「充值余额前置条件」。**禁止**通过把模型价格统一设为零、或给所有用户超大余额来实现（计划书 §6 明确禁止，且会破坏估算成本与资源控制）。

## 2. 逐项复用 / 修改清单

「复用方式」三档：**直接复用**（不动上游）｜**加扩展点**（新增文件为主，上游只加少量接线）｜**必须修改**（改上游既有逻辑，技术债最高）。

| # | 能力 | 上游位置（文件:行） | 复用方式 | 我们的改动要点 | 计划书依据 | 上游升级冲突风险 | 触碰计费不变量 |
|---|---|---|---|---|---|---|---|
| 1 | 渠道管理路由 | `router/channel-router.go:19-21`（`/channel` 组 + `middleware.AdminAuth()`） | 必须修改 | 不开放原管理接口给普通用户；新增受 owner 约束的分享者接口（独立路由与鉴权中间件） | §5.2、§11 | 低（新增路由，不改原组） | 否 |
| 2 | 指定渠道/来源选择 | `middleware/auth.go:536-551`、`middleware/distributor.go:48-78`、`service/channel_select.go:15` | 必须修改 | 新增 Pin 来源与「APIGX 供给授权」校验；保留管理员语义；确保自动/亲和/重试/任务锁定走同一授权 | §5「必须修改」1、§9.1 | **高**：pin 语义被多处复用（任务原渠道也用它），改错会影响任务锁定 | 否 |
| 3 | 随机选路 | `middleware/distributor.go:160` `service.CacheGetRandomSatisfiedChannel`、`service/channel_select.go:110` | 加扩展点 | 在候选集里叠加「可见供给、协议/能力兼容、容量余量、价格/预算」过滤与质量排序；保持其重试参数语义 | §9.2、§9.3 | 中 | 否 |
| 4 | 渠道亲和缓存 | `middleware/distributor.go:201` `service.RecordChannelAffinity` | 加扩展点 | 亲和回访必须过同一授权；渠道地址或归属变更时失效旧亲和 | §9.1、§10.3 | 中 | 否 |
| 5 | 多 Key 选择 | `model/channel.go:206`（`GetNextEnabledKey`）、`:252`（`rand.Intn`）、`:637-650`（锁） | 必须修改 | 支持确定的 Key 注入；补逐 Key 状态/冷却/到期/权重/剩余容量策略；补「共享上游账户池」联合约束 | §3.5、§9.2、[N4j] | 中：`GetNextEnabledKey` 是全渠道共用路径 | **是**（Key 决定价格版本与预留对象） |
| 6 | 多 Key 状态与轮换 | `model/channel.go:182` `GetKeys`、`:673 handlerMultiKeyUpdate`、`:725 hasEnabledMultiKey`、`:738 UpdateChannelStatus` | 加扩展点 | 补稳定凭据 ID、逐 Key 消耗统计与历史归属；不把历史任务改绑到池里下一把 Key | §3.5、§10.3 | 中 | 否 |
| 7 | 模型目录发现 | `controller/channel.go:1411 FetchModels`、`controller/channel_upstream_update.go:364 fetchChannelUpstreamModelIDs`、`:467 fetchAdvancedCustomUpstreamModelIDs`、`:513 checkAndPersistChannelUpstreamModelUpdates`、`:858 ApplyChannelUpstreamModelUpdates`、`:914 DetectChannelUpstreamModelUpdates` | 加扩展点 | 复用发现与「新增/变更/消失」差异流程；外层加 owner 约束；新增项进待发布而非自动扩大分享范围 | §3.2、§8.1 | 中 | 否 |
| 8 | 价格匹配（预览/确认） | `setting/ratio_setting/**`、`setting/billing_setting/**` | 加扩展点 | 只读取 New API 当前本地按次价、倍率价和计价表达式；生成服务端候选令牌并写渠道/供给不可变版本；不增加外部价格源适配器 | §8.3、§8.4 | 中 | 否 |
| 9 | 全站模型价格保存 | `model/model_pricing_config.go:461 UpdateModelPricing`、`:224 PreviewModelPricing`、`:347 ValidateModelPricing`（`:480` 版本冲突 `ErrModelPricingConflict`） | 必须修改 | 原生按 `ModelName` 写全站 Option，没有分享渠道 ID；改为把分享者同步结果写入其有权管理的供给与 Key 价格配置，生成不可变价格版本；普通分享者不得改全站价格 | §8.3、§8.4 | **高**：这是「单渠道同步不得污染全局」的落点 | 否（但影响估算） |
| 10 | 上游分组倍率 | `relay/helper/price.go:49 HandleGroupRatio`（`:63 GetGroupGroupRatio`） | 加扩展点 | 成本倍率必须对应**实际执行 Key** 的适用身份；特殊用户组倍率与普通组倍率为替代关系，不重复相乘；不套用 APIGX 自己的用户组倍率 | §8.3 | 中 | **是** |
| 11 | 计费会话（预留/结算/退款） | `service/billing_session.go:27/43/84/128/154/196/252/306/377` | 必须修改 | 新增 `usage_only` 模式；保持鉴权/额度/计量与幂等；不得绕过配额钳制与饱和审计 | §6、§8.5、§9.2 | **高**：计费是全站核心链路 | **是** |
| 12 | 资金来源 | `service/funding_source.go:42/57/67`（Wallet）、`:96/114/121`（Subscription） | 必须修改 | 免费共享作为新资金来源/模式接入；去掉充值余额前置 | §6、§12 | 中 | **是** |
| 13 | 用量与日志采集 | 计费链路与日志生成为一对（计划书 [N4e]）；本清单未逐行定位 | 加扩展点 | 复用采集基础，新增分享者归属、多计量单位、价格与汇率快照、幂等事件与追加校正 | §8.5、§9.5 | 中 | **是** |
| 14 | 性能统计 | `model/perf_metric.go:11-23`（结构）、`:25 TableName`、`:29 UpsertPerfMetric`、`:51 GetPerfMetrics` | 必须修改 | 现有聚合键是 `ModelName + Group + BucketTs`（唯一索引 `idx_perf_model_group_bucket`），**没有渠道/供给维度**；需扩展供给（与 Key 策略版本）维度、分钟桶与冷启动样本量 | §9.4、[N4c] | **高**：改唯一索引即改迁移 | 否 |
| 15 | 用户模型请求限流 | `middleware/model-rate-limit.go:169 ModelRequestRateLimit`、`setting/rate_limit.go:62 CheckModelRequestRateLimitGroup` | 加扩展点 | 这是**用户级窗口限流**，不是渠道 TPM/并发上限；共享容量控制需另建（见 #16） | §9.2、[N4j] | 低 | 否 |
| 16 | 共享容量控制（渠道/Key/账户池并发、RPM/TPM、预算预留、任务槽位） | **底座没有**：`model/channel.go:637-650` 的选择互斥锁仅保护选择过程；`middleware/model-rate-limit.go` 是用户级窗口限流 | 加扩展点（新增为主） | 新增贯穿请求生命周期的容量控制器：原子预留 → 出站前复核 → 幂等归还 → 对账；HTTP 槽位、长流租约、异步任务槽位分别记账 | §9.2、§11 阶段 0/1 | 低（新增），但必须与计费预留协同 | **是** |
| 17 | 注册与邀请 | `controller/user.go:215 Register`、`:272-273`（`affCode` → `GetUserIdByAffCode`）、`:437 GetAffCode` | 必须修改 | `aff_code` 是**推广归因**（邀请人 ID），不是「必须提交有效邀请码才能注册」；需新增真正的注册邀请类型：哈希存储、次数、到期、撤销、并发原子扣减 | §5「必须修改」2、§10.1 | 中 | 否 |
| 18 | 任务产物访问 | `service/task_artifact_access.go:32 IssueTaskArtifactAccess`、`:48 VerifyTaskArtifactAccess`、`:98 BuildTaskArtifactContentURL`；`middleware/task_artifact_access.go:174 TokenOrTaskArtifactAccessAuth`、`:220 IsTaskArtifactAccess`；`controller/task.go:66 GetTaskArtifacts`、`:233 getTaskForArtifactRequest`、`:279 TaskArtifactContent` | 必须修改 | 现状是「Token 或签名能力」二选一（HMAC + 常量时间比较）；需默认改为**鉴权下载**，签名下载若启用必须短期（≤10 分钟）可撤销，且不得把其他用户对象暴露 | §5「必须修改」4、§10.3、[N4f] | 高：产物访问是安全边界 | 否 |
| 19 | 协议转换 DTO | `relaykit/`（独立 Go 模块，`go.mod` 中 `replace ... => ./relaykit`） | 直接复用 | 逐端点核验协议与功能；`relaykit/` 必须保持可独立构建（`GOWORK=off go build ./...`） | §5.2、底座 AGENTS.md | 低（模块边界清晰） | 否 |
| 20 | 前端 | `web/`（React 19 + Bun + Rsbuild 2） | 加扩展点 | 保留可用表单与后台组件；重做分享广场与分享者工作流；必须复用既有组件与 i18n `t()` | §5.2、底座 `web/AGENTS.md` | 中 | 否 |

## 3. 四项「必须修改的底座行为」的具体修改点

### 3.1 来源选择授权
- 现状：`middleware/auth.go:536-551` 仅管理员可 pin；`middleware/distributor.go:48-78` 按 pin 取渠道，但不校验「该用户是否有权使用该供给」。
- 修改：① 新增 Pin 来源常量与「供给授权」校验；② pin 命中后除状态与过滤器外，再校验平台 Key 的硬限制（允许模型/允许来源/固定来源/是否允许回退）；③ 确保任务原渠道 pin（`PinSourceOriginTask`，`middleware/distributor.go:57-66`）、亲和缓存、重试路径都经过同一校验；④ 非授权供给对外统一 404。
- 最小侵入：把校验放进 `distributor.go` 的 pin 分支与 `ChannelSatisfiesFilters` 的过滤器体系，而不是在每个调用点加 if。

### 3.2 邀请准入
- 现状：`controller/user.go:272-273` 的 `affCode` 只用于查邀请人 ID 写入 `InviterId`，**不阻断注册**。
- 修改：新增注册邀请实体与校验（哈希、次数、到期、撤销、并发原子扣减），与「是否开放注册 / 是否必须邮箱验证」组合开关并存；不得复用 `aff_code` 的字段名来宣称功能完成。

### 3.3 免费计量
- 现状：`service/billing_session.go` + `service/funding_source.go` 已经抽象了预留/结算/退款与资金来源。
- 修改：新增 `usage_only` 模式（新资金来源或模式分支），使其**不依赖充值余额**但仍记录用量、估算成本与资源额度。
- 禁止：把价格设为零、给用户超大余额、跳过计量。

### 3.4 任务产物权限
- 现状：`middleware/task_artifact_access.go:174` 提供「Token 或签名能力」认证；`service/task_artifact_access.go:32/48` 用 HMAC 签发与常量时间校验。
- 修改：默认走鉴权下载（Token + 任务归属校验）；签名能力保留为可选，启用时必须短有效期与可撤销，且签发范围绑定任务 ID 与用户；查询、取消、下载、轮询、回调全部校验归属。

## 4. 扩展点集中目录建议

目的：让相对上游的改动**可枚举、可回滚、可升级**。原则是「新增文件 > 上游扩展点 > 改上游逻辑」。

```
new-api/
├── apigx/                        # 我们全部新增代码集中在这里（新目录，不改上游既有文件）
│   ├── domain/                   # 共享领域：分享分组、供给、Grant、容量与计量的领域模型与不变量
│   ├── api/                      # 分享者与使用者接口（受 owner 约束），独立路由与鉴权中间件
│   ├── route/                    # 路由注册入口，由上游 router/ 里各调用**一行**
│   ├── relayhook/                # 与 relay 的接线：路由决定注入、Key 注入、计量钩子、扩展头剥离
│   ├── capacity/                 # 共享容量控制（并发/速率/预算预留/任务槽位/对账）
│   ├── price/                    # New API 本地价格匹配与渠道/供给不可变版本
│   └── model/                    # 新增 GORM 实体（三库兼容）
└── web/src/features/apigx-*/     # 前端新功能模块，复用既有组件与 i18n
```

上游文件里允许出现的改动只有三类，且每处都要在 `docs/stage0/upstream-diff.md` 登记：
1. **接线一行**：在上游 `router/` 注册我们的路由组；在上游 relay 链路的固定位置调用我们的钩子。
2. **注入点**：`SetupContextForSelectedChannel`（`middleware/distributor.go:613`）与 `GetNextEnabledKey`（`model/channel.go:206`）接受「已确定的渠道 + Key」。
3. **必须修改**：`middleware/auth.go` 的 pin 授权、`controller/user.go` 的注册邀请、`model/model_pricing_config.go` 的保存目标、`model/perf_metric.go` 的聚合维度、计费会话的 `usage_only`。

## 5. 完全不需要改上游、直接复用的部分

| 能力 | 位置 | 说明 |
|---|---|---|
| 厂商适配与协议转换 | `relay/`、`relaykit/` | 逐端点核验后直接复用 |
| 流式输出与超时基础 | relay 执行链 | 复用执行基础，补完整流统计与安全重试 |
| 账号会话与令牌校验 | 上游认证链（`middleware/auth.go` 主体） | 复用基础，不另起第二套用户系统 |
| 管理后台表单与组件 | `web/src/components/` | 复用组件，不另起样式体系 |
| 多 Key 启停/随机/轮询基础 | `model/channel.go:182/206/673/725` | 复用配置与选择逻辑，只在其上补策略与容量 |
| 任务查询的用户归属 | `controller/task.go:233 getTaskForArtifactRequest` | 已有归属校验，复用 |

## 6. 未核实 / 未验证声明

- 本文**只做了源码阅读**：没有编译、没有运行、没有部署、没有调用任何上游、没有跑过任何测试。
- 以下条目尚未定位到精确行号，执行前必须补齐（不得凭印象实现）：
  - 用量与日志的采集与生成为一对（#13），本清单未逐行定位；
  - 「渠道并发/TPM 不存在」是**否定性结论**，依据是计划书 [N4j] 与本轮对 `model/channel.go:637-650`、`middleware/model-rate-limit.go:169` 的阅读；实现前应对 `service/` 与 `relay/` 做一次全量检索确认，避免遗漏隐藏的容量控制。
  - 模型别名、模型映射表（`ModelAlias`）与 `/api/pricing` 解析的确切位置未定位。
- 「上游升级冲突风险」一列是基于调用点重要程度的主观评估，不是对上游代码变更频率的统计。
