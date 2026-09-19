# 项目状态 · apigx（二开 New API）

> 本文件由开发流程维护（`whisper-note-dev-flow/SKILL.md` §4）。**序号倒序，顶部最新**。
> ⚠️ 说明：本项目在**本次会话之前**的进展未按本流程记录，因此编号从 001 开始；更早的详细过程见 `TASK_LOG.md`。

## 当前阶段
**阶段 C 多 Key 与异步任务闭环已完成，进入阶段 D 价格与统一计量闭环。**

每把真实 Key 现在只占一个槽位，权重、健康、容量、熔断和统计都锚定稳定 Key ID。异步任务固化实际 Key 与凭据版本，轮换/删除后仍按历史凭据查询；状态、结算和租约只推进/释放一次，快照写失败有持久修复事件，进程中断后能恢复续租。项目仍需完成阶段 D、按三角色任务流重做前端和生产级验证，当前**尚不可上线**。

## 当前任务
[已完成] 接手整改阶段 C：多 Key 与异步任务闭环。
- 原生 3:1 加权选择为 12:4；单 Key 故障、容量和熔断互不覆盖，状态跟随稳定 Key ID。
- 异步任务快照、历史凭据、终态 CAS、幂等退款/释放、超时对账、持久修复事件和重启恢复已接通。
- 三库、全仓、`relaykit`、真实 PostgreSQL+Redis HTTP、真实进程中断恢复和最终二进制均通过；第 162 个补丁可从锁定基线完整重建。
- 下一开发批次：阶段 D 模型价格一键匹配与六类能力统一计量。

### [029] 2026-09-19 接手整改阶段 C：多 Key 与异步任务闭环

**做了什么**：移除复制明文槽位的多 Key 加权方式，让运行状态按稳定 Key ID 和独立来源合并；为异步任务补齐凭据版本历史、不可变快照、历史凭据轮询、状态 CAS、持久修复事件、任务租约移交/恢复/幂等释放和明确的取消能力契约。

