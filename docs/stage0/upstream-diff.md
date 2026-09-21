# 相对上游的改动台账

这是「我们对 New API 到底改了什么」的唯一权威台账。配套脚本 `tools/dev/upstream-diff.ps1` 给出**实际差异**，本表给出**为什么改**；两者不一致即视为流程违规。

## 规则

1. **先登记再改**：任何改动上游已有文件的行为，都必须先在本表登记，再动代码。
2. **优先顺序**：新增文件 > 通过上游扩展点接入 > 修改上游既有逻辑。第三类是最高技术债，必须写明原因与上游升级时的冲突风险。
3. **区分产品品牌与上游标识**：面向用户的 fork 产品名和默认系统名使用 `APIGX`；Go 模块路径、真实渠道类型、上游仓库链接、版权头、许可证与 `New API` / `QuantumNous` 署名必须保留。禁止无差别全局替换（依据用户的开源决定与 `new-api/AGENTS.md` 项目治理条款）。
4. **不写进上游文档目录**：新增设计文档一律放本仓库 `docs/`，不在 `new-api/docs/` 或 `new-api/plugins/**` 下新建文档。
5. **升级前必查**：升级上游版本时逐条核对本表，确认每条改动是否仍适用、是否被上游同名改动覆盖。

## 当前统计

- 基线提交：`69a50029819a26c53e6babd276d49cfe2f8880ad`
- 当前 fork 提交：`bf36a3a99d5e30da230898ee9eec335902163092`
- 相对基线提交数：**172** 个
- 变化文件：**323** 个（新增 **174**，修改上游已有文件 **149**）
- 行数变化：新增 **46,185** 行，删除 **2,365** 行
- 未跟踪文件：**0** 个
- 核对时间：2026-09-21（`git diff --name-status/--shortstat`；数据看板共享用量本地化收口）

> 2026-09-18 接手审查纠正：本节此前仍写“修改上游文件 0 个”，那只是阶段 0 早期快照，已经与当前 fork 不符。完整风险与整改顺序见 `docs/TAKEOVER_AUDIT.md`。

## 一、修改上游已有文件的改动

