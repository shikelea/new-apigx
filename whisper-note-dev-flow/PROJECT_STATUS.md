# 项目状态 · apigx（二开 New API）

> 本文件由开发流程维护（`whisper-note-dev-flow/SKILL.md` §4）。**序号倒序，顶部最新**。
> ⚠️ 说明：本项目在**本次会话之前**的进展未按本流程记录，因此编号从 001 开始；更早的详细过程见 `TASK_LOG.md`。

### [051] 2026-09-24 修复同标准模型别名渠道未进入授权路由候选并部署

**做了什么**：修复来源授权仅按请求模型字符串精确匹配的问题，使同一 `canonical_model_id` 下的已发布别名供给都能进入智能路由候选；构建并部署 `v0.0.0-apigx.8932687` 到生产 app。

**涉及模块**：`new-api/apigx/model/repo_access.go`、模型/路由回归测试、生产 Docker app 服务。

**改动明细**：
| 文件 / 位置 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/repo_access.go` | 来源授权 | 请求模型先解析 canonical 身份，再合并同标准模型的已发布供给；无身份时保留精确 ID 兼容路径 |
| `new-api/apigx/model/*_test.go` | 回归测试 | 覆盖标准模型 ID、上游别名和同 canonical 渠道候选 |
| `/pan/apigx-test/releases/8932687/apigx-linux-amd64` | 发布产物 | Linux amd64 二进制 SHA-256 `605c286ce24ab46f8b8370d3cbe333a8fdc06c01a2f37e20ba3141079a57a646` |
| `/pan/apigx-test/backups/pre-8932687-20260924T140011Z` | 回滚 | 保存更新前 app 二进制、编排文件和构建信息 |

**验证**：本地定向与全仓 Go 测试已通过；生产 `/api/status` 返回 `v0.0.0-apigx.8932687`；四服务 healthy；MySQL/Redis/stub 容器 ID 和启动时间保持不变；部署后日志出现正常 `use_channel` 记录，未见新的 `no available source`。

**剩余风险**：尚未做生产故障注入；需要用户通过反代真实调用 `deepseek-flash` 等模型确认别名渠道切换。
## 当前阶段
**生产 APIGX 已升级至 `8932687`：同标准模型别名授权合并已部署。**

当前 inner commit 为 `8932687`，生产 `/api/status` 返回 `v0.0.0-apigx.8932687`；本轮只重建 app，MySQL/Redis/stub 未重启。

## 当前任务
[已完成] 修复同标准模型别名授权候选并部署到“主”服务器。
- 实际运行目录为 `/pan/apigx-test`，通过 `docker compose build app` 与 `docker compose up -d --no-deps app` 只重建 `app` 服务。
- 升级前备份位于 `/pan/apigx-test/backups/pre-8932687-20260924T140011Z`，新发布位于 `/pan/apigx-test/releases/8932687`。
- 应用镜像已重建；二进制 SHA-256 为 `605c286ce24ab46f8b8370d3cbe333a8fdc06c01a2f37e20ba3141079a57a646`；回环状态接口确认版本 `v0.0.0-apigx.8932687`。
- app 启动日志无 panic、fatal、迁移失败或 MySQL/Redis 连接错误；未执行故障注入或业务写入测试。

### [050] 2026-09-24 部署成熟渠道耗尽后的冷启动跨渠道回退

**做了什么**：把本地已通过全仓 Go 回归的 inner `2c8b5a5` 发布到“主”服务器 `/pan/apigx-test`。只重建 `app`，让成熟渠道的 Key 池在当前请求内耗尽后可以回退到其他已授权、未满载、未熔断的同模型渠道；MySQL、Redis 和 stub 保持原实例。

**涉及模块**：Linux amd64 构建产物、Docker Compose app 服务、生产备份与健康检查

**改动明细**：
| 文件 / 位置 | 模块 | 改动说明 |
|---|---|---|
| `/pan/apigx-test/releases/2c8b5a5/apigx-linux-amd64` | 发布产物 | 上传并通过 SHA-256 `b74ca3ff64651932a179bc0197257db9188929cf7e66af105d0f701df8813207` 校验 |
| `/pan/apigx-test/apigx-linux-amd64`、`BUILD_INFO` | 运行版本 | 原子替换为 `v0.0.0-apigx.2c8b5a5` |
| `/pan/apigx-test/backups/pre-2c8b5a5-20260924T104251Z/**` | 回滚 | 保存旧二进制、Compose、Dockerfile、`.env`、旧镜像 ID 和四容器更新前状态 |
| `app` 容器 | 运行服务 | 新镜像 `sha256:63d20be7ce436673a0b5bbd615269122c3fa7b5c3e92295104eb96e89a40d60d`，仅 app 重建 |

**验证**：`app/mysql/redis/stub` 四服务 healthy；`/api/status` 返回 `v0.0.0-apigx.2c8b5a5`；app 于 `2026-09-24T10:47:32Z` 启动；启动日志关键错误匹配 0；MySQL/Redis/stub 容器 ID 与 `2026-09-21` 的启动时间保持不变。

**剩余风险**：尚未在生产执行故障注入或专门构造的冷启动回退测试；用户可通过宝塔反代后的真实请求观察 `retry_attempts`、渠道切换和最终响应。
### [049] 2026-09-24 成熟渠道耗尽后的冷启动跨渠道回退

**做了什么**：修复自动路由只把最高质量候选写入请求 allow-list 的问题。请求开始时仍优先成熟候选；同一请求内这些候选因多 Key 耗尽而被排除后，动态扩大 allow-list 到已授权、未满载、未熔断的其他同模型渠道，避免直接返回“可用渠道不存在”。同时把真实上游 HTTP 429 从平台拒绝改为上游质量失败。

**涉及模块**：路由候选装配、请求级渠道过滤器、同步/异步 Relay 重试、质量结果归因、回归测试

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/relayhook/route_strategy.go` | 智能路由 | 保存主候选/回退候选；主候选全部被当前请求排除后展开回退集合；无可比指标时保留可用渠道 |
| `new-api/controller/relay.go` | 重试接线 | 渠道内 Key 池耗尽并加入排除列表后触发回退展开，同步请求与任务提交共用 |
| `new-api/dto/channel_constraints.go`、`new-api/constant/context_key.go` | 请求状态 | 支持原地替换 allow-list、读取已拒绝渠道并保存路由状态 |
| `new-api/apigx/qualityhook/quality.go` | 质量归因 | 429 仅在明确平台错误码下算平台拒绝，真实上游限流计入上游失败 |
| `new-api/apigx/{relayhook,qualityhook}/*test.go` | 回归 | 覆盖主候选优先、回退只在主候选耗尽后生效、幂等展开和上游 429 分类 |

**验证**：受影响包定向测试通过；清理 Go 缓存后全仓 go test ./... -count=1 -timeout=10m -p 1 全部通过；未连接、修改或重启生产服务器。

**剩余风险**：生产尚未部署；需要部署后用真实请求观察 retry_attempts 与路由日志，确认成熟渠道 Key 池耗尽后确实切到冷启动渠道。
### [048] 2026-09-24 生产日志复盘：智能路由与多 Key 重试链路

**做了什么**：只读检查“主”服务器 `/pan/apigx-test` 的运行配置、数据库重试参数和真实请求日志，并对照本地 Relay、渠道选择、质量路由与 Key 池实现，确认“35 → 35 → …”的真实含义和最终 500 的根因。

**涉及模块**：生产应用日志、`controller/relay.go`、`service/channel_select.go`、`apigx/routing`、`apigx/qualityhook`、`apigx/channelpool`

**关键证据**：
- 生产 `RetryTimes = 3`；请求 `202609240806326113267618268d9d6an9FTXMu` 的日志连续记录 `35 -> 35 -> 35 -> 35 -> 35 -> 35`，对应同一渠道内最多 6 次尝试（初始 Key + 5 次换 Key）。`retry_attempts` 中可见 Key 序号变化，因此不是跨渠道死循环。
- 429 按当前规则只在本次请求内排除失败 Key，不写跨请求冷却；Key 池耗尽后才进入 New API 全局跨渠道重试。
- 该模型的渠道 35 已有质量样本（自然请求 7149、成功 839、平台拒绝 6310、上游失败 0），其他同模型供给样本为 0。自动路由只把成熟候选纳入排名，冷启动探索未开启；渠道 35 的 Key 池耗尽后，其余渠道没有进入允许集合，最终记录“分组 default 下模型 deepseek-flash 的可用渠道不存在（retry）”并返回 500。
- 生产上游返回的 429 内容是 `inference exceeds tpm/rpm limit`、`token plan entitlement exhausted` 等真实上游限流，但 `qualityhook.ClassifyOutcome` 目前按 HTTP 429 归为平台拒绝，导致上游限流没有进入上游失败/质量熔断统计。

**结论**：多 Key 轮换已经按设计运行；当前路由故障是“成熟渠道失败后没有冷启动跨渠道兜底”，并叠加上游 429 被错误记为平台拒绝。此次只读复盘未修改代码、配置、数据库、Redis，也未重启或注入生产流量。

**剩余风险**：需要本地修复并验证真实上游 429 的分类、冷启动探索/兜底与路由决策日志后，再安排生产变更；在此之前，生产遇到成熟渠道 Key 池同时失败时仍可能直接返回 500。
### [047] 2026-09-22 生产服务器升级至 Key 冷却版本

**做了什么**：通过 Xterminal 将已完成本地回归的 APIGX `88230c1` 部署到“主”服务器 `/pan/apigx-test`，只重建应用服务并保留原 MySQL、Redis 与回滚产物。

**涉及模块**：Linux 生产二进制、Docker Compose 应用服务、服务器内发布与回滚目录、部署台账

**改动明细**：
| 文件 / 位置 | 模块 | 改动说明 |
|---|---|---|
| `/pan/apigx-test/releases/88230c1/**` | 发布产物 | 上传并校验 `v0.0.0-apigx.88230c1` Linux ELF 与压缩包 |
| `/pan/apigx-test/apigx-linux-amd64`、`BUILD_INFO` | 运行版本 | 原子替换应用二进制并记录 commit、tree 与 SHA-256 |
| `/pan/apigx-test/backups/pre-88230c1-20260922T111251Z/**`、`apigx:rollback-c0c2c84-20260922t111251z` | 回滚 | 保存升级前二进制、Compose、环境配置、BUILD_INFO 和旧应用镜像 |
| `whisper-note-dev-flow/{PROJECT_STATUS,TASK_LOG}.md` | 流程台账 | 记录生产升级范围、健康证据和剩余风险 |

**验证**：本地重新完成 Rsbuild 生产构建和 Linux amd64 交叉编译，二进制 SHA-256 `8f36c58de217759c0f8baefa25de3ec66bd4e2a09e06950e7ac7d85aca25c561`；远端压缩包与二进制哈希复核通过；仅 `app` 容器于 2026-09-22 19:15（Asia/Shanghai）重建，MySQL/Redis 容器保持原实例和启动时间；四服务 healthy，`/api/status` 返回 APIGX `v0.0.0-apigx.88230c1`，启动日志关键错误匹配数为 0。

**剩余风险**：按用户要求未在生产执行故障注入或业务写入验收，智能路由与多 Key 行为待用户真实测试；启动日志仍提示 `SESSION_COOKIE_SECURE` 未启用且 refresh/logout Origin 校验关闭，需结合最终 HTTPS 反代域名单独收口。

### [046] 2026-09-22 区分 429 与其他可重试错误的 Key 冷却

**做了什么**：把领域层已有但未生效的 Key 冷却接入真实失败链路和底座运行时选择器；429 保持请求内切换语义，临时上游故障使用 60 秒持久化冷却并自动恢复。

**涉及模块**：Key 失败归类、底座多 Key 运行时状态、分享渠道投影、Key 池状态展示、七语言与补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/channelpool/{key_stats,outcome,projection}.go`、`apigx/model/repo_upstream.go` | 失败归类与投影 | 429/业务错误/鉴权错误/临时故障分流；冷却 Key 保留在投影并同步截止时间 |
| `new-api/model/channel.go` | 底座选择器 | 按稳定 Key ID 持久化冷却截止时间，选择时跳过未来截止时间并动态恢复 |
| `new-api/web/src/features/share/components/key-pool-card.tsx`、`i18n/locales/*.json` | 用户交互 | 说明 429 例外与 60 秒冷却，显示“冷却至 HH:mm:ss” |
| `deploy/patches/0182-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 可重建交付 | 固化 inner `88230c1`、182 个补丁与锁定基线重放证据 |

**验证**：定向 Go 包、`go test ./... -count=1 -timeout=10m -p 1`、SQLite/PostgreSQL 18.4/MySQL 8.0.12 持久化矩阵、前端组件和全量 163 文件 1788 项、typecheck、变更文件 lint/format、七语言 JSON、Rsbuild 生产构建与嵌入前端的 Go 二进制构建均通过；182 个补丁重放 tree 与 fork HEAD 一致。

**剩余风险**：真实第三方可能使用非标准状态码或在 HTTP 200 响应内返回业务错误，本轮分类以当前 Relay 能取得的 HTTP 状态为准；生产多实例竞争仍需在正式升级流程中观察。本轮未连接或部署生产服务器，也未推送远端。

### [045] 2026-09-22 模型广场来源卡片显示真实渠道名称

**做了什么**：补齐模型广场列表和详情的真实分享渠道名称，把来源卡片的信息层级调整为渠道名称在上、分享者在下，并停止把分享分组名称称为“分享名称”。

**涉及模块**：广场查询与 API 契约、模型详情来源卡片、前端类型和展示回归、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/repo_plaza.go`、`apigx/api/share.go` | 广场契约 | 列表查询、详情查询和 JSON 响应增加非敏感的 `channel_name` |
| `new-api/web/src/features/plaza/types.ts`、`pricing/components/model-details-sources.tsx` | 来源卡片 | 渠道名称作为主标题，分享者显示在次行；缺少渠道名时回退渠道对外 ID |
| `new-api/apigx/{api,model}/*test.go`、`web/src/features/pricing/__tests__/model-details-sources.test.tsx` | 回归 | 覆盖列表/详情渠道名契约以及渠道名与分享者的展示顺序 |
| `deploy/patches/0181-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 可重建交付 | 固化 inner `594c5c3`、181 个补丁与锁定基线重放证据 |

**验证**：`go test ./apigx/model ./apigx/api -count=1 -timeout=5m` 通过；前端定向 1 文件 1 项及既有来源聚合回归通过，typecheck、变更文件 oxlint/oxfmt 和 Rsbuild 生产构建通过；181 个补丁重放 tree 与 fork HEAD 一致。

**剩余风险**：本轮未连接、修改或重启生产服务器；真实生产页面要在部署 `594c5c3` 后由用户验收。组件回归触发一条既有 Base UI `nativeButton` 测试警告，不影响测试结果或本次来源卡片行为。

### [044] 2026-09-22 模型映射、身份确认与广场来源归并

**做了什么**：把分享者模型映射、管理员身份确认和模型管理统一成一条供给生命周期；确认/改映射会迁移真实供给并重建底座投影，模型广场按最终标准身份聚合且同一渠道只保留一个来源。

**涉及模块**：分享供给送审、模型目录与身份迁移、底座模型投影、广场聚合与路由代表、质量归属、分享者/管理员前端、七语言、三库与本地浏览器回归

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/{repo_catalog.go,repo_offering.go}`、`apigx/api/share.go` | 映射与身份确认 | 草稿保存映射意图，送审才登记候选；拒绝退回可编辑状态；确认/改映射迁移同一归一化身份的供给并清理孤立身份 |
| `new-api/apigx/api/share.go`、`apigx/route/route.go` | 底座投影与兼容回填 | 标准模型确认后重建渠道模型、`model_mapping` 和能力；启动时回填历史已确认身份 |
| `new-api/apigx/model/repo_plaza.go`、`repo_offering.go`、`qualityhook/quality.go` | 广场、路由与质量 | 按最终标准模型聚合；同一渠道的多个上游别名只选一个稳定代表，并保持质量与价格归属一致 |
| `new-api/web/src/features/{share,apigx-platform,models,pricing}/**`、`i18n/locales/*.json` | 用户交互 | 分享者设置/修改映射并重提；管理员在模型管理中完成待确认、已确认、拒绝和改映射；来源详情去重且保留质量和价格 |
| `new-api/service/billing_session_quality_test.go`、相关 APIGX 测试 | 回归夹具 | 质量结算夹具补齐已发布、已确认前置条件；新增身份迁移、拒绝重提、广场去重和三库矩阵覆盖 |
| `deploy/patches/0180-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 可重建交付 | 固化 inner `ba64967`、180 个补丁与锁定基线重放证据 |

**验证**：前端格式与变更文件 lint、typecheck、18 个定向文件 98 项、全量 162 文件 1786 项和 Rsbuild 生产构建通过；受影响 Go 包及 `go test ./... -count=1 -timeout=10m -p 1` 通过；SQLite、PostgreSQL 18.4、MySQL 身份生命周期矩阵通过；本地浏览器实走草稿映射、批量送审、拒绝重提、确认/改映射和发布，两个大小写别名最终只显示一张卡和一个来源，模型说明、6 小时可靠性、延迟、吞吐量及 `$0.150000 / $0.600000` 参考价格正确，console error 0；180 个补丁重放 tree 与 fork HEAD 一致。

**剩余风险**：按用户要求未连接、测试或部署生产服务器；生产多实例竞争、历史大数据量回填时间与真实第三方模型命名仍需在用户的生产升级流程中观察。全仓 lint 仍有锁定 New API 基线既有问题，本轮变更文件 lint 已通过。

### [043] 2026-09-21 分享删除链路彻底撤下渠道与广场缓存

**做了什么**：统一分享渠道、分组和供给的删除收尾，区分平台代建与管理员绑定的底座渠道归属，补启动时历史残留对账，并让前端删除成功后立即刷新广场和渠道列表。

**涉及模块**：分享资源删除生命周期、底座渠道投影、启动对账、路由缓存、模型广场缓存、七语言文案、三库与本地浏览器回归

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/api/share.go`、`apigx/model/repo_share.go` | 分组与渠道删除 | 分组删除逐项复用渠道收尾；删除接口保持所有者幂等并保护其他用户资源 |
| `new-api/apigx/channelpool/projection.go` | 底座渠道归属与对账 | 平台代建渠道物理删除，管理员绑定渠道只停用清空；启动时清理旧版孤儿与墓碑 |
| `new-api/apigx/route/route.go` | 启动接线 | 领域迁移完成后执行一次删除残留对账 |
| `new-api/web/src/features/share/**`、`i18n/locales/*.json` | 前端一致性 | 删除后失效分享、广场和底座渠道查询；分组删除文案明确子渠道会停止服务 |
| `deploy/patches/0179-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 可重建交付 | 固化 inner `8d768a1`、179 个补丁与锁定基线重放证据 |

**验证**：受影响 Go `apigx/api`、`channelpool`、`model`、`route` 通过；SQLite、PostgreSQL、MySQL 生命周期矩阵此前已覆盖代建/绑定、分组级联与历史用量边界；前端 typecheck、分享模块 11 文件 33 项、七语言 JSON、lint/format 和生产构建通过；本地真实浏览器创建分组/分享渠道/底座渠道后删除分组，页面立即消失且数据库确认分享渠道保留墓碑、代建底座渠道物理删除；179 个补丁重放 tree `452aafd626573d0ea4eb386ec74ffb4aa33b2d37` 与 fork HEAD 一致。

**剩余风险**：按用户要求未连接或部署生产服务器；生产升级后的缓存、多实例与真实第三方行为仍需用户在生产变更流程内验证。历史管理员绑定渠道在分享删除后会停用并清空凭据，这是既有安全收尾语义，恢复需管理员重新配置。

### [042] 2026-09-21 资源发布、模型同步与供给删除闭环

**做了什么**：取消分享分组审核，修复公开供给审核后仍不可见、影子渠道误删导致模型同步 500/无限加载、供给无法删除、注册邀请码无管理入口和重试轨迹无法区分同渠道换 Key 等问题，并部署到隔离测试服完成真实流程验收。

**涉及模块**：分享分组与模型供给生命周期、影子渠道恢复、上游模型同步、注册邀请码管理、使用日志重试轨迹、底座用户分组文案、三库回归、补丁链与隔离测试服

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/{repo_offering.go,repo_share.go,repo_access_admin.go}`、`apigx/api/{share.go,access.go}` | 资源生命周期与邀请码 | 分组创建即发布并迁移历史准备态；重复同状态操作幂等；供给删除同步撤下底座能力并保留用量；管理员注册邀请码生成/脱敏列表/撤销 |
| `new-api/controller/channel.go`、`new-api/apigx/api/share.go` | 模型同步与影子渠道恢复 | 禁止通用渠道删除接口破坏存活的 APIGX 影子渠道；已误删时从密文 Key/配方重建，并用 CAS 收敛并发修复 |
| `new-api/controller/relay.go`、`new-api/service/log_info_generate.go`、`new-api/web/src/features/usage-logs/**` | 重试轨迹 | 写入渠道与从 1 开始的 Key 序号；新版按渠道分段展示 Key 尝试，旧版重复渠道压缩显示 |
| `new-api/web/src/features/{share,system-settings}/**`、`i18n/locales/*.json` | 用户流程 | 增加供给删除与确认、同步失败结束加载、注册邀请码管理卡；底座用户分组改名为“底座分组（高级）”并解释与 APIGX ACL 的边界 |
| `deploy/patches/0177-*`、`0178-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 可重建交付 | inner `c0c2c84`、178 个补丁及锁定基线重放证据 |

**验证**：受影响 Go 三包通过；SQLite、PostgreSQL 18.4、MySQL 分组幂等与渠道生命周期矩阵通过；真实 MySQL 6 请求并发影子渠道修复收敛为同一新渠道；前端 typecheck、3 项新增组件测试、全量 162 文件 1785 项、生产构建、变更文件 lint 与七语言 JSON 通过；178 补丁重放 tree `bcf97120ab2c4d8ea69c2e4c165825b7263c7c83` 与 fork HEAD 一致。测试服 `v0.0.0-apigx.c0c2c84` 四容器 healthy：历史渠道恢复后同步 9 个模型；注册邀请码创建时仅返回一次明文、列表仅含前缀且撤销生效；分组创建即发布且重复发布成功；独立供给发布后广场可见并路由 200，删除后列表消失、详情 404、路由 503，同时该供给 3 条用量快照仍保留；6+6 Key 故障链路按两条渠道和 12 把不同凭据执行，429 Key 下一请求仍可用。二进制 SHA-256 `bc329a305f2ba694e4af9f8b4778bc969d266cc6ea94fd5c9675a4baae2d91e5`。

**剩余风险**：隔离测试服是单机、单应用进程与可控上游；尚未覆盖真实第三方错误格式、生产多实例竞争、持续故障压测、域名/TLS/CDN/反代链路和正式安全验收。全仓 lint 仍受未改动的 New API 上游历史问题影响，当前变更文件 lint 已通过。

### [041] 2026-09-21 批量 Key、有限故障切换与熔断自动恢复

**做了什么**：补齐创建前/创建后的批量 Key 管理，把渠道内 Key 尝试上限与 New API 全局跨渠道重试预算衔接，并用持久状态机、后台最小探测、凭据阻塞和恢复质量基线完成无人流量时的自动恢复。

**涉及模块**：分享渠道与 Key 池、同步/异步重试、持久熔断探测、智能路由预过滤、质量桶、System Task、隔离测试服、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/api/share.go`、`web/src/features/share/**` | 批量 Key | 创建兼容单 Key/多 Key，已有渠道整批事务添加；前端统一多行输入、去空去重和数量提示 |
| `new-api/service/multi_key_failover.go`、`controller/relay.go` | 故障切换 | 单渠道初次加 5 次换 Key，不消耗全局预算；池耗尽后复用 `RetryTimes` 跨授权渠道；429 不跨请求冷却 |
| `new-api/apigx/model/repo_breaker_probe.go`、`controller/system_task_handlers.go` | 熔断恢复 | 持久 open/recovering/auth_blocked/closed 状态、租约、防重复探测、连续两次成功恢复和失败退避 |
| `new-api/apigx/{relayhook/route_strategy.go,routing/assemble.go}` | 路由与质量 | 候选预过滤时建立持久状态；活动探测状态优先；恢复后从新质量基线计算 |
| `new-api/apigx/relayhook/breaker_guard.go`、`controller/channel-test.go` | 主动探测 | 内部探测只绕过自身熔断守卫，保留 ACL、Key、容量和真实 Relay；深拷贝渠道状态避免污染缓存 |
| `tools/stage2-verify/{bounded-failover-breaker,stub-profile}.mjs`、`deploy/test-server/README.md` | 远端验收 | 固化 6+6 Key、429 无冷却、重启持久化、401 阻塞、两次成功恢复与计量隔离步骤 |

**验证**：定向回归与 `go test ./... -count=1 -timeout=10m -p 1` ✅；SQLite/MySQL/PostgreSQL 29 表迁移、前端 160 文件 1782 项、typecheck/lint/format/build、桌面/移动批量流程此前通过 ✅；远端 `faa0310` 从零创建熔断行且渠道自动禁用，重启后状态保留，401 后 `state=3` 且 20 秒不再探测，轮换 Key 后第一次成功 `state=1/consecutive_success=1` 且用户只走健康来源，第二次成功 `state=4` 并恢复渠道；`probe_count 0→1→2→3`，坏渠道消费日志保持 5；最终 6+6 共 12 把不同 Key、HTTP 429、跨渠道一次、429 Key 后续仍入选；176 个补丁重建 tree `97ebe71b1b2572956b9d897813ddf2915f2e4522` 与 fork HEAD 一致。

**剩余风险**：证据来自单机、单应用进程和可控上游；尚未覆盖真实第三方错误格式、生产多实例竞争、持续故障压测、域名/TLS/CDN/反代链路和正式安全验收，因此当前可用于测试环境，不作为生产上线结论。

### [040] 2026-09-21 隔离测试服部署与核心路由验收

**做了什么**：把当前 APIGX fork 部署到“主”服务器的独立 `/pan/apigx-test` 栈，并用内网可控上游完成渠道内多 Key、401/429 故障切换、质量/容量智能路由和重启持久化验收。

**涉及模块**：测试服 Docker Compose、MySQL/Redis 持久化、可控上游、Stage 2 验收脚本、Xterminal 远端部署

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `deploy/test-server/**` | 部署编排 | APIGX、独立 MySQL 8.2、独立 Redis、内网 stub 和按 profile 启动的验收容器；持久目录全部固定到 `/pan/apigx-test` |
| `tools/stub-upstream/server.mjs` | 可控上游 | 支持按请求注入状态序列，用于证明第一次 401/429 后第二把 Key 成功 |
| `tools/stage2-verify/multi-key-failover.mjs` | 多 Key 验收 | 创建隔离模型并验证等权轮询、401/429 后同渠道换 Key；动态模型通过正式管理员价格接口设置最小正价格 |
| `tools/stage2-verify/{route-strategy,key-weight-distribution}.mjs` | 路由验收 | 支持远端凭据、容器内上游地址和新环境自动创建平台 Key；质量路由动态模型同步写入全局价格配置 |

**验证**：远端四服务 healthy，`/api/status` 返回版本 `v0.0.0-apigx.bf36a3a`；只监听 `127.0.0.1:13000`，MySQL/Redis/stub 无宿主端口；轮询请求 8/8、两 Key 4:4；429 与 401 均两次尝试且使用不同 Key，最终 HTTP 200；默认自动、显式 auto、可靠性、延迟、吞吐各 8/8 只选优质 A；A 并发槽占满时 A/B=1/1，释放后 A/B=1/0；MySQL 质量桶重启前后 7/7，Redis AOF 探针保持；`.env` 与验收凭据权限均为 600。

**剩余风险**：这是单机、单应用进程、可控上游的联网前测试证据；尚未覆盖真实供应商错误格式、生产多实例竞争、持续负载、域名/TLS/CDN/反代链路和正式安全验收，因此可用于测试环境，不作为生产上线结论。

### [039] 2026-09-21 数据看板共享资源用量本地化

**做了什么**：补齐数据看板共享资源用量标题的七语言翻译，使中文界面的区块、页签和卡片不再回显英文键名。

**涉及模块**：数据看板国际化、前端构建与浏览器回归、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/web/src/i18n/locales/*.json` | 国际化 | 七种语言增加 `Shared resource usage`；简体中文为“共享资源用量”，繁体中文为“共享資源用量” |
| `deploy/patches/0172-*`、`deploy/base.lock.json`、`docs/stage0/upstream-diff.md` | 交付 | 固化 fork HEAD、172 个补丁与锁定基线重建证据 |

**验证**：七语言 JSON 解析、oxfmt、i18n 1 文件 3 项、Rsbuild 生产构建 ✅；嵌入前端的 Go 二进制重建并重启本地网关；`/dashboard/shared-usage` 的页面标题、页签和卡片均显示“共享资源用量”，数据正常加载且 console error 0 ✅；172 个补丁重建 tree `e3e041bdca20f3230ef7897e87a25feff8e7a4bd` 与 fork HEAD 一致；inner commit `bf36a3a99d5e30da230898ee9eec335902163092`。

**剩余风险**：无新增风险；本补丁只修正文案，不改变共享用量数据与路由逻辑。生产上线边界仍与 [038] 一致。

### [038] 2026-09-20 接手整改阶段 G5：首页产品化、模型目录文案与 APIGX 品牌统一

**做了什么**：把默认主页从通用 New API 宣传页改成 APIGX 共享模型资源平台入口，重写管理员模型身份确认文案，并把 fork 的默认产品身份、迁移、开源说明和桌面应用统一到 APIGX；New API 继续作为上游来源和真实兼容语义保留。

**涉及模块**：默认主页、模型身份确认、系统默认值迁移、布局与 About、开源 README/NOTICE、Electron 元数据、七种语言、浏览器与补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/web/src/features/home/**` | 产品首页 | 展示模型聚合、统一平台 Key、质量与容量路由、多 Key 故障切换、六类能力、免费用量记账和分享者流程 |
| `new-api/web/src/features/apigx-platform/**` | 模型身份确认 | 用管理员可执行的确认、合并与广场展示结果替换 Offering、候选、对外标准 ID 等内部术语 |
| `new-api/common/constants.go`、`model/frontend_option_migration.go` | 默认品牌迁移 | 新安装默认 APIGX；旧库仅把精确的 `SystemName=New API` 迁移为 APIGX，自定义系统名不变，三库使用 GORM 结构条件 |
| `new-api/web/{index.html,src/components/layout/**,src/features/about/**}` | Web 身份与署名 | 标题、页头、侧栏、页脚和 About 使用 APIGX；保留 New API 仓库、AGPLv3 和项目贡献者署名 |
| `new-api/{README*,NOTICE,electron/**}` | 开源与桌面发行 | fork README/NOTICE、包名、产品名、窗口、托盘和崩溃日志统一为 APIGX，保留上游 notices |
| `deploy/patches/0171-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、品牌边界、差异统计和 171 个补丁可重建证据 |

**验证**：前端全量 158 文件 1778 项 Vitest（前序收口）及最终主页/目录 2 文件 5 项 ✅；typecheck、变更文件 oxlint/oxfmt、七种语言 JSON 解析、Rsbuild 生产构建 ✅；Go `common`、`model`、`controller` ✅；SQLite、PostgreSQL 18.4、MySQL 8.0.12 默认品牌迁移矩阵 ✅；1440×1000 浅色/深色与 390×844 深色/移动菜单无横向溢出，真实模型广场保留 6 小时可靠性、P50 延迟、吞吐、来源数与来源详情，About 显示 APIGX、New API 上游和 AGPLv3，已登录新标签 console error 0 ✅；171 个补丁重建 tree `64a4bd61a15459b7835a4c1007d2b988804bddf8` 与 fork HEAD 一致；inner commit `ab677b6e347ae2b374764af4847f985ff9a8b9a7`。

**剩余风险**：仓库全量 oxlint 仍会报告锁定 New API 基线中既有的大量规则债务，本轮改动文件已通过；浏览器证据覆盖关键桌面/移动视口，不替代完整设备与无障碍审计；当前证据仍不替代真实第三方账号、生产多实例、持续压测或正式安全验收。

### [037] 2026-09-20 接手整改阶段 G4：分享模型人工定价与批量送审

**做了什么**：补齐动态计价表达式和未知价格的人工定价入口，把模型定价状态、单项送审和渠道级批量送审收进分享者模型工作台；以 owner、状态和修订号条件更新保证价格快照不会跨用户或越过送审状态。

**涉及模块**：分享者人工定价、不可变价格快照、模型批量送审、价格预览回填、渠道模型工作台、三库/前端/浏览器/补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/api/share.go`、`apigx/model/repo_price.go` | 人工定价 | 新增 owner-scoped 人工价格接口，十进制字符串精确转微美元，保存锁定快照并以状态/修订号 CAS 挂接 Offering |
| `new-api/apigx/api/share.go`、`share_test.go` | 批量送审 | 新增渠道级提交接口，只提交已定价草稿/已驳回模型并返回逐项结果 |
| `new-api/apigx/price/match.go`、`web/src/features/share/**` | 价格工作台 | 动态表达式/未知价格可手填；已有人工价格完整回填；模型表显示定价状态并提供单项与批量送审 |
| `new-api/apigx/model/{repo_price_test,migrate_matrix_test}.go` | 原子与三库验证 | 覆盖状态/修订竞态回滚，并在 SQLite、PostgreSQL 18.4、MySQL 8.0.12 执行人工价格生命周期 |
| `deploy/patches/0170-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、差异统计和 170 个补丁可重建证据 |

**验证**：前端 typecheck、oxlint、oxfmt、157 文件 1775 项 Vitest、Rsbuild ✅；受影响 Go 包与 `go test ./... -count=1 -timeout=10m -p 1` ✅；SQLite/PostgreSQL 18.4/MySQL 8.0.12 人工价格矩阵 ✅；普通账号完成动态表达式 → 手动定价 → 已定价 → 一键送审 → 待审核，390×844 无横向溢出、console error 0 ✅；170 个补丁重建 tree `b51450e809e49e5664393a2f02a98b602e182c5a` 与 fork HEAD 一致；inner commit `89b80286ebe2e34c2a70fe892f08566a77463239`。

**剩余风险**：证据来自本地数据库、可控上游和有限浏览器视口，不替代真实第三方、生产多实例、持续压测或正式安全验收；默认并行 Go 全仓曾出现一次既有 SQLite/熔断夹具资源竞争波动，失败用例连续 10 次、controller 整包及串行全仓复跑均通过，未发现与 G4 改动相关的失败。

### [036] 2026-09-20 接手整改阶段 G3：流式响应后的重试边界

**做了什么**：审查锁定 New API 基座的 Key 选择、错误分类、跨渠道重试和主要流式适配器；在复用这些组件的同时增加响应提交守卫，防止部分 SSE 已写出后继续换 Key/渠道或追加 JSON 错误体。

**涉及模块**：Relay 错误判定、渠道内多 Key、跨渠道重试、顶层错误呈现、流式控制器回归、三库/补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/service/relay_error.go` | 重试判定 | 跨渠道与同渠道重试统一在错误分类前检查 Writer 是否已写出 |
| `new-api/controller/relay.go` | 错误呈现 | 已开始 HTTP 响应时只记录错误，不再追加 JSON；WebSocket 继续发送协议错误帧 |
| `new-api/{service/relay_error_test.go,controller/relay_multi_key_failover_test.go}` | 回归验证 | 覆盖未写出仍可重试、写出后两类重试均停止，以及真实 Anthropic SSE 部分输出后坏帧不换 Key、不拼接响应 |
| `deploy/patches/0169-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、基座复用结论、验证结果和可重建补丁链 |

**验证**：service/controller 定向回归 ✅；可控 HTTP 验证 Anthropic SSE 输出 `partial` 后坏帧只访问 `key-a`、不访问 `key-b` 且无 JSON 错误拼接 ✅；原 429→同渠道换 Key→授权渠道、零全局重试 401 换 Key、400 不换 Key ✅；`go test ./... -count=1 -timeout=10m` ✅；SQLite/PostgreSQL 18.4/MySQL 8.0.12 三库矩阵 ✅；169 个补丁重建 tree `119822f37a6425f7a838edbdfa9412ef472147f8` 与 fork HEAD 一致；inner commit `d83af55402e94c2af893a108bda36e23e22a30f6`。

**剩余风险**：流式故障使用本地可控 Anthropic SSE，不替代真实供应商的中途断流、HTTP/2 复位、生产多实例竞争和持续长连接压测；响应已开始后客户端会保留部分结果并看到连接终止，平台不会跨上游续接同一条流。

### [035] 2026-09-20 接手整改阶段 G2：渠道内多 Key 故障切换

**做了什么**：在 New API 原生多 Key 选择、错误重试和跨渠道路由上增加最薄的请求级接线；可重试上游错误先排除当前失败 Key 并重选同渠道 Key，池耗尽后再切授权渠道，同时修正重试前容量尝试被 Writer 默认状态误记成功的问题。

**涉及模块**：多 Key 选择、请求级失败状态、同步 Relay、异步任务提交、容量尝试账本、分享渠道前端、三库/HTTP/补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/model/channel.go`、`service/multi_key_failover.go` | Key 选择 | 在现有加权轮询/随机上增加稳定 Key ID 与索引排除；单请求按渠道隔离失败集合并限制最多 10 把 Key |
| `new-api/service/relay_error.go`、`controller/relay.go` | 同步/异步重试 | 复用底座错误策略；429/401/临时错误同渠道优先，Key 池耗尽后才进入原跨渠道预算；400、`SkipRetry`、取消和本地任务错误停止轮换 |
| `new-api/middleware/distributor.go`、`apigx/relayhook/capacity_hook.go` | 上下文与容量 | 每次 Setup 从未失败 Key 中选择并保存稳定身份；重试前用真实上游状态结束 Attempt、释放旧租约，新 Key 获取新租约 |
| `new-api/controller/*failover*_test.go`、`apigx/relayhook/capacity_hook_test.go` | 行为验证 | 覆盖 429 两把 Key 后换授权渠道、未授权渠道隔离、零全局重试下 401 换 Key、400 不换 Key，以及 Writer 仍为 200 时真实 429 入账 |
| `new-api/web/src/features/share/**`、`i18n/locales/*.json` | 分享者配置 | 保留加权轮询/加权随机选择，并说明可重试错误会自动换当前渠道内下一把可用 Key |
| `deploy/patches/0168-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、复用边界、验证结果和可重建补丁链 |

**验证**：真实 Redis 尝试账本验证旧租约释放、429 失败状态/真实 HTTP 状态和新租约分配 ✅；可控 HTTP 验证 `key-a 429 → key-b 429 → 授权渠道 key-c 200`、未授权 `key-z` 零访问、`RetryTimes=0` 下 401 换 Key、400 不换 Key ✅；受影响 Go 包与 `go test ./... -count=1 -timeout=10m` ✅；SQLite/PostgreSQL 18.4/MySQL 8.0.12 自助渠道生命周期矩阵 ✅；前端全量 156 文件 1771 项、typecheck、变更文件 oxlint/oxfmt、Rsbuild 与嵌入式 Go 二进制构建 ✅；168 个补丁重建 tree `22d57e448637313116157b7110b67ea6fe660571` 与 fork HEAD 一致；inner commit `913aebe6d81cdde90e3405d1af35748f9a9e28c0`。

**剩余风险**：错误矩阵使用本地可控上游与单进程 Redis/数据库，尚不能替代真实供应商的错误报文、生产多实例竞争和持续故障压测；10 Key 上限是单次请求的保护阈值，拥有更多 Key 的池不会在一次请求里无限遍历；历史 PostgreSQL 渠道的非法 `model_mapping` 转义仍是既有数据债。

### [034] 2026-09-20 接手整改阶段 G1：新用户信息架构与资源工作流

**做了什么**：从新普通用户、受邀使用者、分享者和管理员四条真实任务流重新收口前端；把资源范围统一成公开、自己的私有、别人分享给我，同模型聚合展示并保留质量指标；拆出访问授权、合并共享用量与数据看板，简化平台 Key 和定价同步交互。

**涉及模块**：模型广场、平台 Key、访问授权、数据看板、我的分享、模型定价同步、资源 ACL 与路由、i18n、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/model/{repo_plaza,repo_quality,repo_apikey}.go`、`apigx/api/{access,share}.go` | 资源契约 | 广场返回公开/私有/分享给我范围和来源质量；本人私有资源与有效授权进入可访问集合；平台 Key 保存同一资源范围；吞吐统一由质量视图计算，缺失保持未知 |
| `new-api/web/src/features/pricing/**` | 模型广场 | 同模型 ID 聚合卡片；卡片保留 6 小时可靠性、P50 延迟、文本吞吐和来源数；详情展示分享人、分享名称、来源级质量/价格，并直接配置自动或指定来源 |
| `new-api/web/src/features/keys/**` | 平台 Key | 简化 Key 创建/编辑；独立资源配置使用公开、私有、分享给我、全部或指定来源，与广场语义一致；自动模式明确为质量与容量综合选择 |
| `new-api/web/src/features/access/**`、`routes/_authenticated/access/**` | 访问授权 | 新增 `/access`，集中资源邀请发放、撤销和邀请码兑换；受邀资源回到广场“分享给我”查看 |
| `new-api/web/src/features/dashboard/**`、`features/share/**` | 用量与分享 | 移除免费共享场景的余额/剩余额度主流程；共享资源用量并入数据看板；“我的分享”聚焦资源维护并把容量选择改为窄屏可用布局 |
| `new-api/web/src/features/system-settings/models/**` | 管理员定价 | 上游价格同步应用后使当前系统价格基线失效并刷新；APIGX 价格匹配仍只读已生效的 New API 本地配置 |
| `deploy/patches/0167-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 G1 结论、fork HEAD、差异统计与可重建补丁链 |

**验证**：模型广场 3 文件 13 项定向测试 ✅；仪表盘与侧边栏 2 文件 19 项测试 ✅；前端全量 156 文件 1771 项 Vitest、typecheck、56 个变更文件 oxfmt、49 个变更 TS/TSX oxlint、Rsbuild 生产构建 ✅；`go test ./... -count=1 -timeout=10m`、SQLite/PostgreSQL/MySQL 矩阵与嵌入式 Go 二进制 ✅；390×844 与 1440×1000 广场均显示可靠性/延迟/吞吐，移动标签与详情无重叠或横向溢出，平台 Key 显示“综合自动（质量与容量）”，console error 0 ✅；167 个补丁重建 tree `8c3d13464f82eb194575031e0244ae0e133ac0bb` 与 fork HEAD 一致；inner commit `d15104c5f49e464dedfc6c46dee5885db93fe854`。

**剩余风险**：质量聚合与交互证据来自本地样本和两种关键视口，不是生产流量分布或完整设备/无障碍审计；真实第三方账号、生产多实例竞争、持续压测和正式安全验收仍未完成。ShareGroup 继续作为后端授权与发布边界，后续升级不能把它与 New API 计费分组或前端资源范围混用。

### [033] 2026-09-20 接手整改阶段 F2：专用渠道配方覆盖

**做了什么**：继续复用 New API 内置异步任务插件，以服务端拥有的稳定配方安全开放 8 种视频供应商；固定底座任务类型和插件身份，模型抓取读取插件声明，前端只呈现用户需要的能力、地址、凭据格式和后续操作。

**涉及模块**：自助渠道配方目录、任务插件配置、模型抓取、凭据校验、分享渠道弹窗、i18n、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/api/share.go`、`share_test.go` | 视频任务配方 | 新增阿里百炼、可灵、Vidu、豆包 Seedance、Gemini Veo、OpenAI Sora、海螺、即梦；服务端固定 `ChannelTypeTaskPlugin` 与插件键；目录只返回可用配方且不泄露底座类型/插件身份；可灵/即梦校验成对凭据或 `sk-` 中转 Key；模型抓取读取插件声明 |
| `new-api/web/src/features/share/**` | 分享渠道弹窗 | 展示异步任务后续步骤和成对 Key 格式，保持现有 Dialog、Field、Select 与 Input 组件及响应式布局 |
| `new-api/web/src/i18n/locales/*.json` | 国际化 | 七种语言同步异步任务和凭据格式提示 |
| `deploy/patches/0166-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、差异统计、阶段 F2 证据和可重建补丁链 |

**验证**：后端配方目录、插件身份隐藏、错误凭据零写入、原子创建和声明模型抓取测试 ✅；SQLite、PostgreSQL、MySQL 自助渠道生命周期矩阵 ✅；前端 share/access 8 文件 26 项 Vitest、typecheck、变更文件 oxlint/oxfmt、Rsbuild 生产构建 ✅；`go test ./... -count=1 -timeout=10m` 明确退出 0，`GOWORK=off relaykit go test ./...` ✅；F2 候选 `/api/status` 200 ✅；390×844 与 1440×1000 选择 Kling 后均显示视频能力、异步流程、默认地址和 Key 格式，两种视口无横向溢出、console error 0 ✅；166 个补丁重建 tree `7169a6cdd134c0c24431f8788a5a04e5ac0d74e5` 与 fork HEAD 一致；inner commit `835492d9c0cb29d593e6d3721e59d8922f80e46b`。

**剩余风险**：当前证据没有使用八家供应商的真实账号逐一提交和轮询任务，不能替代真实第三方兼容性；也没有完成生产多实例、持续压测、正式安全验收或完整设备/无障碍审计。Vertex AI 仍因服务账号、项目和区域等额外配置保持关闭；PostgreSQL 历史渠道 `model_mapping` 中的非法 `\\:` 转义仍是既有数据债。

### [032] 2026-09-20 接手整改阶段 F1：安全渠道配方与邀请控件收口

**做了什么**：把普通分享者的渠道创建入口收口为服务端拥有的安全配方目录；服务端按稳定配方 ID 重新解析底座渠道类型并保持三对象原子创建。邀请卡改用项目 Select，移动端改为纵向信息卡，并移除已终态邀请码的重复撤销动作。

**涉及模块**：自助渠道配方目录、分享渠道创建、邀请发放与列表、移动/桌面响应式、i18n、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/api/share.go`、`share_test.go` | 渠道配方 | 新增配方目录；普通用户只提交 `recipe_id`；服务端重新解析类型，拒绝未知配方与伪造协议，保留底座渠道/APIGX 渠道/初始加密 Key 同事务创建 |
| `new-api/web/src/features/share/**` | 分享渠道弹窗 | 从服务端加载配方，处理加载/失败重试/空目录/失效配方；显示能力提示和 Base URL 要求；“New API 实例”给出明确用途说明 |
| `new-api/web/src/features/access/**` | 邀请控件 | 原生选择器替换为项目 Select；移动端卡片、桌面表格使用同一数据；终态邀请不再显示撤销动作 |
| `new-api/web/src/i18n/locales/*.json` | 国际化 | 七种语言同步上游类型、配方加载状态和 New API 实例说明 |
| `deploy/patches/0165-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、差异统计、阶段 F1 证据和可重建补丁链 |

**验证**：后端配方目录/伪造协议/未知配方零写入/原子创建测试 ✅；SQLite、PostgreSQL 18.4、MySQL 8.0.12 自助渠道生命周期矩阵 ✅；前端 8 文件 25 项 Vitest、typecheck、变更文件 oxlint/oxfmt、Rsbuild 生产构建 ✅；`go test ./... -count=1 -timeout=10m` 与 `GOWORK=off relaykit go test ./...` ✅；390×844 移动邀请卡与 1440×1000 桌面表格均无横向溢出、console error 0 ✅；165 个补丁重建 tree `c615b2cc9202676457a5ee4b9c1052a63087666a` 与 fork HEAD 一致；inner commit `448809088784efe84bd52b4029c36b07318607ed`。

**剩余风险**：当前四种安全配方不是六类真实第三方全覆盖；视频等专用任务型渠道仍需按底座真实必填设置逐项开放。现有证据不能替代生产多实例、持续压测或正式安全验收；PostgreSQL 历史渠道 `model_mapping` 中的非法 `\\:` 转义仍是既有数据债。

### [031] 2026-09-20 接手整改阶段 E：三角色前端任务流重做

**做了什么**：按分享者、使用者和管理员的真实操作顺序重组 APIGX 前端；补齐分享渠道的多 Key、模型、本地价格、容量与审核工作台，把模型广场来源选择直接写入平台 Key，并让管理员从待审核资源和资源选择器完成价格、别名与发布操作。

**涉及模块**：分享者控制台、模型广场与来源偏好、平台 Key、管理员运营台、公开资源 ID、移动端操作卡片、i18n、补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/web/src/features/share/**` | 分享者任务流 | 分组主层级与渠道就绪顺序；多 Key 新增/轮换/启停/删除/权重；模型抓取/手填结果；本地价格预览确认；容量与审核动作 |
| `new-api/web/src/features/pricing/**`、`features/keys/**` | 使用者任务流 | 模型详情展示分享来源；选择综合自动、质量策略或公开 Offering ID 指定来源，并写入目标平台 Key |
| `new-api/web/src/features/apigx-platform/**` | 管理员任务流 | 待审核队列、资源选择器、价格候选/确认、别名与发布；桌面表格和移动操作卡片使用同一动作集合 |
| `new-api/apigx/api/share.go`、`apigx/model/repo_{offering,plaza,upstream}.go` | 前端契约 | 返回公开资源 ID、审核所需上下文、Key 权重更新和可选择资源，避免主流程暴露内部数字 ID |
| `new-api/web/src/i18n/locales/*.json` | 国际化 | 七种语言同步阶段 E 状态、动作、反馈和错误文案 |
| `deploy/patches/0164-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、差异统计、阶段 E 证据和可重建补丁链 |

**验证**：TypeScript typecheck ✅；本次变更 TS/TSX oxlint 与 31 个变更文件 oxfmt ✅；阶段 E 11 个测试文件 42 项 Vitest ✅；Rsbuild 生产构建与嵌入式 Go 二进制构建 ✅；`go test ./apigx/api ./apigx/model -count=1` 与 `go test ./... -count=1 -timeout=10m` ✅；SQLite 3.50.4 / PostgreSQL 18.4 / MySQL 8.0.12 三库矩阵 ✅；390×844 管理员审核和模型候选操作卡片完整，`scrollWidth=clientWidth=390`、console error 0 ✅；164 个补丁重建 tree `a6e0f700ecd4b4d2be1ce859997fe6e9b5fdf778` 与 fork HEAD 一致；inner commit `d6d5b7faab8e803bb11f0c6ddd1fa81514fb7875`。

**剩余风险**：当前证据仍来自本地数据库、可控上游替身和有限浏览器视口，不能替代真实第三方全覆盖、生产多实例、持续压测或正式安全验收；邀请卡仍有旧原生选择器，自助渠道配方仍只开放 OpenAI/Anthropic/Gemini；既有渠道的非法 `model_mapping` 转义数据仍需单独治理。

### [030] 2026-09-19 接手整改阶段 D：价格匹配与六类能力统一计量

**做了什么**：把抓取模型后的价格匹配收口到 New API 当前本地价格配置，以服务端候选令牌确认不可变版本；让同步与异步的六类能力统一写入真实数量、单位、可信度与冻结成本，并把分享者用量视图改为从有效事件计算净值。

**涉及模块**：本地价格匹配、价格版本、同步/异步用量事件、图片/语音/视频事实采集、分享者聚合与用量卡、三库/HTTP/补丁交付

**改动明细**：
| 文件 | 模块 | 改动说明 |
|---|---|---|
| `new-api/apigx/price/match.go`、`apigx/api/share.go` | 价格匹配 | 只读 New API 本地按次价、倍率价与计价表达式；精确/已确认别名匹配；服务端候选令牌、过期拒绝、人工锁价 |
| `new-api/apigx/model/usage_event.go`、`apigx/relayhook/usage_record.go` | 统一计量 | 十二种维度、八种单位、可信度、不可变价格/汇率快照、追加校正和幂等事件 |
| `new-api/relay/**`、`service/**`、`plugins/tasks/kling/plugin.js` | 六类事实 | 排除图片假 token；透传 STT/TTS 真实时长；Kling 终态提取秒数；视频查询不重新选路；异步任务冻结模型身份 |
| `new-api/apigx/model/repo_owner_usage.go`、`web/src/features/share/**` | 分享者视图 | 按渠道/Key/模型聚合净用量，区分已定价、待汇率、未定价、明确零价及不可得计量 |
| `deploy/patches/0163-*`、`deploy/base.lock.json`、`docs/**` | 交付 | 固化 fork HEAD、价格来源决定、阶段 D 证据和可重建补丁链 |

**验证**：阶段 D HTTP 20/20 ✅；SQLite 3.50.4 / PostgreSQL 18.4 / MySQL 5.7.26（InnoDB）矩阵 ✅；`go test ./... -count=1 -timeout=10m` ✅；Windows HTTP/2 GOAWAY 与断路器缓存各重复 20 次 ✅；`relaykit` 在 `GOWORK=off` 下独立测试 ✅；前端生产构建、typecheck、分享用量卡与 i18n 定向用例 ✅；最终候选二进制 `/api/status` 200 ✅；163 个补丁重建 tree `8910ff2ef81e31f8dd5e19fd8112b0c4207c416d` 与 fork HEAD 一致；inner commit `6c927799038205180d4fcfd683fcf86c4a11bf68`。

**剩余风险**：验证使用本地 PostgreSQL/Redis 与可控替身，不是生产多实例、持续压测、正式安全测试或六类真实第三方全覆盖；现有分享者用量卡已按后端契约修正，但完整三角色前端任务流、移动端与真实浏览器验收仍属阶段 E；既有渠道的非法 `model_mapping` 转义数据仍需单独治理。

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