**涉及模块**：多 Key 选择与投影、Key 凭据生命周期、异步任务提交/轮询/取消、容量租约与对账、三库迁移、HTTP 验收与补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/model/channel.go`、`apigx/channelpool/**` | 多 Key | 每把真实 Key 单槽位投影；稳定 ID 与原生权重选择；底座健康、容量、熔断按来源独立保存并生成兼容镜像 |
| `new-api/apigx/model/{models_identity,models_metering,repo_upstream,repo_async_task}.go` | 凭据/任务模型 | 新增凭据版本历史；不可变任务快照；状态单向推进与 CAS；租约释放状态；无密钥持久修复事件 |
| `new-api/apigx/relayhook/{async_task,capacity_hook}.go` | 任务生命周期 | 提交后移交请求租约；轮询使用历史凭据；终态幂等释放；超时和进程重启 reconciliation；快照失败后补建 |
| `new-api/service/task_polling.go`、`relay/relay_task.go`、`controller/task_cancel.go` | 底座复用 | 批量任务按凭据版本分组查询；实时查询使用历史凭据；取消仅在适配器确认并赢得底座 CAS 后结算与释放 |
| `tools/stage{1,2,3}-verify/**` | 验收 | 修复夹具失败检查；覆盖原生权重、单 Key 隔离、正常/超时任务、取消限制和 Key 轮换/删除后的历史查询 |
| `deploy/patches/0162-*`、`deploy/base.lock.json`、`docs/{TAKEOVER_AUDIT,stage0/upstream-diff}.md` | 交付 | 更新 fork HEAD、差异统计、阶段 C 台账与可重建补丁链 |

**验证**：受影响 Go 包与新增 CAS 未命中用例 ✅；Windows HTTP/2 GOAWAY 专项 20 次 ✅；`go test ./... -count=1 -timeout=10m` ✅；`relaykit` 在 `GOWORK=off` 下独立测试 ✅；SQLite 3.50.4 / PostgreSQL 18.4 / MySQL 8.0.12 阶段 C 行为矩阵 ✅；原生 Key 权重 16/16 且请求分布 12:4、单 Key 隔离 9/9、正常异步任务 15/15、1 分钟超时对账 9/9、取消稳定返回 501、Key v1/v2 在轮换并删除活动 Key 后均完成且替身确认分别使用历史凭据 ✅；真实杀掉网关后新 PID 接管存量租约，任务保持运行、续租后完成，数据库 `reservation_release_state=2` 且 Redis 租约清零 ✅；最终二进制 `/api/status` 200 ✅；162 个补丁从锁定基线重建后的 tree `65876bdc7f1eceb528d47ddc7d4dbcf2d45493c5` 与 fork HEAD 完全一致；inner commit `338405ddb61ff84a6ff1dac959ddafafe1d68845`。

**剩余风险**：本地闭环使用 PostgreSQL、Redis 与可控替身，不是生产多实例、持续压测或真实第三方上游证明；当前内置/Kling 视频适配器没有上游取消实现，端点明确返回 501；价格版本与六类能力统一计量仍属阶段 D；既有多个渠道的 `model_mapping` 含非法 `\\:` 转义，启动日志仍会复现。

### [028] 2026-09-19 接手整改阶段 B4：默认 `auto` 质量路由

**做了什么**：让平台 Key 的空值/`auto` 默认策略真正使用 6 小时可靠性、延迟、文本吞吐与实时容量综合选路；候选计算失败关闭，容量变化立即参与下一次决策，质量样本保持可归因且幂等。

**涉及模块**：平台 Key 策略、候选装配、质量采集与汇总、容量策略缓存、三库矩阵、前端文案、HTTP 验收与补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/relayhook/route_strategy.go`、`middleware/distributor.go` | 请求策略 | 空值/auto 接入 `StrategyAuto`；无候选返回空 allow-list；移除含实时容量的最终优胜集合缓存 |
| `new-api/apigx/model/repo_quality.go`、`qualityhook/quality.go`、`routing/assemble.go` | 质量 | 只用自然流量的合格上游尝试计算质量；文本 token/生成时长成对汇总并写入候选吞吐 |
| `new-api/service/{billing_session,text_quota}.go`、`relay/common/relay_info.go` | 结算采集 | 最终 completion token 进入质量样本；同一结算会话重试只落一次质量样本 |
| `new-api/apigx/api/share.go`、`channelpool/policy_source.go` | 容量一致性 | 容量策略创建/更新后立即清策略缓存，保证候选判断与最终原子预留使用同一上限 |
| `new-api/web/src/features/keys/**`、`i18n/locales/*.json` | 平台 Key 前端 | 将 auto 文案改为“综合自动（质量与容量）”并同步七种语言 |
| `tools/stage2-verify/route-strategy.mjs` | HTTP 验收 | 失败关闭地创建完整发布夹具，覆盖五种策略、未知值、满载切换与释放恢复 |
| `deploy/patches/0161-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 交付 | 更新 fork HEAD、差异统计、B4 台账与可重建补丁链 |

**验证**：受影响 Go 包 ✅；SQLite 3.50.4 / PostgreSQL 18.4 / MySQL 8.0.12 质量桶与 6 小时视图矩阵 ✅；`go test ./... -count=1 -timeout=10m` ✅；前端 typecheck、平台 Key 用例 6/6、生产构建 ✅；后端最终二进制构建 ✅；真实 PostgreSQL+Redis 网关 HTTP：空值/auto/reliability/latency/throughput 均 A=5/B=0，A 满载时 A=1/B=1，释放后 A=1/B=0，未知策略 HTTP 400 ✅；161 个补丁从锁定基线重建后的 tree `35eb05fa148842d9ce49156b39cfdd08647b525f` 与 fork HEAD 完全一致；inner commit `5cc4a4d3ef51f3edc9e8fc886602e8e4909e53ed`。

**剩余风险**：本地网关闭环使用 PostgreSQL、Redis 与本地替身，不是生产多实例、持续压测或真实第三方上游证明；非文本能力目前缺失吞吐指标时会按可靠性、延迟与容量重归一，图片/语音/视频处理速度指标仍待后续能力化；多 Key 轮换后的查询与异步任务凭据/价格快照、终态收尾仍属阶段 C/D；既有多个渠道的 `model_mapping` 含非法 `\\:` 转义，启动日志仍会复现。

### [027] 2026-09-19 接手整改阶段 B3：渠道原子生命周期

**做了什么**：将自助分享渠道创建、供给发布投影与删除/停用收口为失败关闭的原子生命周期，禁止普通用户绑定全局底座渠道，并保证重复删除可继续收尾。

**涉及模块**：分享接口、渠道仓储、底座能力投影、三库生命周期矩阵、API 回归、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/api/share.go` | 生命周期编排 | 普通用户只能提交自助配方；底座/APIGX 渠道/初始 Key 同事务创建；发布状态/审计/模型/能力同事务；删除按失败关闭顺序执行 |
| `new-api/apigx/model/repo_share.go` | 渠道仓储 | 支持事务内创建及幂等软删除收尾 |
| `new-api/apigx/channelpool/projection.go` | 底座投影 | 停用底座与能力、刷新底座状态和 APIGX 路由策略缓存、清空 Key/模型/多 Key/能力投影 |
| `new-api/apigx/{api/share_test.go,channelpool/projection_test.go}` | 回归 | 初始 Key 与发布投影故障回滚、权限拒绝、删除清理和重复删除 |
| `deploy/patches/0160-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 交付 | 更新 fork HEAD、差异统计、B3 台账与可重建补丁链 |

**验证**：SQLite 3.50.4 / PostgreSQL 18.4 / MySQL 8.0.12 生命周期矩阵 ✅；`apigx/api`、`apigx/channelpool`、`apigx/model` ✅；`go test ./... -count=1 -timeout=10m` ✅；真实 PostgreSQL+Redis 网关 HTTP 闭环 15/15 ✅；删除后 PostgreSQL 六项状态直查通过 ✅；160 个补丁从锁定基线重建后的 tree `9dc37086021a5e534dffc581e96d72d6cd729bb3` 与 fork HEAD 完全一致；inner commit `b4a78236ebfe902f90f0ac89208eea01219cbcc7`。

**剩余风险**：本地网关闭环使用 PostgreSQL、Redis 与本地替身，不是生产多实例或真实第三方上游证明；默认 `auto` 质量路由仍未闭合，当前不可上线；删除后的既有异步任务采用失败关闭，凭据快照、轮换后查询和终态收尾仍属阶段 C/P1-2；既有多个渠道的 `model_mapping` 含非法 `\\:` 转义，启动日志仍会复现。

### [026] 2026-09-19 接手整改阶段 B2：跨 owner 审核

**做了什么**：把 Offering 发布/驳回从错误的 owner-scoped 写操作改成 reviewer-scoped 条件状态转换，增加管理员待审核队列，并将审核结果与脱敏不可变审计事件放进同一事务。

**涉及模块**：审核仓储、分享接口、三库行为矩阵、API 回归、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/repo_offering.go` | 审核仓储 | 跨 owner 队列、条件更新、审核结果投影与事务内 AuditEvent；理由只接受 64 字符内安全码 |
| `new-api/apigx/api/share.go` | 审核接口 | `GET /api/apigx/reviews/offerings`；管理员跨 owner 发布/驳回；请求 ID、IP 哈希和 UA 脱敏上下文 |
| `new-api/apigx/{api/share_test.go,model/repo_test.go,model/migrate_matrix_test.go}` | 回归 | 普通用户拒绝、管理员跨 owner、重复审核、审计唯一性和 SQLite/MySQL/PostgreSQL 行为 |
| `deploy/patches/0159-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 交付 | 更新 fork HEAD、差异统计、B2 台账与可重建补丁链 |

**验证**：真实 PostgreSQL+Redis 网关 HTTP 闭环 13/13 ✅；PostgreSQL 中发布/驳回各只有一条审计事件，状态分别为 published/rejected ✅；`apigx/model`、`apigx/api` ✅；SQLite 3.50.4 / PostgreSQL 18.4 / MySQL 8.0.12 真实方言矩阵 ✅；`go test ./... -count=1 -timeout=10m` ✅；159 个补丁从锁定基线顺序应用后的 tree `21c6a53730aa97cbe23d3060867188898db07334` 与 fork HEAD 完全一致。

**剩余风险**：本地 HTTP 闭环不是生产多实例证明；默认 `auto` 质量路由与 B3 渠道创建补偿、删除/停用生命周期仍未完成，当前不可上线；既有多个渠道的 `model_mapping` 含非法 `\\:` 转义，已在启动日志复现，需单独修复或清理数据。

### [025] 2026-09-19 接手整改阶段 B1：统一出站 ACL

**做了什么**：将分享资源 owner、公开可见性、有效分组/Offering/平台 Key Grant、资源存活与发布状态、平台 Key 模型/分享者/固定 Offering 范围收口为统一授权决策；在候选、策略评分前过滤，并在固定来源、重试、异步任务、Midjourney、Responses WebSocket 和历史任务上游代理出站前复验。

**涉及模块**：访问仓储、来源守卫、路由候选、底座分发、异步任务、WebSocket、历史产物代理、验收脚本、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/repo_access.go`、`repo_plaza.go` | 授权 | 统一资源快照；排除已撤销/到期 Grant，只纳入存活且已发布的资源；保留历史受管理渠道集合 |
| `new-api/apigx/relayhook/{source_guard,route_strategy}.go`、`routing/assemble.go` | 路由 | ACL 与平台 Key 范围求交；策略缓存按 user/token 隔离；授权过滤先于质量和容量装配 |
| `new-api/middleware/distributor.go`、`dto/channel_constraints.go`、`model/channel_constraint.go` | 分发 | 增加请求级 deny/allow 过滤，空允许集合直接统一拒绝；最终守卫先于 Key 选择和容量预留 |
| `new-api/relay/{relay_task,mjproxy_handler,responses_websocket}.go`、`controller/{task,video_proxy}.go` | 旁路 | 原渠道、任务重试、MJ、每次 WebSocket 请求、上游任务/产物代理均实时复验；本地产物仍允许任务所有者读取 |
| `tools/stage1-verify/{source-allowlist,rejection-matrix}.mjs` | 验收 | 创建完整已发布 Offering，验证 Grant/平台 Key 求交、撤销、回退、容量和拒绝脱敏 |
| `deploy/patches/0158-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 交付 | 更新 fork HEAD、差异统计、B1 台账与可重建补丁链 |

**验证**：ACL/策略直接影响包提交后联合回归 ✅；`go test ./... -count=1 -timeout=10m` ✅；`relaykit` 在 `GOWORK=off` 下独立构建 ✅；视频路由 10 次、HTTP/2 GOAWAY 20 次 ✅；`source-allowlist.mjs` 5/5、`rejection-matrix.mjs` 10/10 ✅；158 个补丁从锁定基线顺序应用后的 tree `b19c708d79b97d392b2882a2d8f700e0c8ced1c6` 与 fork HEAD 完全一致。

**剩余风险**：本地网关闭环使用 PostgreSQL、Redis 与本地替身，不能作为真实第三方上游或生产多实例证明；B2 跨 owner 审核、B3 渠道生命周期和默认 `auto` 质量路由仍未完成，当前不可上线。

### [024] 2026-09-18 接手整改阶段 A：可信工程基线

**做了什么**：消除五个验收脚本假绿与 TPM 人工断言，修复 Windows 缓存键/SQLite 连接隔离、Redis 3 缓存写入兼容和 HTTP/2 测试脆弱断言，新增共享模式正式配置模板与启动失败关闭校验，并把内层提交固化为第 157 个补丁。

**涉及模块**：验收脚本、APIGX 启动配置、Redis 缓存、Windows 测试隔离、补丁交付与接手文档

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `tools/stage0-verify/acceptance.mjs`、`tools/stage2-verify/*.mjs` | 验收 | 断言失败退出 1、异常退出 2；TPM 自取本次用量和 Redis 窗口计数 |
| `.env.example`、`new-api/apigx/startup/**`、`new-api/main.go` | 配置/启动 | 增加共享模式配置基线及依赖一致性失败关闭 |
| `new-api/common/redis.go`、`new-api/model/*cache*.go`、`user_session.go` | Redis | 兼容 Redis 3 的多字段 Hash 写入 |
| `new-api/service/*affinity*_test.go`、`new-api/controller/*_test.go` | 测试 | 单调隔离键、WAL/busy timeout、捕获并关闭被全局覆盖的数据库句柄 |
| `new-api/relay/channel/api_request_getbody_test.go` | 测试 | 按可观察重试契约断言，移除平台相关错误文案依赖 |
| `deploy/patches/0157-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 交付 | 更新 fork HEAD、差异统计与补丁重建证据 |

**验证**：`go test ./... -count=1` ✅；`service`/`controller` 整包 ✅；SQLite 并发用例各 20 次 ✅；亲和缓存 50 次 ✅；HTTP/2 负例 100 次 ✅；`relaykit` 在 `GOWORK=off` 下独立构建 ✅；SQLite 3.50.4 / MySQL 8.0.12 / PostgreSQL 18.4 fresh+upgrade 矩阵 ✅；五个脚本受控失败均退出 1；TPM 开启/关闭分别为 Redis 46/8204；157 个补丁从锁定基线重建 tree 与 HEAD tree 一致。

**剩余风险**：四项业务 P0 尚未关闭，当前不可上线；本机 MySQL 默认 MyISAM 只能用于拒绝路径验证，APIGX 运行需 InnoDB；没有真实第三方上游、生产多实例、持续压测或正式安全测试结论。

### [023] 2026-09-18 前端使用逻辑二次审查与后端后置约束

**做了什么**：回扫现有分享、广场、平台 Key、邀请和管理页面，把用户新增的“后端完成后再按实际使用逻辑优化前端”写成正式实施阶段，并给出三角色端到端验收门槛。

**涉及模块**：`features/share`、`features/plaza`、`features/pricing`、`features/keys`、`features/access`、`features/apigx-platform`、接手计划

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `docs/TAKEOVER_AUDIT.md` | 接手计划 | 新增 P1-7；把阶段 E 改为分享者/使用者/管理员任务流驱动的前端重做，并拆出阶段 F 运维交付 |
| `whisper-note-dev-flow/TASK_LOG.md` | 流程 | 记录 UI 后置约束、源码回扫范围和浏览器验收门槛 |

**验证**：静态核对现有页面与 API 接线，确认 `/share` 平铺、渠道详情缺 Key CRUD、平台 Key/管理员表单依赖内部数字 ID、广场前 100 条客户端过滤、mutation 状态反馈不足等具体问题；本轮未修改或运行前端业务代码。

**剩余风险**：阶段 A-D 后端接口与状态机尚未修复；前端实现必须等待这些契约冻结后再开始，届时还需真实浏览器、桌面/移动端和三角色数据验收。

### [022] 2026-09-18 全仓接手审查与优化计划

**做了什么**：对外层交付仓、156 个 New API fork 提交、关键出站链路、验收脚本和三库证据做回扫；区分底座复用、确认缺陷、设计债与验证边界，形成按依赖排序的接手路线。

**涉及模块**：New API fork、APIGX 权限/路由/多 Key/价格/计量/任务、前端、验证体系、交付文档

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `docs/TAKEOVER_AUDIT.md` | 接手审查 | 新增审查结论、P0/P1/P2 清单、证据边界、阶段 A-E 路线与退出门槛 |
| `docs/stage0/upstream-diff.md` | 上游差异 | 将过时的“修改 0 个”修正为 186 个变化文件（新增 123、修改 63） |
| `docs/stage4/README.md` | 历史验收 | 标记旧“31/31/全部通过”已被接手审查取代，不得作为上线结论 |
| `whisper-note-dev-flow/TASK_LOG.md` | 流程 | 将接手审查 5/5 子任务闭环 |

**验证**：锁定基线顺序应用 156 个补丁后树与当前 fork 完全一致；`relaykit` 通过；前端 typecheck 与相关测试 23/23；APIGX Go 包和 `middleware` 通过；MySQL 8.0.12 上 APIGX 表迁移及幂等通过。全仓 Go 测试仍因可在原始 New API 基线复现的 Windows 测试隔离/SQLite 句柄问题失败或超时，已明确列为阶段 A，不写成绿色。

**剩余风险**：P0 全部未修复，当前不可上线；本机 MySQL 默认引擎仍为 MyISAM；没有真实第三方上游、生产多实例、持续压测或正式安全测试结论。

### [021] 2026-09-18 MySQL 三库矩阵补齐 + 升级回滚演练 + 收尾清单

**做了什么**：拿到 MySQL 凭据后补齐三库矩阵（**先失败后修复再通过**）；用旧版本二进制演练升级回滚；并把"还差什么"逐条落成文档。

**涉及模块**：数据库迁移（`apigx/model`）、文档（`docs/stage4/`）、流程文档

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/migrate.go` | 数据库迁移 | MySQL 方言下 `Set("gorm:table_options","ENGINE=InnoDB")` 后再 AutoMigrate ⇒ 我们的表**显式 InnoDB**（不依赖服务端默认值）；只影响 `apigx_*` 表 |
| `docs/stage4/acceptance-evidence.md` | 文档 | 新增 §22（三库矩阵）、§22.1（MySQL 先失败后修复）、§22.2（服务端 MyISAM 问题）、§23（升级回滚演练） |
| `docs/stage4/README.md` | 文档 | 新增「六、最终确认复跑结果」与「七、还差什么（收尾清单）」 |
| `tools/stage1-verify/migrate-pg-matrix.ps1` | 脚本 | 更正「本机没有 MySQL 实例」的过时说法（事实：有 8.0.12，但当时缺凭据） |
| `whisper-note-dev-flow/TASK_LOG.md` | 文档 | 追加三库矩阵、回滚演练记录；顶部新增「收尾清单」任务块 |

**验证**：
- 三库矩阵：`sqlite ✅ postgres ✅ mysql ✅`，**连跑两次通过**（幂等），`go test ./apigx/model/ -run TestMigrateMatrixOnConfiguredDialects`
- 升级回滚：旧二进制（`1c981ff`，落后 HEAD 59 个提交）在**新版已迁移的库副本**上启动成功、`/api/status` 200、登录 200，且回滚前后我方新表指纹逐项一致（`usage_events=81 async_tasks=10 channels=166 users=8`）
- 回归（更早一轮）：`apigx/model`、`apigx/api`、`apigx/relayhook`、`middleware`、`apigx/channelpool` 全 ok；前端 typecheck 0 错、share+i18n 13/13

**剩余风险**：
1. **MySQL 整机不可用**：该实例 `default-storage-engine=MyISAM` ⇒ 底座自身表建不出来（`Error 1071`）⇒ 需把 `my.ini` 改为 `InnoDB`（详见 README §七 A1）；
2. 宿主 `C:` 仅剩约 2.8 GB ⇒ 底座 503 守护会间歇触发；
3. 计划内未做：`perf_metrics` 看板页、外部告警通道、UI 那一轮；
4. 验收残留 C1–C8（详见 README §七）。

**流程合规说明**：本轮起补记 —— 前几轮（MySQL 修复、回滚演练）只提交了代码与台账，**漏了 PROJECT_STATUS 序号条目**，本条一并补齐；`codebase-memory-mcp` 在本会话**不可用**（工具集中没有该 MCP），按 skill §1 的允许项跳过并在此说明。
### [020] 2026-09-18 T4.5 + T4.6 收口：阶段 4 验收表逐条归档完成

**做了什么**：把计划书 §11 阶段 4 验收表（**L650–L680，实为 31 行**，含我拆开的多 Key/指定渠道等行）
**逐条定稿**：每行给出结论与出处；并在文件顶部新增「0. 结论总览」，写明状态计数、**未闭合项**与**阻塞条件归类**。

**涉及模块**：文档（`docs/stage4/`）、流程文档

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `docs/stage4/acceptance-evidence.md` | 文档 | 顶部新增「0. 结论总览」（**只统计计划书 31 行**，不再混入我自己的审计表）；未闭合项逐条列出并归类阻塞条件；正文 22 个小节保留全部过程记录 |
| `whisper-note-dev-flow/TASK_LOG.md` | 文档 | T4.5、T4.6 打勾并把交付物指向台账 |

**验证**：计数由脚本按区间取行后统计（不手写）——**31 行：✅28 / ⚠️3 / ❌0**；
T4.5/T4.6 均已打勾；台账引用的测试名此前已通过防伪校验
**剩余风险**：① **#21**（凭据轮换字段与任务路径消耗记录无写入方）；② **#22**（真实杀进程演练受宿主磁盘 503 阻断，且脚本需显式传网关环境）；
③ **#28**（跨日"回到原预留周期"缺预算预留的周期标识）；以上两项与 #19 的预留链路**同族**；
④ 计数口径修正说明：上一版把审计表混入统计，导致把"⚠️4"当成计划行数 ⇒ 已在总览中写明修正
**过程记录**：本轮自查发现我自己台账的**统计口径错误**（把 §8 的脱敏审计表当成计划验收行）⇒ 改为**按区间取行**统计并重写总览
### [019] 2026-09-18 T4.4 运营审核 + 指标看板与告警

**做了什么**：① 当场取运营审核链路的新鲜证据；② 指标看板**复用底座**（不另造）；③ 补上我们**唯一缺失**的一环 —— 熔断告警。

**涉及模块**：apigx/routing、docs/stage4

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `apigx/routing/alert.go`（新） | routing | `claimBreakerAlertSlot`（返回 bool 的限频闸门，间隔 600s）+ `resetBreakerAlertState`（仅测试用） |
| `apigx/routing/assemble.go` | routing | 熔断打开分支接入限频告警：`common.SysError("apigx alert: offering %s breaker OPEN …")` |
| `apigx/routing/decide_test.go` | 测试 | 新增 `TestBreakerAlertIsRateLimitedPerOffering`：断言限频不变量（非日志文本） |
| `docs/stage4/acceptance-evidence.md` | 文档 | 第 10 节：审核证据、指标盘点（perf_metrics 44 行）、告警实现与未做项 |

**验证**：审核 4 个用例当场 **PASS**；`perf_metrics` **44 行**（底座接口 `GET /api/perf-metrics/summary` 可用）；
告警测试 **PASS**、`apigx/routing` 整包 **ok**、`go build` ✅
**剩余风险**：① **可视化看板页面未做**（底座有数据与接口，我们没做运营汇总页）；② **外部告警通道未接入**
（邮件/Webhook/IM），当前告警只进服务日志，依赖日志采集与规则；③ 限频状态在**进程内存**中
（多实例下每实例各限一次，可接受但需知晓）
**过程记录**：我猜的测试文件名 `breaker_guard_test.go` 不在 `apigx/routing`（它属别的包）⇒ 脚本**校验失败并跳过写入**，
改到真实存在的 `decide_test.go` 后一次通过（**不猜文件名**这条纪律再次生效）
### [018] 2026-09-18 T4.3 备份恢复 / 迁移幂等 / 配置回滚（演练）

**做了什么**：对**本地真实环境**做备份恢复演练（**不动现场库**：恢复到新库校验），并在恢复库上验证迁移幂等，
另做配置改动→重启→回滚的闭环。

**涉及模块**：运维演练脚本、docs/stage4

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `tools/stage4-verify/t43-backup-restore-drill.ps1` | 新建 | 取指纹 → `pg_dump -Fc` → 恢复到新库 → 逐项比对；**比不一致即 exit 1**，并对"解析出 0 项"直接判失败 |
| `docs/stage4/acceptance-evidence.md` | 文档 | 第 9 节：三项演练记录与未演练部分；台账 #31 由 ❌ 升为 ✅ |

**验证**：指纹 **12 项全部一致**（含凭据密文 md5、指纹 md5、价格版本、权限、历史消耗）；
恢复库上**连续启动两次**均无 ERROR/panic、行数不变 ⇒ **迁移幂等**；配置 `RegisterEnabled` 改→重启生效→回滚→重启恢复原值
**剩余风险**：① **升级回滚（回退旧二进制）未演练** —— 本环境只有当前构建；② 本地演练**不能替代**生产停机窗口与回滚预案；
③ 演练脚本放在 `tools/stage4-verify/` 且**可重复执行**（会重建 `apigx_restore_drill` 库）
**过程记录**：① 脚本首次执行因 **UTF-8 无 BOM** 被 PS 5.1 按 ANSI 读取而乱码报错 ⇒ 改为带 BOM；
② 我按 `=` 解析 `psql -tAf` 的 `k|v` 输出 ⇒ 解析出 0 项却打印"✅ 全部一致"（**假绿**）⇒ 改为按 `|` 解析并加"0 项即失败"断言；
③ 配置回滚脚本的提示语我写反了（回滚成功却打印"未生效"）—— 属显示逻辑错误，已当场识别
### [017] 2026-09-18 T4.2 权限绕过与日志脱敏（含两条中间件层回归测试）

**做了什么**：阶段 4 的 T4.2 收口 —— ① 用**真实库**查证日志与存储脱敏；② 给两条权限规则补**中间件层回归测试**。

**涉及模块**：middleware（测试）、docs/stage4

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `middleware/auth_test.go` | 测试 | 新增 `TestSetupContextForTokenRejectsPinSuffixForNonAdmin`（普通用户带 `-<channelId>` ⇒ 403 + `specific_channel_version` 头）与 `TestUserAuthRejectsDisabledUser`（停用用户 ⇒ 401 `AUTH_USER_DISABLED`）；前者需在该夹具内补 `AutoMigrate(&model.Token{})` |
| `docs/stage4/acceptance-evidence.md` | 文档 | 第 8 节：脱敏真实库查询表 + 权限绕过代码位置 + 8.3 收尾记录；#5/#26 由 ⚠️ 升为 ✅ |

**验证**：`go test ./middleware/` 两个新用例 **PASS**、整套件 **ok**；台账防伪校验通过；总览重算（✅ 升为 **18**）
**剩余风险**：① 底座 `channels.key` 仍有 7 条形如 `sk-...`（底座自身转发所需）——**已知边界**，未解决；
② 台账仍有 **12 项 ⚠️ 部分覆盖**（含"按执行 Key 计价""池内细节不外泄""进程被杀"等）与 **1 项 ❌ 待演练**（备份恢复/升级回滚）；
③ T4.3–T4.6 未开始
**过程记录**：pin 用例首次失败于夹具缺 `tokens` 表（已补迁移）；期间我误用 PowerShell `String.Replace` 的三参数重载
（不存在）导致文件未改写，改为单参数并加写入校验后通过
### [016] 2026-09-18 阶段 4 验收台账 + T4.1 并发与资源耗尽演练

**做了什么**：阶段 4「受控上线与运维」开工。先把计划书 §11 阶段 4 验收表（**L650–L680，30 行**）
逐条落成**证据台账**，每条标注状态与出处；随后执行 **T4.1**（并发与资源耗尽）当场演练。

**涉及模块**：文档（`docs/stage4/`）、Go 测试（当场执行）

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `docs/stage4/acceptance-evidence.md` | 新建 | 31 项台账：场景 / 状态（✅已有证据 ⚠️部分覆盖 ❌待演练 🏭需生产环境）/ 现有证据或缺口；含"仍需补的演练清单"与复现命令 |
| — | 测试 | T4.1 当场执行：`apigx/capacity` 6 项 + `apigx/channelpool` 6 项，共 12 项 PASS |

**验证**：台账内含**防伪校验**（脚本会逐条核对文中引用的 `Test*` 是否真实存在）；
T4.1 演练输出为当场结果（非引用历史结论）；状态总览由脚本按表格行**重算**而非手写
**剩余风险**：① 台账中 **10 项为"部分覆盖"**（缺口已在表内逐条写明，主要是"按执行 Key 计价""池内细节不外泄"
"账户封禁路径""凭据轮换后查旧任务""重复/修订事件""同键不同参数冲突"等断言）；② **#31 备份恢复/升级回滚未演练**；
③ **指标看板与告警、日志脱敏**属阶段 4 的"工作"项，本轮**未开始**
**过程记录**：台账首次校验即抓到一处**假证据** —— 我引用了 `TestMarkAttemptAbandoned`，
而它是**函数名**不是测试名（仓库中不存在该测试）⇒ 已改为真实测试名，并保留该校验为常规检查
### [015] 2026-09-18 分享者视角的消耗视图（U-3）

**做了什么**：补上"看不到我分享的具体消耗"。**换了数据源**（原定表不可用）后，
后端按**渠道 / 上游 Key / 模型**三个维度聚合分享者自己的调用，前端在「我的分享」加消耗卡。

**涉及模块**：apigx/model、apigx/api、前端 share、i18n

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `docs/stage3/…`（无） | — | — |
| `apigx/model/repo_attempt.go` | 仓储 | 新增 `SummarizeOwnerAttempts`（渠道/Key）、`SummarizeOwnerAttemptsByModel`（模型，字符串聚合）、`FillOwnerUsageNames`；归属由「渠道 → owner_user_id」子查询推出 |
| `apigx/api/share.go` | 接口 | 既有 `usage` 路由组新增 `GET /api/apigx/usage/summary?group_by=channel|key|model`（只返回自己的量） |
| `web/src/features/share/api.ts` | 前端 | `getUsageSummary` |
| `web/src/features/share/components/usage-summary-card.tsx` | 前端 | 消耗卡：三维度切换 + 请求/成功/失败/未完成/输出 token/平均 TTFT；名称空时显示「未记录」 |
| `web/src/features/share/index.tsx`、i18n | 前端 | 挂载消耗卡；7 语各补 9 条 |

**验证**：`go build` ✅；`apigx/model` + `apigx/api` 用例 ✅；接口**真实数据实测**：
`group_by=channel` → 200 且尝试/成功数正确，`key`/`model` → 200；
前端 typecheck 0 错 / oxlint 0 错 / 用例 13/13 / build ✅；网关产物哈希随改动变化 ✅
**剩余风险**：① **金额未显示** —— attempts 表没有费用字段，接口**不臆造**金额；若要看金额需另接底座 logs 口径；
② 部分 attempt 的 `upstream_key_id=0` / `upstream_model_id=''`（异步任务创建等路径未记录）⇒
界面如实显示「未记录」而**不编名字**；这是**数据缺口**，值得后续单独排查；
③ `apigx_usage_events`（0 行、无写入方）仍是一处**未启用的 schema**，要么补写入方、要么明确废弃
### [014] 2026-09-18 邀请码「发放/管理」与「兑换」合并为一个界面（U-2）

**做了什么**：按用户要求把两处合并 —— 「我的分享」的邀请卡（发放/列表/撤销）与独立的「兑换邀请」页面。
现在**同一张卡**里既能发放管理、也能粘贴邀请码兑换；独立页面与导航入口一并删除。

**涉及模块**：前端 access/share、路由、导航

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `web/src/features/access/components/invite-card.tsx` | 前端 | 新增「兑换邀请码」区块（复用 `redeemAccessInvite`），成功后链到模型广场，失败统一提示且不区分原因 |
| `web/src/routes/_authenticated/access-invite/index.tsx` | 路由 | **删除**（独立兑换页） |
| `web/src/features/access-invite/index.tsx` | 前端 | **删除**页面组件；**保留 `api.ts`**（邀请卡在用） |
| `web/src/hooks/use-sidebar-data.ts` | 导航 | 移除「兑换邀请」入口 |
| `web/src/hooks/__tests__/sidebar-config.test.tsx` | 测试 | 同步断言（个人分组不再含该独立入口） |

**验证**：typecheck **0 错**；改动文件 oxlint 0 错（`src/hooks/use-table-url-state.ts:225` 另有一处**既有** error，非本次改动，未触碰）；
hooks/access/i18n 用例 **16/16**；build ✅；网关产物哈希随改动变化
**剩余风险**：① 合并后兑换入口在「我的分享」页内 —— 未受邀的普通使用者需先进入自己的分享页才能兑换，
若日后要为纯使用者提供更显眼的入口，需再讨论放置位置；② 独立页删除后，旧书签 `/access-invite` 将 404（无重定向）
### [013] 2026-09-18 分享渠道/分组的「发布」语义真正生效 + 前端向导（U-1）

**做了什么**：用户反馈"新建渠道之后直接就确定、直接就轮询、也不选择模型"。查清根因后分三步收口：
① 修正被写错的发布状态语义；② 平台侧发布/暂停端点；③ **闸门真正生效**（广场可见性 + 路由候选）+ 前端向导与状态展示。

**涉及模块**：apigx/model、apigx/api、前端 share、验收脚本、流程文档

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `apigx/model/repo_share.go` | 仓储 | 新增 `ShareGroupPublish{Draft,Pending,Published,Suspended,Retired}` 常量与 `SetShareGroupPublishStatus` |
| `apigx/api/share.go` | 接口 | 新建分组初始状态 = 公开⇒待审核 / 非公开⇒已发布；可见性未指定按公开处理（保守默认）；新增 `POST /api/apigx/share-groups/:id/publish-status` |
| `apigx/model/repo_plaza.go` | 闸门 | `plazaVisibilityPredicate` 追加 `AND g.publish_status = 已发布` ⇒ 草稿/待审核/已暂停一律不可见 |
| `apigx/model/repo_offering.go` | 闸门 | `ListPublishedOfferingsByModel` 追加"分组必须已发布"子查询 ⇒ 不进入路由候选 |
| `web/src/features/share/index.tsx` | 前端 | 新建渠道后**自动跳转渠道详情**（选模型）；分组表显示**发布状态** |
| `web/src/features/share/components/*` | 前端 | `ChannelCreateDialog.onSaved` 带上新渠道 id |
| 验收脚本 ×4、`chat-matrix` | 工具 | 建组后补平台审核调用；setup 按**模型去重**（修上一轮记录的异常根因） |

**验证**：`go build` ✅；`apigx/api`+`apigx/model`+`apigx/routing` 用例全过；`capability-gates` **12/12**；
对话矩阵 setup **clean exit 0** + verify **5/5**；前端 typecheck 0 错 / oxlint 0 错 / 相关用例 13/13 / build ✅；
网关产物哈希随改动变化（`index.902c144b90.js` → `index.5722693234.js`）⇒ 改动确实上线
**剩余风险**：① 渠道**尚未**单独建发布状态（当前随分组生效），若日后要按渠道粒度暂停需扩展；
② 渠道列表只显示**分组**发布状态，未显示渠道自身健康/暂停意图；③ MySQL 未在本机验证（改动只加列等值条件，方言无关，但按规范如实标注未跑 MySQL）
### [012] 2026-09-18 阶段 3 验收报告：六类能力支持范围逐项结论（T3.9）

**做了什么**：按计划书 §11 阶段 3 的验收要求，把六类能力**逐行给出结论**（`已验证` / `明确不支持` / `未验证+补齐条件`），
并坦白列出三个未收口项。

**涉及模块**：文档（新增 `docs/stage3/`）、验收脚本判读、对上游的一次探测

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `docs/stage3/acceptance-report.md` | 新建 | 阶段 3 逐项结论表 + 未验证项补齐清单 + 复现命令 |
| `tools/stage3-verify/video-task.mjs.bak` | 清理 | 删除我遗留的备份文件 |

**验证**：结论均有出处（脚本项名 / 代码位置 / 探测响应）；探测：`POST aiping.cn/api/v1/images/edits` → **400「文件读取异常」**（非 404 ⇒ 端点存在）
**剩余风险**：① 图片**编辑**未验证（本会话只有 tokenrhythm 的 Key，缺 aiping 的 Key）；② 语音**上传时长**在当前架构下无法度量（平台不解码音频）⇒ 需用户决策是否改变做法；③ 对话 **Gemini 原生路径**明确不支持（无可用的 Gemini 形态上游）
### [011] 2026-09-18 「邀请码」界面 + 「选择模型」界面（§4 访问授权 / 同步模型）

**做了什么**：用户提出的三个功能缺口（不能删除 / 邀请码在哪设置 / 加了渠道怎么选模型）全部落地：
- [010] 分组与渠道**删除**入口（后端 5 个删除接口早已具备，界面此前没有入口）
- [011] 「访问授权（邀请码）」卡：选分组 + 次数 + 有效期 → 发放 → **一次性明文可复制**；
  列表（前缀/分组/已用/状态）→ 撤销（**连带撤销**由该邀请产生的授权）
- [011] 「选择模型」按钮：先**预览**上游模型清单（不落数据）→ 开关勾选 → 只对**勾选的**建草稿供给
  （原有「同步模型」一键全导的行为**未改动**）

**涉及模块**：前端 share、access、i18n

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `web/src/features/access/api.ts`（新） | 前端 | 邀请发放/列表/撤销 + 授权列表/撤销 |
| `web/src/features/access/components/invite-card.tsx`（新） | 前端 | 邀请码卡（一次性明文 + 撤销） |
| `web/src/features/share/components/model-import-dialog.tsx`（新） | 前端 | 预览 + 勾选导入 |
| `web/src/features/share/api.ts` | 前端 | 4 个删除接口 + createOffering（按选择建供给） |
| `web/src/features/share/index.tsx`、`channel-actions.tsx` | 前端 | 删除列/按钮、邀请卡挂载、选择模型按钮 |

**验证**：typecheck 0 错 / oxlint 0 错 / hooks+i18n+pricing+share+access 用例 **262/262** / build ✅ / 网关产物与 dist 一致
**剩余风险**：无（两轮里我自己的三处失误均被工具当场抓住：嵌套三元、守卫条件写成检查字符串、URL 用单引号写模板变量）
### [009] 2026-09-18 视频真实厂商链路 + i18n 全量审计 + 兑换限流

**做了什么**：① 打通「平台 → 真实厂商」视频链路；② i18n 做**全量审计**并归零；③ 给兑换接口加按用户限流。

**涉及模块**：plugins/tasks/sora、apigx/api、前端 i18n

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `plugins/tasks/sora/plugin.js` | 任务插件 | `models` 增加 `MiniMax-Hailuo-02`（该插件协议与 aiping 的 `/v1/videos` 完全一致）⇒ 登记 **D-028** |
| `apigx/api/share.go` | 接口 | 兑换路由挂 `middleware.UserCriticalRateLimit("access_invite_redeem")`（复用底座机制，关闭开关时 no-op） |
| `web/src/i18n/locales/*.json` | i18n | 审计 4663 个在用键，补齐缺失 10 条（7 语）⇒ **缺失归零** |

**验证**：视频：平台提交 **200** + 轮询 **200**（真实任务，只提交一次）；限流：紧限额 3/60s 下第 4 次起 **429** ✓；
i18n：审计 **0 缺失** ✓；回归：五类验收 5/5·5/5·16/16·12/12·8/8 + Go 12 包 + 前端 1740 用例 ✓
**剩余风险**：无（`hailuo` 停用属**环境配置**，用底座插件机制实现、可逆，已在 `docs/stage0/upstream-diff.md` 记录复现步骤）

### [008] 2026-09-18 「我的分享」补全操作：新建分组 / 新建渠道

**做了什么**：分享者可以自己建分组与渠道；渠道采用"**平台代建底座渠道**"方案，分享者只填 名称/协议/上游地址/**自己的 Key**。

**涉及模块**：apigx/api（接口）、apigx/model、前端 share

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `apigx/api/share.go` | 接口 | `createChannel` 支持配方自建：代建底座渠道（用底座类型常量映射协议）+ 绑定；Key **加密登记**（secretbox 信封 + 指纹，缺主密钥失败关闭） |
| `web/src/features/share/**` | 前端 | `GroupCreateDialog`（含可见性）、`ChannelCreateDialog`、页面两个按钮 |

**验证**：端到端：配方建渠道 **200** → Key 池 1 条 → 同步模型 **200** 且发现上游模型 ✓
**剩余风险**：无（**契约变更**已同步更新用例：缺配方 ⇒ 400、配方不完整 ⇒ 400，锁住"不允许半成品渠道落地"）

### [007] 2026-09-18 「平台管理」并入系统设置

**做了什么**：按用户要求，把平台卡片改为**系统设置的一个分区**（与认证/计费/运维并列），删除独立路由与导航入口。

**涉及模块**：前端 system-settings、apigx-platform

**验证**：typecheck 0 错 / 用例 95/95 / build ✓
**剩余风险**：无

### [006] 2026-09-18 模型广场并入底座 `/pricing` + 分享分组页与独立兑换入口

**做了什么**：① 模型广场改用底座 `/pricing`（新增**分享来源数**、**分享分组筛选**、详情页**分享来源表**）；
② 分享分组页接通真实入口；③ 新增**资源邀请独立兑换页**（§4 第 3 条）。

**涉及模块**：前端 pricing、plaza、access-invite、share

**验证**：typecheck / oxlint 0 错 / 用例通过 / 网关产物与本地 dist 一致 ✓
**剩余风险**：**语音上传时长**仍未验证（平台不解码音频 ⇒ 能力矩阵如实标注 `not_verified`）

### [005] 2026-09-18 UI 收敛：导航归位 + 删除我重复造的页面

**做了什么**：模型广场→常规、我的分享/兑换邀请→个人、平台管理→管理；删除与底座重复的 `/usage`、`/task-center` 页面。

**验证**：hooks 用例全过；用户四条意见（i18n/渠道/平台合并/轮询）逐条闭环
**剩余风险**：无

### [004] 2026-09-18 模型目录（§4「标准模型、别名、版本、能力说明」）

**做了什么**：此前 `apigx_canonical_models` / `apigx_model_aliases` **只有 schema、没有实现**；补齐候选登记、
**只认已确认**的解析、对外**标准模型 ID 与介绍**、以及平台侧候选确认台（含批量匹配）。

**验证**：用例覆盖"候选永不解析成结论""幂等且不打回待确认""未命中返回 false"✓
**剩余风险**：无

### [003] 2026-09-18 注册邀请准入（§5 第 2 条）

**做了什么**：新增 `RegisterInviteRequired` 开关（含**管理后台开关**，即时生效）、注册闸门（**原子核销** + 失败**补偿回补**）、
注册页邀请码输入、专门三语文案。**不复用** `aff_code`（它只是推广归因）。

**验证**：端到端 7/7（无码/无效码被拒、有效码成功、单次码不可复用、**role=1 不提权**）✓
**剩余风险**：无（限流已在 [009] 补上）

### [002] 2026-09-18 分享分组页（访客视角）

**做了什么**：新增 `/plaza/group/:groupPublicId`，展示分组名/介绍/分享者/可见资源；数据**只从广场取**（不另开越权查询）。

**验证**：广场带出分组说明（与编辑弹窗数据闭环）✓
**剩余风险**：无

### [001] 2026-09-18 访问控制：邀请、授权、撤销与到期（后端）

**做了什么**：把 `apigx_invites` / `apigx_grants` 从"只有 schema"补到可用（发放、**原子核销**、访问判定、撤销），并让授权**真正生效**。

**涉及模块**：apigx/model（仓储）、apigx/api（接口）、广场可见性

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `apigx/model/repo_access.go` | 仓储 | 邀请发放（只存哈希）+ **原子核销** + 访问判定 + 只查授权的判定 |
| `apigx/model/repo_access_admin.go` | 仓储 | 所有者校验、邀请列表/撤销、指定用户授权/撤销 |
| `apigx/api/access.go` | 接口 | 7 个访问控制接口（含访客兑换入口） |
| `apigx/model/repo_plaza.go` | 广场 | 判据改为「公开 或 有未过期授权」，列表与详情同源 |

**验证**：go build ✅｜`apigx/api` + `apigx/model` 用例 ✅（含并发兑换原子性、越权防护、撤销与到期）
**剩余风险**：~~① 兑换接口尚无频率限制~~（已在 [009] 关闭）；~~② 前端页面未做~~（已在 [007]/[009] 完成）；~~③ 注册邀请码尚未接入注册流程~~（已在 [003] 完成）