| # | 路径 | 类型 | 改动内容 | 原因 / 计划书依据 | 上游升级风险 | 登记日期 | 状态 |
|---|---|---|---|---|---|---|---|
| D-001 | `new-api/middleware/distributor.go` | 必须修改 | 把 token「允许模型」硬限制的校验从**非 pin 分支内部**提取为共用函数 `enforceTokenModelLimit`，并提前到 pin / 非 pin 分支**之前**执行 | TD-08；计划书 §5.2 第 1 条「不能只删除管理员判断；指定、自动、亲和缓存、重试和任务原渠道锁定都必须经过同一资源授权检查」 | **中高**：`Distribute()` 是所有转发请求的必经入口（含任务原渠道 pin）。该文件在上游属高频改动区，升级时需重新核对 pin 分支与模型限制的相对位置 | 2026-09-16 | 已实施 |
| D-001-注 | 同上 | — | 仅影响**启用了 token 模型限制**的令牌（`token_model_limit_enabled=true`）；未启用限制的令牌行为完全不变 | 影响面收敛，便于回归 | — | 2026-09-16 | — |
| D-002 | `new-api/middleware/task_plugin_origin_task_test.go` | 追加 | 追加 2 个回归测试：`TestDistributePinnedRequestStillEnforcesTokenModelLimit`（反例：pin 下未授权模型必须 403）与 `TestDistributePinnedRequestAllowsAuthorizedModel`（正例：pin 下已授权模型仍放行） | D-001 的回归保护；底座 AGENTS.md 要求「扩展既有测试文件，不为小改动散落测试」 | 低：仅追加测试函数，不改上游既有用例 | 2026-09-16 | 已实施 |
| D-003 | `new-api/model/main.go` | 接线 | 在 `migrateDB()` 末尾（底座所有迁移完成之后）加 5 行：调用 `apigxmodel.Migrate(DB)`，失败即返回错误。另加 1 行 import | T1.3：让 APIGX 的 27 张领域表随底座启动创建；只增不改、幂等，不触碰底座表 | 低（新增调用点，不改既有迁移顺序）；升级时需核对 `migrateDB()` 末尾是否仍是合适的挂载点 | 2026-09-16 | 已实施 |
| D-029 | `new-api/main.go` + `apigx/startup/**` | 新增校验器 + 接线 | `InitResources` 在数据库、Redis、选项和日志库完成初始化后、后台任务启动前执行 APIGX 配置一致性检查；共享模式强制 usage-only、来源守卫、容量、Key/质量统计、熔断、策略路由、TPM 调整、Redis、64 位十六进制主密钥和 MySQL InnoDB | 接手整改阶段 A：避免共享平台在关键保护开关缺失或互相矛盾时仍带病启动；通用 New API 默认配置保持兼容 | 低到中：`main.go` 新增一个失败关闭的启动点；上游升级时需确认资源初始化顺序仍保持“依赖已就绪、后台任务未启动” | 2026-09-18 | 已实施并实测 MyISAM 拒绝 / InnoDB 接受 |
| D-030 | `new-api/common/redis.go`、`model/{token_cache,user_auth_cache,user_session}.go` | 兼容修复 | Go Pipeline 的 map 写入改为逐字段 `HSET`；Lua 多字段写入改用 `HMSET`，保留原事务、TTL、认证栅栏与会话版本语义 | 本地锁定环境使用 Redis 3.0.504；Redis 4 才支持多组 field/value 的 `HSET`，原实现持续报 `Wrong number of args` 并导致登录后缓存接口异常 | 中：触及底座缓存公共路径；已用真实 Redis 完成登录、用户信息、令牌接口和全仓回归，升级 Redis 后仍兼容 | 2026-09-18 | 已实施 |
| D-031 | `new-api/service/channel_affinity_*_test.go`、`controller/{access_token_audit,security_enrollment}_test.go` | 测试隔离修复 | 亲和缓存夹具改用进程内原子序列；SQLite 使用 WAL + 有界 busy timeout；捕获并关闭每次 `InitDB/InitLogDB` 新建的连接，避免全局句柄覆盖后泄漏 | 接手整改阶段 A：Windows 下纳秒时钟键碰撞、SQLite 文件占用和并发写锁令整包/全仓测试不可信 | 低：只改测试；SQLite 并发用例各 20 次、service/controller 整包与全仓通过 | 2026-09-18 | 已实施 |
| D-032 | `new-api/relay/channel/api_request_getbody_test.go` | 测试契约与服务端夹具修复 | HTTP/2 不可重放请求的负例不再绑定 Go/Windows socket 内部错误文案；GOAWAY 重放夹具先半关闭写端并排空对端数据，使客户端先观察到优雅 GOAWAY，再继续断言请求失败/重放次数与请求体等可观察契约 | Windows 在未读取 SETTINGS ACK 时全关闭连接会把 GOAWAY 转成 `WSAECONNRESET`，令测试随机错过预期重放路径；这是夹具时序问题，不改变生产请求逻辑 | 低：只改测试；不可重放负例定向 100 次、GOAWAY 重放 20 次、整包与全仓均通过 | 2026-09-19 | 已实施 |
| D-033 | `new-api/{middleware/distributor.go,dto/channel_constraints.go,model/channel_constraint.go,relay/**,controller/{task,video_proxy}.go}` + `new-api/apigx/{model,relayhook,routing}/**` | 统一资源授权与出站复验 | 新增按 owner、公开、有效 Grant、资源存活/发布状态计算的授权快照；把平台 Key 模型白名单、固定 Offering、允许分享者与资源 ACL 求交，并在固定来源、自动/策略、亲和、随机、重试、任务原渠道、Midjourney、Responses WebSocket 和历史任务上游代理路径统一过滤与出站前复验；已撤销 Grant 不再可见，已停用/删除的 APIGX 渠道仍保留受管理身份，拒绝统一为 503 | 接手审查 P0：此前非公开、已撤权或已停用的分享资源可经底座候选、历史 channel ID 或旁路入口继续出站；计划书要求所有选路入口共享一条授权链且授权先于质量、容量与 Key 选择 | **中高**：触及底座分发、任务和 WebSocket 高频路径；已用身份矩阵、受影响包、全仓、本地 PostgreSQL+Redis+真实网关/本地替身闭环验证，仍需后续真实第三方上游与生产多实例验证 | 2026-09-19 | 已实施 |
| D-034 | `new-api/apigx/api/share.go`、`apigx/model/repo_offering.go` 及既有测试文件 | reviewer-scoped 审核 | 新增管理员待审核队列；发布/驳回按资源 ID 跨 owner 条件更新，并在同一事务写入脱敏不可变审计事件；分享者 CRUD 继续保持 owner 隔离；审核理由限制为安全码 | 接手审查 P0-3：原审核复用了 owner-scoped 仓储，管理员无法审核其他分享者的 Offering | 低到中：只改 APIGX 审核接口与仓储；已完成普通用户拒绝、跨 owner 发布/驳回、重复审核、审计唯一性、三库和全仓回归 | 2026-09-19 | 已实施 |
| D-035 | `new-api/apigx/api/share.go`、`apigx/channelpool/projection.go`、`apigx/model/repo_share.go` 及既有测试文件 | 渠道原子生命周期 | 普通用户只能按配方代建专属底座渠道；底座/APIGX 渠道/初始加密 Key 同事务创建；供给发布、审核审计、底座模型与能力同事务提交；删除先停用底座与能力并清缓存，再幂等清空明文 Key、模型、多 Key 状态和能力，最后软删领域渠道 | 接手审查 P0：原创建失败会留下启用孤儿底座，发布能力投影失败会留下不可路由的已发布供给，删除只软删领域行且底座仍可转发 | 中：触及分享渠道创建、发布与删除；已完成逐步骤故障注入、重复删除、SQLite/PostgreSQL/MySQL 矩阵、真实 PostgreSQL+Redis HTTP 和全仓回归 | 2026-09-19 | 已实施 |
| D-036 | `new-api/{middleware/distributor.go,service/{billing_session,text_quota}.go,relay/common/relay_info.go}`、`new-api/apigx/{relayhook,routing,qualityhook,model,api,channelpool}/**`、平台 Key 前端文案 | 默认 `auto` 综合质量路由 | 空值和 `auto` 均按 6 小时可靠性、延迟、文本吞吐与实时容量综合排序；授权、熔断、满载先排除，无候选失败关闭；移除包含实时占用的最终优胜集合缓存；容量策略更新立即清准入缓存；文本成功样本成对累计 token/生成时长，探测、平台拒绝、取消与结算重试不污染质量 | 接手审查 P0-2：原 `auto` 明确绕过质量决策并退回底座随机，最终路由缓存还会让满载后的请求持续撞同一渠道 | 中高：触及每请求候选与结算质量采集；已完成定向/全仓回归、三库质量矩阵、最终二进制真实 HTTP 五策略与满载切换验收 | 2026-09-19 | 已实施 |
| D-037 | `new-api/model/channel.go`、`apigx/{channelpool,model,relayhook}/**`、`controller/{relay,system_task_handlers,task_cancel}.go`、`service/task_polling.go`、任务/视频路由 | 稳定多 Key 与异步任务生命周期 | 每把真实 Key 只保留一个槽位，稳定 Key ID、原生权重和健康/容量/熔断独立来源进入底座选择器；新增加密凭据版本历史、不可变任务快照、历史凭据查询、状态 CAS、持久修复事件、任务租约移交/续租/幂等释放、取消能力契约和 501 明确限制 | 接手审查 P1-1/P1-2：旧权重靠复制明文槽位，索引状态会串 Key；异步任务只写快照，不按历史凭据查询，也没有终态、取消与重启对账闭环 | **中高**：触及底座多 Key 选择、任务提交/轮询和容量租约；已完成三库矩阵、全仓、真实 PostgreSQL+Redis HTTP、Key 轮换/删除、真实进程中断恢复与 162 补丁重建；真实第三方取消仍未实现 | 2026-09-19 | 已实施 |
| D-038 | `new-api/apigx/{price,model,relayhook,api}/**`、`service/{quota,text_quota,task_polling,token_counter}.go`、`relay/{common,channel/openai}/**`、Kling 插件与分享者用量卡 | New API 本地价格匹配与六类统一计量 | 一键匹配只读 New API 当前本地按次价、倍率价和计价表达式，以服务端候选令牌确认并写不可变价格版本；同步与异步事件统一记录真实能力、维度、单位、可信度和冻结成本；分享者按渠道、Key、模型查看净用量、重试和四类成本状态 | 接手审查 P1-3/P1-4：原一键匹配要求客户端上传读数，图片假 token、语音秒数和异步任务最终 facts 不能形成可追溯成本，分享者汇总也会重复或漏算 | **中高**：触及结算后计量、异步终态、视频状态查询和分享者聚合；三库、阶段 D HTTP 20/20、全仓、前端构建/typecheck/定向用例、最终二进制与 163 补丁重建均通过 | 2026-09-19 | 已实施 |
| D-039 | `new-api/web/src/features/{share,pricing,keys,apigx-platform}/**`、`new-api/apigx/{api/share.go,model/repo_offering.go,model/repo_plaza.go,model/repo_upstream.go}` | 三角色前端任务流 | “我的分享”按分组和渠道就绪链组织，补多 Key 权重/轮换、模型导入、本地价格匹配、容量与审核；模型详情用公开 Offering ID 写入指定来源或自动策略；管理员从待审核队列与资源选择器处理价格、别名和发布；窄屏改用完整操作卡片 | 接手审查 P1-7：原页面按表结构平铺，分享者缺 Key/价格流程，使用者与管理员需手填内部 ID，移动端关键操作不可见 | **中**：主要改前端信息架构与 APIGX 查询/更新端点；已完成 typecheck、变更文件 lint/format、11 文件 42 项 Vitest、生产构建、Go 全仓、三库及 390×844 浏览器回归 | 2026-09-20 | 已实施；P1-7 关闭，P1-6 的邀请组件与协议覆盖留待阶段 F |
| D-040 | `new-api/apigx/api/{share.go,share_test.go}`、`new-api/web/src/features/{share,access}/**`、七种语言文件 | 安全自助渠道配方 + 邀请控件 | 服务端拥有普通用户配方白名单并按稳定 `recipe_id` 重新解析底座渠道类型；目录不泄露底座类型，未知配方零写入，三对象仍同事务创建；前端加载目录并处理完整异步状态，邀请改用项目 Select 和移动卡片，终态邀请不再重复撤销 | 接手审查 P1-6：客户端原先直接提交协议字符串，邀请残留原生选择器且移动表格横向滚动；“New API”还被误称为协议 | **中**：改变普通用户自助创建请求契约；管理员绑定既有底座渠道入口保持兼容。已通过三库生命周期、Go 全仓、8 文件 25 项前端测试、构建和 390×844/1440×1000 浏览器回归 | 2026-09-20 | 已实施；P1-6 的组件与任意类型注入缺口关闭，专用任务型协议继续留在 F2 |
| D-041 | `new-api/apigx/api/{share.go,share_test.go}`、`new-api/web/src/features/share/**`、七种语言文件 | 内置异步任务配方复用 | 在服务端白名单增加阿里百炼、可灵、Vidu、豆包 Seedance、Gemini Veo、OpenAI Sora、海螺、即梦 8 个配方；统一创建 `ChannelTypeTaskPlugin` 渠道并固定插件身份，目录不返回底座类型/插件键；模型抓取读取已启用插件声明；可灵/即梦校验 `accessKey|secretKey` 或 `sk-` 中转 Key；前端显示视频能力和创建后的抓取、定价、审核步骤 | 接手审查 P1-6 / 阶段 F2：视频不能伪装成通用同步协议，也不应为每个供应商重写中继；应复用 New API 已有任务插件及其模型声明 | **中**：普通用户新增 8 种可见配方，底座任务插件注册表成为可用性来源；已通过目录泄漏/错误凭据零写入/原子创建/模型抓取、三库生命周期、Go 全仓、前端 26 项及 390×844/1440×1000 浏览器回归。Vertex AI 因还需服务账号、项目和区域配置继续关闭 | 2026-09-20 | 已实施；F2 安全配方覆盖完成，真实第三方账号联调仍属上线前验证 |
| D-042 | `new-api/apigx/{api,model,relayhook,routing}/**`、`new-api/web/src/features/{access,dashboard,keys,plaza,pricing,share,system-settings}/**`、路由、侧边栏与七种语言文件 | 新用户资源工作流与信息架构 | 广场把资源归为公开、本人私有、别人分享给我，同模型 ID 聚合；卡片保留 6 小时可靠性、P50 延迟、文本吞吐，详情保留来源级指标与参考价格；访问授权拆为 `/access`，共享用量并入数据看板；平台 Key 使用同一资源范围并支持综合自动/指定来源；价格同步应用后刷新当前系统价格基线 | 接手整改 G1：原 ShareGroup、余额、重复用量和资源来源概念直接暴露在普通用户主流程，广场与 Key 的范围语义不一致，价格同步后前端仍可能沿用旧基线；模型卡片质量指标必须保留 | **中**：改变广场可见资源、平台 Key 范围与多个主要页面；已通过前端全量 1771 项、typecheck、构建、Go 全仓、三库矩阵及 390×844/1440×1000 浏览器回归。质量证据仍是本地样本，不代替生产流量和完整设备审计 | 2026-09-20 | 已实施；ShareGroup 继续作为后端授权/发布边界，普通用户一级导航统一为资源范围 |
| D-043 | `new-api/{model/channel.go,middleware/distributor.go,controller/relay.go,service/relay_error.go}`、`new-api/apigx/relayhook/capacity_hook.go`、分享渠道前端与相关测试 | 渠道内多 Key 故障切换 | 继续复用 New API 的加权轮询/随机、逐 Key 健康、错误判定与跨渠道重试；为单次请求增加失败 Key 排除集合，429/401/可重试临时错误先换当前渠道内未尝试 Key，耗尽后才进入授权范围内的跨渠道路由；渠道内切换不消耗全局重试预算，单池最多尝试 10 把 Key；异步只在提交阶段切换，成功提交后的查询继续使用凭据快照；失败尝试按真实上游状态结束容量租约 | 用户要求轮 Key 与底座多渠道故障切换一致，无需为每把 Key 建渠道或让用户创建多个渠道分组；原逻辑只在选路前选择一次 Key，429 后跨渠道重试，且重试前容量账本会把 Writer 默认 200 误记为成功 | **中高**：触及同步/异步重试热路径与容量尝试账本；已通过真实 HTTP 替身 429→换 Key→换授权渠道、401 在零全局重试下换 Key、400 不换 Key，尝试账本保存真实 429 并在重试前释放旧租约，Go 全仓、三库、前端全量与 168 补丁重建。仍需生产多实例和真实第三方故障压测 | 2026-09-20 | 已实施；正常流量继续使用底座原生 Key 策略，APIGX 只增加请求级排除与同渠道优先接线 |
| D-044 | `new-api/service/{relay_error.go,relay_error_test.go}`、`new-api/controller/{relay.go,relay_multi_key_failover_test.go}` | 流式响应后的重试边界 | 在底座跨渠道与 APIGX 渠道内重试判定前统一检查 Gin Writer；响应已写出后停止所有 HTTP 重试，顶层只记录错误，不再把 JSON 错误体追加到已开始的 SSE/普通响应；WebSocket 继续走原错误帧 | 锁定基线的重试策略没有响应提交检查；Claude/Gemini 等适配器可能在已输出部分流后返回错误，继续换 Key/渠道会把两个上游响应拼入同一客户端流 | **中**：修改公共错误判定与顶层呈现；已用真实控制器链路验证 Anthropic SSE 输出部分内容后坏帧只访问第一把 Key且无 JSON 拼接，原 429/401/400 行为、Go 全仓和三库矩阵通过；仍需真实第三方流中断压测 | 2026-09-20 | 已实施；未写出响应时继续完整复用原错误策略，写出后严格停止重试 |
| D-045 | `new-api/apigx/{api/share.go,model/repo_price.go,price/match.go}`、`new-api/web/src/features/share/**`、七种语言文件 | 分享模型人工定价与批量送审 | 动态计价表达式或未知价格保留未知状态并给分享者人工录入入口；服务端按本人 Offering 反查作用域，保存 `manual + locked + accepted` USD 不可变快照，以状态和修订号条件更新防止送审竞态；渠道工作台显示定价状态，支持单项及批量提交已定价草稿/驳回模型，并返回成功、跳过和失败明细 | G4 分享者流程断点：动态表达式提示“手动输入”却没有入口，模型抓取后也不能从渠道工作台一键提交审核 | **低到中**：新增 APIGX owner-scoped API、价格快照和分享工作台交互；已通过权限/事务/并发回滚、SQLite/PostgreSQL/MySQL、前端 1775 项、Go 全仓、生产构建及桌面/移动真实浏览器流程 | 2026-09-20 | 已实施；明确零价与未知价继续分离，待审核/已发布状态禁止改价 |
| D-046 | `new-api/{common/constants.go,main.go,model/frontend_option_migration.go,web/**,electron/**,README*,NOTICE}` | APIGX 产品主页、模型目录文案与品牌边界 | 默认主页围绕模型广场、统一平台 Key、质量与容量路由、渠道内多 Key 故障切换、六类 AI API 和免费共享记账重做；管理员模型身份确认改用业务语言；默认系统名、HTML、布局、About、README 与桌面应用统一为 APIGX；仅把数据库中精确等于 `New API` 的旧默认值迁移为 APIGX，自定义名称不变；上游链接、AGPLv3、原作者署名、Go 模块路径和真实 New API 渠道类型继续保留 | 用户明确要求开源产品使用 APIGX 品牌；旧页面仍以 New API 通用网关和内部数据结构为中心，用户无法从首页理解共享资源工作流 | **中**：改动默认产品身份、首页与多语言文案，并增加跨三库默认值迁移；已通过前端全量 1778 项、定向 5 项、typecheck、变更文件 lint/format、生产构建，Go common/model/controller、SQLite/PostgreSQL/MySQL 品牌迁移矩阵，以及 1440/390 浅色/深色、真实广场/About 与 console error 0 浏览器验收 | 2026-09-20 | 已实施；APIGX 为 fork 产品身份，New API 仅在上游来源、许可署名、模块路径和真实渠道语义中出现 |
| D-047 | `new-api/web/src/i18n/locales/*.json` | 数据看板共享资源用量本地化 | 为数据看板区块、页签和共享用量卡片共同使用的 `Shared resource usage` 翻译键补齐七种语言；简体中文显示“共享资源用量” | G5 浏览器回扫发现该英文键在中文界面直接回显，影响数据看板的一致性 | **低**：只补已有翻译键，不改变数据或路由；七语言 JSON、定向 i18n 3 项、格式检查、生产构建及真实浏览器页面均通过 | 2026-09-21 | 已实施 |
| D-005 | `new-api/service/billing.go` | 修改 | 新增常量 `BillingSourceUsageOnly = "usage_only"` | T1.6：免费共享模式需要独立的资金来源标识 | 低（仅新增常量） | 2026-09-16 | 已实施 |
| D-006 | `new-api/service/billing_session.go` | 必须修改 | ① `NewBillingSession` 最前面加分支：`IsUsageOnlyMode()` 时走 `newUsageOnlySession`（跳过 tryWallet 的余额前置）；② `reserveFunding` 的类型 switch 增加 `case *UsageOnlyFunding` 返回 nil | T1.6；计划书 §6「免费模式必须改结算路径而非数值」 | **中高**：计费会话是全站核心链路。已加 4 个单测 + 运行时 A/B 验收（默认行为零回归） | 2026-09-16 | 已实施 |
| D-009 | `new-api/middleware/distributor.go` | 必须修改 | 在 `SetupContextForSelectedChannel` 之后、`c.Next()` 之前插入容量钩子：`relayhook.Acquire` 取租约、`defer lease.Release()` 在下游全部结束后释放；满载 429、账本不可用 503。另加 1 行 import | T1.7 第二部分；计划书 §9.2（共享容量控制必须贯穿请求生命周期） | **中高**：`Distribute()` 是所有转发请求的必经入口（含任务原渠道 pin）。改动本身是「新增一个可开关的调用」，默认关闭时行为不变 | 2026-09-16 | 已实施（默认关闭，`APIGX_CAPACITY_ENABLED`） |
| D-012 | `new-api/router/api-router.go` | 接线 | 新增 4 行：APIGX 独立路由组 + `UserAuth` + `apigxroute.Register`；另加 1 行 import | T1.4：分享者接口必须独立于 `/channel` 那组管理员路由（渠道是全局资源、那组挂 AdminAuth） | 低（新增一组路由，不动既有路由） | 2026-09-16 | 已实施 |
| D-027 | `controller/channel.go`（底座渠道更新路径） | 缺陷（待确认） | 通过 `PUT /api/channel/` 修改渠道的 **`base_url` 不生效**：接口返回 200，但库中值未变 | 阶段 3 真实厂商联调时发现：把 base_url 从 `https://aiping.cn/api/v1` 改为 `https://aiping.cn/api` 后，**接口 200 但库未变** ⇒ 平台请求变成 `…/api/v1` + 适配器 `/v1/embeddings` = **双 `/v1`** → 上游 404。改为直接更新库并等同步周期后即通 | 中（影响运维改上游地址；**尚未定位是底座有意忽略还是解析问题**，未改代码） | 2026-09-17 | **已记录，未修复**（需确认底座意图再动） |
| D-026 | `relay/helper/valid_request.go` | 缺陷修复（规格约束） | 音频上传补**大小上限 25 MiB**（常量 `MaxAudioFileSizeBytes`，校验就在既有"必须有 file"判定旁） | 阶段 3 核对计划书"上传大小/时长限制"时实测发现：转写上传**没有任何大小上限** —— 超大文件会先吃掉内存与磁盘 | 低（只新增拒绝路径；未超限请求行为不变） | 2026-09-17 | 已实施 |
| D-025 | `relay/helper/valid_request.go` + `relaykit/dto/{embedding,rerank}.go` | 缺陷修复（规格约束） | 为**计数类**字段补上限：embeddings 输入条数 **2048**、rerank 文档条数 **1000**（常量放在各自 DTO 旁，与既有 `MaxImageN = 128` 同风格） | 阶段 3 核对计划书"输入量限制 / 文档数量限制"时实测发现：两个校验器**只判空、没有上限** —— 一个超大输入数组会先吃掉内存与上游配额，属仓库自身规则（用户可控计数必须在校验器设界）要求补的洞 | 低（仅新增拒绝路径；未超限的请求行为不变；`relaykit` 已用 `GOWORK=off` 独立构建验证） | 2026-09-17 | 已实施 |
| D-024 | `new-api/relay/helper/valid_request.go` | 缺陷修复 | 音频转写/翻译请求在**校验点**判定"必须有 file"，缺文件返回**客户端错误（4xx）** | 阶段 3 实测：缺文件时错误一路落到适配器的"打开文件失败"处，最终报 **500** —— 调用方会误判为平台故障并重试，而实际是它自己的请求不合法 | 低（只把错误分类提前到校验点；合法请求行为不变） | 2026-09-17 | 已实施 |
| D-023 | `new-api/relay/audio_handler.go` | 可观测性 | 音频中继成功但响应**未报告任何用量**时，写一条**明确告警**（不改变任何计费行为） | 阶段 3 测试中一度以为"上游不带 usage ⇒ 完全不计费"，**后经复跑更正**：用量记录**会写**（走既有 `PostTextConsumeQuota` 分支），只是**异步批量落库有延迟**（我读得太早，属**假阴性**）。本改动保留其价值：把"**本次响应未报告任何用量**"记进日志——这仍是运维/审计想知道的事实，也提示"该次计费依据不足"。定价策略（拒绝/兜底价并标注/允许但标记）仍属产品决策 | 低（只加日志；额度计算路径未改） | 2026-09-17 | 已实施 |
| D-022 | `new-api/plugins/tasks/kling/plugin.js` | 缺陷修复 | 轮询解析钩子 `parseTaskResult` 的状态映射**方向反了**：原来 `{submitted:"SUBMITTED", processing:"IN_PROGRESS", ...}` 把**宿主/厂商词表当键去查上游值**，于是上游返回 `IN_PROGRESS` 一律查不到 ⇒ `UNKNOWN` ⇒ 宿主报 `unknown task status` ⇒ **kling 视频任务永远停在 queued**。改为**输出厂商词表**（`NOT_START/SUBMITTED/QUEUED/IN_PROGRESS/SUCCESS/FAILURE`——宿主 `model.TaskStatus*` 与 DB 用的就是这套），并接受 `processing/succeed/failed` 等同义写法。**注**：`native.taskStatus`（对客户端渲染用）**未改**——它原本的映射方向是对的 | 阶段 3 验收实测：宿主只认 `submitted/processing/succeed/failed`，而该分支把上游原始状态原样返回 ⇒ 宿主每次都判 `unknown task status`，**kling 视频任务永远停在 queued**（日志成片的 `poll unrecognized ... unknown task status: IN_PROGRESS/SUCCESS/FAILURE`）。**对真实 Kling 同样成立**（它返回的正是这套词） | 低（只改状态词的映射；`task_result` 等仍透传） | 2026-09-17 | 已实施 |
| D-021 | `new-api/relay/helper/valid_request.go` | 校验加固 | 在 `GetAndValidOpenAIImageRequest` 里新增**尺寸语法与范围**校验（`宽x高`，各边 1..4096），一条路径同时覆盖 JSON 与 multipart | 阶段 3 用真实上游（`qwen-image-2.0`）发现：**上游对 `size=999x999` 会默默接受并出图**，于是"不支持的尺寸"要到出图后才发现——那时已经计费。数量 `n` 本来就在这一处校验（`dto.MaxImageN`），同类约束放在一起避免散落 | 低（仅收紧非法输入；空串仍放行=用上游默认；不硬编码任何上游的支持档位） | 2026-09-17 | 已实施 |
| D-020 | `new-api/dto/channel_constraints.go` + `model/channel_constraint.go` + `middleware/distributor.go` | 新增过滤器类型 + 接线 | ① 新增 `FilterAllowedChannelIds`（"只保留列出的渠道 ID"，空列表=不允许任何渠道）；② 加入 `filterEvalOrder` 并在 `channelMatchesFilter` 里按 `ch.Id` 判定；③ 在 `Distribute` 里（选择之前）把 APIGX 按平台 Key 策略算出的**合格渠道集合**加成请求级过滤器 | T2.7 用户侧：计划书要求"按调用者所选策略排序并只选符合请求要求的渠道"。底座已有**请求级候选过滤器**机制（`GetChannelConstraints` → 选择时应用），复用它即可，**不必改写选择器** | 低（新增一种过滤器；未加该过滤器时行为完全不变） | 2026-09-17 | 已实施 |
| D-019 | `new-api/service/text_quota.go` | 接线 | 在消费日志前加 1 行 `relayhook.AdjustReservedTPM(ctx, prompt+completion)` | T2.5：TPM 预留用的是**可执行的输出上限**，必须按实际 usage 退回差额，否则渠道每分钟额度被"最坏情况"占满。此处是唯一"gin 上下文 + 最终 usage"都齐的位置 | 低（默认关闭：`APIGX_TPM_ADJUST_ENABLED`；开启后只做一次窗口计数调整） | 2026-09-17 | 已实施 |
| D-018 | `new-api/relay/request_billing.go` | 接线 | 在 `PrepareRequestBilling` 里（`ModelPriceHelper` 之后、`FreeModel` 短路之前）加 6 行：`relayhook.CheckRequestTPM(c, channelId, tokens, meta.MaxTokens)`，失败则直接返回错误 | T1.7 TPM 维度：预留量必须用**可执行的输出上限**，而该上限（`TokenCountMeta.MaxTokens`）只有请求解析后才有；此位置还在 `FreeModel` 短路之前，保证免费模型同样受约束 | **中**：这是每个转发请求的计费入口。改动为「新增一个默认关闭的校验」（未配置 TPM 时不产生任何约束与查询） | 2026-09-17 | 已实施（`APIGX_CHANNEL_TPM_LIMIT` / 渠道策略，未配置即不约束） |
| D-017 | `new-api/controller/channel.go` | 新增导出包装 | 加 3 行 `FetchChannelUpstreamModelIDsForAPIGX(channel)`，转调未导出的 `fetchChannelUpstreamModelIDs` | T1.8 一键获取：真正干活的函数会按渠道类型分派到 40+ 家 provider 适配器；没有导出入口就只能重写一遍（违反 CODE_STYLE 第 25 条） | 低（纯新增导出函数，不改既有逻辑） | 2026-09-17 | 已实施 |
| D-016 | `new-api/middleware/distributor.go` | 必须修改 | 在 `SetupContextForSelectedChannel` 设置完 Key 索引之后加 3 行：`relayhook.AcquireForAttempt(c, channel.Id)`，失败则返回错误；`Distribute` 尾部改为只 `defer relayhook.ReleaseFromContext(c)` | T1.7：底座的失败重试会**重新选渠道**并再次经过本函数，容量租约必须**按每次尝试**获取，否则重试换渠道后仍占用旧渠道的租约、新渠道完全没做容量检查 | **中**：该函数是选路后的必经点（初次与每次重试）。行为仍由默认关闭的开关控制 | 2026-09-17 | 已实施 |
| D-015 | `new-api/middleware/distributor.go` | 接线 | 在 `c.Next()` 之后、响应状态 < 400 的分支里（底座自己在此记录渠道亲和性）加 1 行：`channelpool.RecordSuccessFromContext(c, channel.Id)` | T1.5：成功路径的逐 Key 统计（否则 `LastOKAt` 永远为 0，分享者看不出哪把 Key 可用） | 低：复用底座**已有的成功点**，不新增钩子位置；默认关闭 | 2026-09-16 | 已实施（`APIGX_KEY_STATS_ENABLED`，默认关闭） |
| D-014 | `new-api/service/relay_error.go` | 接线 | 在 `ProcessChannelError` 的自动禁用块之后加 2 行：调用 `channelpool.RecordFailureFromContext(c, channelId, statusCode, errorCode)`；另加 1 行 import | T1.5：逐 Key 统计与 Key 级故障隔离需要请求失败时的结果 | **中**：`ProcessChannelError` 是所有渠道失败必经之路。改动是「新增一个默认关闭的调用」，关闭时不产生任何查询 | 2026-09-16 | 已实施（`APIGX_KEY_STATS_ENABLED`，默认关闭） |
| D-013 | `new-api/apigx/{secretbox,api,route}/**` | 新增 | 上游凭据加密封装（AES-256-GCM 信封、失败关闭）、分享者接口、路由入口 | T1.4/T1.5；计划书 §10.3（上游 Key 加密存储、主密钥与库分离） | 无（上游不存在同名路径） | 2026-09-16 | 已实施 |
| D-011 | `new-api/apigx/model/{repo_share,repo_upstream,repo_test}.go` | 新增 | 领域仓储层：分享分组/渠道/上游账户池/上游 Key 的 CRUD 与不变量（软删除墓碑、**服务端强制归属**、Key 池筛选、参数校验）；8 个测试 | T1.4/T1.5/T1.8 的共同地基；计划书 §9.2、§10.3 | 无（上游不存在同名路径） | 2026-09-16 | 已实施 |
| D-010 | `new-api/apigx/relayhook/{capacity_hook,capacity_hook_test}.go` | 新增 | 容量接线：`Acquire`/`Lease.Release`/长流续租协程/错误码映射（429+Retry-After、503）；为渠道+平台 Key+用户三维度做同一次原子预留 | T1.7 | 低（新包；不反向依赖 middleware，避免循环） | 2026-09-16 | 已实施 |
| D-008 | `new-api/apigx/capacity/{capacity,capacity_test}.go` | 新增 | 共享容量账本：Lua 原子预留/幂等归还/租约续租与过期回收；维度 key 覆盖用户·平台 Key·渠道·供给·上游 Key·上游账户池 | T1.7；计划书 §9.2（底座没有渠道/Key 级并发上限，阶段 0 已实测确认） | 无（上游不存在同名路径） | 2026-09-16 | 已实施（核心账本） |
| D-007 | `new-api/service/apigx_usage_only.go`、`apigx_usage_only_test.go` | 新增（同包） | 模式开关（`APIGX_USAGE_ONLY`）、`UsageOnlyFunding`、`newUsageOnlySession` 及其单测 | T1.6 | 低（新文件；与底座 `funding_source.go` 同包，便于实现 FundingSource 接口而不产生循环依赖） | 2026-09-16 | 已实施 |
| — | 补丁导出 | — | `deploy/patches/0001-*` 至 `0172-fix-localize-shared-resource-usage.patch`；fork 分支 `apigx`，HEAD `bf36a3a` | 让工作区仓库可追溯 fork 状态，且能从固定提交复现 | — | 2026-09-21 | 172 个补丁从基线重放成功，tree `e3e041bdca20f3230ef7897e87a25feff8e7a4bd` 与 fork HEAD 一致 |

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
| `new-api/apigx/price/` | New API 本地价格匹配与渠道/供给不可变价格版本 | 阶段 2 / 接手整改阶段 D | 2026-09-16 |
| `new-api/apigx/startup/` | 共享模式启动配置与依赖一致性检查，缺失关键保护时失败关闭 | 接手整改阶段 A | 2026-09-18 |
| `new-api/apigx/model/` | 新增 GORM 实体（三库兼容） | T0.4 | 2026-09-16 |
| `new-api/web/src/features/apigx-*/` | 前端新功能模块，复用既有组件与 i18n | 阶段 1 | 2026-09-16 |

## 三、允许直接沿用、不改动的上游能力

位置已由 T0.3 精确定位（依据 `docs/stage0/reuse-modify.md`）：

| 能力 | 上游位置（文件:行） | 我们的用法 |
|---|---|---|
| 厂商适配器与协议转换 | `relay/`、`relaykit/`（独立模块，`go.mod` 有 `replace ... => ./relaykit`） | 直接复用，逐端点核验；保持 `relaykit` 可独立构建 |
| 模型目录发现 | `controller/channel.go:1411 FetchModels`、`controller/channel_upstream_update.go:364/467/513/858/914` | 复用发现与「新增/变更/消失」差异流程，外层加 owner 约束 |
| 价格配置 | `setting/ratio_setting/**`、`setting/billing_setting/**` | APIGX 只读取已生效的本地按次价、倍率价与计价表达式；底座管理员可继续使用原生同步界面维护这些值 |
| 计费预留/结算框架 | `service/billing_session.go:27/43/84/128/154/196/252/306/377` | 复用框架，新增 `usage_only` 资金来源 |
| 用户级模型限流 | `middleware/model-rate-limit.go:169`、`setting/rate_limit.go:62` | 复用（注意：它不是渠道并发/TPM 上限） |
| 性能统计采集 | `model/perf_metric.go:11-23/25/29/51` | 复用采集，扩展供给与 Key 策略版本维度 |
| 多 Key 启停与轮询基础 | `model/channel.go:182/206/673/725/738` | 复用选择逻辑，补策略、容量与逐 Key 统计 |
| 任务归属校验 | `controller/task.go:233 getTaskForArtifactRequest` | 复用，另在产物访问上收紧默认策略 |
| 账号会话与令牌校验 | `middleware/auth.go` 主体 | 复用，不另起第二套用户系统 |
| 管理后台组件 | `web/src/components/` | 前端复用，不另起样式体系 |

## D-028：sora 任务插件声明了本项目实际使用的视频模型（MiniMax-Hailuo-02）

**性质**：**产品需要的改动**（不是底座缺陷）。

**背景**：本项目视频能力走 aiping.cn，其视频接口是 OpenAI 风格 `POST /v1/videos` ——
与底座内置 `sora` 任务插件协议**完全一致**（`{baseUrl}/v1/videos` + `GET /v1/videos/{id}` + `/content`）。
但底座按**模型名**把请求认领给插件：`MiniMax-Hailuo-02` 被内置 `hailuo` 插件（MiniMax 原生协议）认领，
于是我们的 `sora` 渠道拿不到请求（503 "claimed by task plugin hailuo"）。

**改动**：`plugins/tasks/sora/plugin.js` 的 `models` 增加 `"MiniMax-Hailuo-02"`，
让底座把该模型认领给 `sora` 插件；协议/路由不变（原本就兼容）。

**配套的环境级操作（不写进代码，属部署配置）**：停用内置 `hailuo` 插件以避免认领冲突 ——
用底座自带机制 `PUT /api/plugin/task` 上传 hailuo 自身源码 + `enabled:false`（**可逆**）。

**验证**：经平台 `POST /v1/videos {model:"MiniMax-Hailuo-02"}` ⇒ **200**，任务
`task_cgApa5Xj4kAnFIrZzdnBWOqayhV5eo09` ⇒ 轮询 **200 / queued**（真实厂商任务，只提交一次）。
