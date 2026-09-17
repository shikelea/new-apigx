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
- [x] T1.1 平台 Key 接入（按底座现状：明文存储 + 等值查找；用 `authz.SecretView` 约束可见性；明文不进日志）→ `docs/stage1/platform-key-notes.md`（待补）；验收脚本 `tools/stage1-verify/platform-key.mjs`
  - [x] **按 TD-07 沿用底座现状**（明文 `tokens.key` + 等值查找），这是**有意识记录的偏离**，不是缺陷；本次工作包要证明的是"这个决定没有带来额外风险面"。
  - [x] **端到端验收 7/7**：① 等值查找可用（明文直接用于真实请求，HTTP 200）；② 所有者可读明文（48 字符）；③ **他人拿不到明文**；④ 拒绝响应**不含明文**；⑤ **越权响应与「不存在」的响应逐字节一致**（不泄露存在性）；⑥ 他人令牌列表不含该令牌。
  - [x] **直接查库核对**：`tokens.key` 确实等于明文（属预期）；**`logs` 表中明文出现次数 = 0**（明文不进日志 ✓）。
  - ✅ **重要发现（底座约定，与 T1.12 相关）**：底座的旧接口在失败时返回 **HTTP 200 + `{"success":false,"message":"record not found"}`**，而不是 4xx（`POST /api/token/:id/key` 越权时即如此）。归属隔离本身是**正确**的（`model.GetTokenByIds(id, userId)` 按 owner 过滤），但它**不符合计划书 §9.1 的错误码约定**。结论：**我们自己的接口（`apigx/**`）继续用规范状态码**（404/401/429/503），底座旧接口的状态码问题在 T1.12 里统一登记与处理。
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
- [x] T1.5 多 Key 导入与池策略（稳定凭据 ID、逐 Key 状态与统计）—— **已完成并端到端验收（7/7）**
  - ⚠️ **复用修正（2026-09-16，用户质询后核查）**：底座**已有**完整多 Key 机制，我先前写的选择器属于重复实现，**已删除**。
    - 底座已有：轮询/随机选择（`model/channel.go` 的 `GetNextEnabledKey`，`constant/multi_key_mode.go`）、逐 Key 禁用状态与原因/时间（`ChannelInfo.MultiKeyStatusList`）、单 Key 故障隔离与「全失效才停渠道」（`handlerMultiKeyUpdate`）、**批量导入**（`controller/channel.go` 渠道新增/编辑路径内联）、逐 Key 管理接口（`ManageMultiKeys`）。
    - 更重要的是：底座在 `SetupContextForSelectedChannel` 里已完成**唯一的选路决定**（计划书 §9.2）。APIGX 侧再写选择器会制造第二个决定点，并与底座轮询游标互相竞争。**选择一律交给底座。**
    - 保留的仅剩：`ParseImportedKeys`（底座批量导入是控制器内联逻辑，无法作为库复用；已对齐其「去空白/丢空行」行为，并额外做批内去重与行号回显），以及指纹查重（密文带随机 nonce 不可比对）。
  - ✅ **裁决（用户，2026-09-16）**：采用方案①——**阶段 1 只提供轮询/随机**（直接用底座现成能力），加权与剩余容量推迟到有真实需求时；**不为它扩展底座**。
  - 🔒 **用户强调并要求记死**：本项目是**二开**，**不造轮子**。已把它写成 `CODE_STYLE.md` 第 25 条（最高优先级）：动手前必须先 grep 底座；列出「底座已有能力」对照表与「三条同时成立才允许自己写」的判定标准；并把本次 T1.5 重复实现作为反面案例永久记录。
  - [x] **投影已实现**（`apigx/channelpool`）：把分享者的 Key 池解密后按 `(weight DESC, id ASC)` **确定顺序**写进底座渠道的多 Key 列表，并调用**底座的 `channel.Update()`** 完成写入与规范化。测试 5/5：写入与顺序正确、只投影可用 Key、**重投影必须清空按索引存放的旧状态**（否则旧禁用状态会套到别的凭据上）、整池无可用凭据报错、单把解不开时跳过而不拖垮整池。
  - [x] 两个复用要点已记录：① 底座 `Update()` 用的是**全局 `basemodel.DB`**（生产由启动路径设置，测试须显式指向同一库）；② 它还会同步 **abilities 路由能力表**——这正是复用底座写路径的好处，路由不会与渠道状态脱节。
  - [x] **逐 Key 统计回填（数据层）**：`apigx/channelpool/outcome.go` 的 `RecordKeyOutcome` —— 底座的运行期只提供 Key **索引**（`ContextKeyChannelMultiKeyIndex`），这里用投影把索引翻译回 `StableKeyID` 再落库（轮换/增删/改权重都会改变索引对应关系，不翻译就会张冠李戴）。实现：成功记 `LastOKAt`；失败记 `LastErrorAt` + 脱敏 `LastErrorCode`；**鉴权失效只把该 Key 标为 invalid（Key 级隔离），不动渠道与其它 Key**；可设 Key 级冷却。测试 5/5（另有投影 5/5，共 10/10）。
  - [x] **请求路径接入（失败路径）**：`apigx/channelpool/key_stats.go` 的 `RecordFailureFromContext`，挂在底座 `service.ProcessChannelError`（台账 D-014）。**默认关闭**（`APIGX_KEY_STATS_ENABLED`）。四条设计约束：关闭时零查询；只对多 Key 渠道生效（上下文无索引即跳过）；只记脱敏状态码+错误码（不记凭据/上游原文）；**统计失败绝不影响请求**。**401/403 才判为凭据失效**——400 之类业务错误不得停掉一把好 Key（有专门测试守这条）。测试 13/13。
  - [x] 修掉一个自己埋的坑：`dbProvider` 初版默认返回 nil（测试里方便），会导致**生产静默失效**；已改为默认接 `apigxmodel.DefaultDB()`（迁移时捕获的主库句柄）。
  - [x] ⚠️ **补掉一个真设计缺口**：`RecordKeyOutcome` 起初**只改我们表里的状态，没有告诉底座**——而底座是按它自己按下标的状态表决定跳过与否的，因此「Key 级隔离」实际**没有生效**（失效的凭据仍会被继续使用）。已改为失效时**同时调用底座的 `UpdateChannelStatus`**（仍是复用：它按凭据字符串换算下标、记录原因与时间，并且**只在全部 Key 都不可用时才停掉整个渠道**）。为此投影新增 `IndexToKeyValue`（只在进程内传递，禁止落日志/持久化）。
  - [x] **关键验证**（`TestAuthFailureMakesBaseSkipTheFailedKey`）：记一次 401 后，用**底座自己的 `GetNextEnabledKey()`** 查询 → 确实跳过失效索引、返回另一把凭据。这条证明的是「我们的统计 → 底座真实选择行为」的完整链路。
  - [x] **投影触发接口**：`POST /api/apigx/channels/:id/apply`（此前投影没有任何触发入口，功能上是半截的）。设计上刻意做成**显式动作**而非每次改 Key 自动发生：投影会重写底座 Key 列表并清空其按下标的状态表，自动执行容易在分享者没预期时改变行为。空池返回 **409 `no_usable_key`**（渠道将不再接收新请求，必须让分享者看到）；越权 404；响应只回数量，绝不带凭据。测试 3/3（接口层共 11/11）。
  - [x] **成功路径统计**：`RecordSuccessFromContext`，复用底座**已有的成功点**（`Distributor` 里 `c.Next()` 之后、状态 < 400 的分支，底座自己在那里记渠道亲和性），不新增钩子位置（台账 D-015）。此前只有失败路径，`LastOKAt` 永远是 0，分享者看不出哪把 Key 好用。
  - [x] 🐛 **又抓到一个自己写的真 bug**：`RecordKeyOutcome` 原先复用 `ProjectKeyPool`，而它会**写底座渠道并清空按下标的状态表与轮询游标**——等于**每次记录统计都把底座的 Key 健康状态抹掉一次**（既慢又错）。已拆成 `buildPool`（只读）/ `BuildKeyPoolProjection`（只读导出）/ `writePoolToBaseChannel`（唯一写底座处），统计只走只读路径；并加回归测试：记录统计后底座的 `MultiKeyStatusList` 与 `MultiKeyPollingIndex` 必须原封不动。
  - [x] **HTTP 层端到端验证已建脚本并跑过**（`tools/stage1-verify/key-isolation.mjs`，走真实网关 + 替身）：链路本身通了（apply 200、上游 401 → 客户端 401、上游恢复 → 200、响应不含凭据），**但暴露一个真缺陷**（见下）。
  - [x] 🐛 **端到端发现的真缺陷：`apply` 只写库，对运行中的网关不即时生效**。证据：投影确实落库（底座渠道 `is_multi_key=true, multi_key_size=2, keys=2`），但**两把 Key 的 `last_ok_at`/`last_error_at` 全为 0**——说明两个统计钩子一次都没执行。原因：网关走**内存渠道缓存**（`SYNC_FREQUENCY=60s`），请求发出时缓存里仍是投影前的旧渠道（`is_multi_key=false`、单把占位 Key），于是 `ContextKeyChannelIsMultiKey` 为假、钩子按设计跳过；也就是说那一轮的请求**根本没用上我们投影的 Key**。
    - 影响：分享者点「应用」后最长 60 秒内实际不生效（"已生效"是假的）；在这个窗口里逐 Key 统计与隔离针对的也不是真正被使用的凭据。
    - [x] **已修**：`apply` 成功后调用底座的 `CacheUpdateChannel(&baseChannel)` 刷新内存缓存（复用底座自己的缓存函数）。**这是端到端才能发现的缺陷**——单测里没有缓存，永远暴露不出来。
    - [x] **修完重跑端到端：7/7 全过**。关键观察：被 401 拒的那把 Key `status=2(invalid)/usable=false`，另一把 `status=1/usable=true`，第二次请求 200——**Key 级故障隔离在真实请求路径上生效，且渠道没有整体停摆**。
    - 📌 端到端脚本踩到的两个**测试设计**问题（记下来省得再犯）：① 底座在**多个同模型渠道**之间选择，必须用**本次独占的模型名**才能保证请求走自己的渠道；② 新建渠道的**模型→渠道路由能力缓存在网关内存**（默认 60s 同步），能力行确实已写入 `abilities` 表，因此脚本改为**轮询等待可路由**而不是固定 sleep。
  - [x] ❗ **开放项已定位根因（端到端 + 网关日志）**：成功与失败两条统计**都**报了同一个错——
    `channelpool: record key success/failure failed: object not found: key index 1 not in current projection of channel 10`。
    **根因：Key 索引空间在「隔离」之后发生分歧。**
    - 底座侧：索引 0 被我们隔离 → 它继续用**索引 1**（另一把 Key）；
    - 我们侧：重建投影时 `AvailableUpstreamKeys` 会**过滤掉已失效的 Key**，于是另一把 Key 变成**索引 0**，投影里根本没有索引 1。
    - 我们按设计**拒绝猜测索引**（日志报错而不是写错行）——行为是安全的，但后果是**统计漏记**。
    - 更危险的一层：如果两套索引**碰巧对齐**（例如失效的是最后一把），统计会被**静默记到错误的凭据上**。因此这不是「偶发漏记」，而是**索引映射方法的缺陷**。
  - [x] **索引映射已按底座实际 Key 列表重做**（`MapBaseKeyIndexes`）：以底座渠道当前 Key 列表为唯一事实来源、**按凭据明文逐位匹配**（不依赖顺序），因此隔离/删除/重排之后任何索引都能翻译。端到端确证：两把 Key 的失败统计**都**记上了（旧实现会丢掉索引 1 那条）。测试 17/17（新增回归测试 `TestRecordKeyOutcomeAfterIsolationStillMapsCorrectly`）。
  - [x] 顺带补上「**渠道自动恢复**」：全池失效时底座会停掉整个渠道（`status=3` 自动禁用），此后 apply 不会恢复它 → 出现「池里有可用 Key、渠道却仍停摆」。现已在 apply 时把**自动禁用**的渠道恢复为启用（**不动人工禁用**的，那是管理员的决定）。已确认有效：端到端后 `channel_status=1`。
  - [x] **已修并端到端验证通过（7/7）**。根因经读底座源码确认：`CacheUpdateChannelStatus` 在渠道被置为非启用时会把该渠道从 `group2model2channels`（模型→渠道路由索引）里**删掉**，而**重新启用时不会加回来**——所以只改状态 + `CacheUpdateChannel` 会出现「库里一切正常、请求却 no available channel」。
    - 修法：恢复自动禁用的渠道后调用底座的 **`InitChannelCache()`** 重建路由索引（复用底座入口，不自己维护那张索引表）。
    - **端到端最终结果 7/7**：上游 401 → 该 Key `status=2(invalid)/last_err` 记录；恢复另一把并 apply → 渠道自动恢复 + 重新纳入路由 → 第二次请求 **200**；统计完整（`last_ok` 与 `last_err` 都在）；接口响应不含凭据。
  - [x] **投影映射的进程内缓存已完成**（`mapping_cache.go`）：TTL 30s + `apply` 时显式失效。必要性不只是性能——`MapBaseKeyIndexes` 要解密该渠道的**全部凭据**，而它被逐 Key 统计（每次请求结束）调用；缓存避免热路径反复解密、也让明文不必反复出现在内存里。安全权衡已记录在代码注释：缓存含投影明文，故 TTL 取短、仅在池子/渠道状态变化时失效、绝不写日志或持久化。测试 19/19（新增缓存命中与失效、缓存路径下索引翻译一致性）。
  - 📌 端到端脚本也已修正一处**测试假设错误**：底座在 401 后会**用另一把 Key 重试**，所以「上游对一切返回 401」会把池里每把都合法标记失效、整池停摆（正确行为）。脚本已改为 401 阶段后**显式恢复一把 Key 再 apply**，以验证「隔离后仍能继续服务」。
  - [ ] **修法（下一轮）**：索引映射必须基于**底座渠道实际的 Key 列表**（`baseChannel.GetKeys()`）来构建，而不是基于「当前可用的 Key」。做法：读底座渠道的 Key 列表 → 逐位与我们渠道的凭据（解密后）匹配 → 得到 index→StableKeyID。这样底座用哪个索引都能翻译，且天然与真实状态一致（单一事实来源）。
  - [ ] 待做：① **`apply` 刷新底座渠道缓存**（上一行，必须做）② 修完后重跑端到端直到隔离真正生效 ③ 投影映射加进程内缓存（现每次回填都重算：读池 + 解密 + 读底座渠道；失效点应挂在 `apply` 上）。
  - ⚠️ **已知残留风险（有意接受）**：底座 `channels.key` 是明文，投影后凭据会以明文出现在底座表中。我们自己的存储是加密信封；要消除这份明文投影必须改底座 relay 的取键路径，超出阶段 1 范围。
  - 📌 **T1.5 正确实现路径（复用底座，勿再自写）**：
    - 写入：构造底座 `model.Channel`（`Key` = 解密后按固定顺序换行拼接；`ChannelInfo.IsMultiKey=true`、`MultiKeySize=N`、`MultiKeyMode`）后调用 **`channel.Update()`**（其内部会重算 `MultiKeySize`、裁剪越界的状态表）；
    - 选择：**`channel.GetNextEnabledKey()`**（轮询游标线程安全，由 `GetChannelPollingLock` 保护）；
    - 故障隔离：**`model.UpdateChannelStatus()`**（逐 Key 禁用并记录原因/时间，全部失效才停渠道）；
    - 逐 Key 统计回填：底座把本次使用的 Key 索引写进上下文（`ContextKeyChannelMultiKeyIndex`），我们据此把统计落到对应的 `StableKeyID`。
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
  - [x] **上游 Key 与共享账户池容量维度已完成并端到端验收（4/4）**：`channelpool.BuildKeyCapacityDimensions` 把底座给出的 Key 索引翻译成我们的凭据身份（走映射缓存），产出 `upkey`（单把凭据）与 `upaccount`（共享账户池）两个容量维度；账户池维度只在凭据**确实声明归属**某池时施加（共享账户由分享者显式声明，平台不替它推断）。
    - **端到端**：同一渠道下**两把不同的 Key** 声明属于**同一账户池**、池上限=1、**渠道不设限**、并发 3 → **200×1 + 429×2，上游只收到 1 次**。这正是计划书 §9.2 的核心要求：**不能因为切换模型或 Key 就叠加容量**。
    - 🐛 **本轮又抓到自己写的一个真 bug**：`Acquire` 里「所有维度都未配置就提前返回」的判断**排在新增维度拼接之前**，导致「只配账户池上限、不配渠道/令牌/用户上限」时新维度**永远不生效**（端到端第一次跑就是 3 个全过）。已把判断移到全部维度拼接之后，并加注释说明。
  - [x] **上限来源改为策略库并端到端验收（4/4）**：新增 `apigx/model/repo_policy.go`（按主键/按范围读取 `apigx_capacity_policies`，**只透传四态原始值**，解释权统一归 `capacity.Resolve`）+ `channelpool/policy_source.go`（策略链 = 渠道策略 → 环境变量兜底，带 30s 缓存）+ 接口 `PUT /api/apigx/channels/:id/capacity-policy`。
    - **端到端（`tools/stage1-verify/channel-policy-limit.mjs`，不设任何环境变量上限）**：策略「明确值 1」→ 并发 3 放行 1（200×1+429×2）；策略「明确不限」→ 全部放行；策略「未配置」→ 全部放行（过渡行为）。三者行为可区分，证明四态没有被折叠成"有值/无值"两种。
    - 🐛 **本轮又抓到两个自己写的真 bug**：① 策略接口只写策略行、**没把策略挂到渠道上**（`apigx_channels.capacity_policy_id` 仍为 0），读取侧按 FK 找不到策略 → 上限静默不生效；② 重启网关时**旧进程仍占着 3000 端口**，新进程 `bind` 失败退出，导致第一轮验证打的是旧进程（`apigx` 改了代码却"没生效"）。两条都已记录，后者是**环境陷阱**：以后重启必须先确认端口释放。
  - [x] **容量租约改为「按每次尝试」获取，并借底座既有重选机制**（台账 D-016）：底座的失败重试会**重新选择渠道**并再次经过 `SetupContextForSelectedChannel`（源码确认：`controller/relay.go` 的 `getChannel` 在重试时调 `service.CacheGetRandomSatisfiedChannel`）。因此把容量预留从 `Distribute` 移到该函数内、每次尝试都按**当次选中的渠道与凭据**重新预留，并在进入新尝试前释放上一次的租约。
    - 修掉的真缺陷：原先租约只在请求开始时取一次 → **重试换到别的渠道后，租约还挂在旧渠道上，新渠道根本没做容量检查**。
    - 顺带说明：**候选重选不需要我们自己写循环**——底座的重试循环已经是「重新选渠道」的实现，我们只需保证每次尝试都预留。
    - 验证：策略库渠道上限 4/4、账户池上限 4/4 均在新位置下通过（重构位置属于高风险改动，故两条链路都重跑）。
  - [x] **对账（reconciliation）已完成**：`apigx/model/repo_attempt.go`（出站尝试持久化：`BeginAttempt`/`CompleteAttempt`/`ListStaleAttempts`/`MarkAttemptAbandoned`）+ `apigx/channelpool/reconcile.go`（`ReconcileStaleAttempts`）。执行顺序按计划书 §9.2：**先持久记录出站尝试，再发送**；进程中途退出后，凭这条记录**主动、精确（按 `AttemptUID` 释放）、留痕（标记遗弃）**地回收容量。
    - **为什么不能只靠租约 TTL**：TTL 是被动回收（最长等到 90s），且过期不留痕迹、事后无法回答"当时到底有没有发出去"。
    - 接线：`AcquireForAttempt` 取到租约即落库尝试；`ReleaseFromContext` 先记完成状态再释放（顺序反了会让正常完成的尝试被误判为遗弃）。
    - **宽限期 30 分钟**：必须远大于正常请求时长，否则会把正在跑的长流误判为遗弃。有专门用例守这条（未陈旧的尝试不得被对账）。
    - 测试通过：陈旧 → 容量被释放 + 留遗弃痕迹 + 再次对账不重复处理；未陈旧 → 不动；完成状态幂等（重复调用不覆盖已有结果）。
    - 📌 过渡做法（已记录在代码注释）：`apigx_attempts.request_id` 本应指向 `apigx_requests` 行号，但阶段 1 尚未持久化请求意图；而 `(request_id, attempt_index)` 上有唯一索引，一律填 0 会互相冲突，故用 `AttemptUID` 的 FNV-64 派生（唯一且确定）。等 `apigx_requests` 落库后应改为真实行号。
  - [x] **RPM 窗口维度（已完成，端到端 3/3）**——串行 3 次 → `200,200,429`（带 Retry-After）。**串行**是关键：它不可能被并发维度拦住，因此这条证据恰好证明**速率计数在窗口内不归还**。
    - [x] `apigx/capacity/window.go`：新增**只增不减**的窗口计数原语 `IncrementWindow`（Lua「先判断再累加」，超限则**不写入**）+ `WindowTTL`。**为什么不能沿用租约模型**：并发槽位请求结束要归还，而速率计数必须在整个窗口内保留——归还的话 RPM 永远数不到 2，限速形同虚设。代价是"准入后失败的请求也占额度"，这对限速是正确语义。
    - [x] 单测 4/4（真实 Redis）：累计到上限后超限且**不计入**、计数随窗口 TTL 自动过期、非法入参被拒、不同分钟落在不同 key。
    - [x] 接线：`APIGX_CHANNEL_RPM_LIMIT` + 渠道策略（`RpmMode/RpmLimit`）策略链；**顺序为先取并发租约、再计 RPM**，RPM 超限时**把刚拿到的并发租约还回去**（避免"没发出去的请求"白占槽位）。
    - [x] ❗ **500 的真因已定位并修复（本轮最重要的发现，是产品缺陷不是测试问题）**：网关日志为 `unsupported protocol scheme ""`，即**转发时渠道 BaseURL 为空**。根因有两层：
      1. `Channel.BaseURL` 是**指针**，`GetBaseURL()` 在指针为 nil 时返回空串；
      2. 我们把准入/容量钩子插在 `SetupContextForSelectedChannel` **中间**——**在设置 `ContextKeyChannelBaseUrl`/`ChannelKey` 之前**。钩子在这些检查中**提前 `return error`**（例如来源被拒但"可回退"时返回可重试错误、不 abort）时，函数提前返回，**BaseURL 与渠道 Key 都没写进上下文**；而 `Distribute` 调用本函数时**忽略返回值**，请求照旧发出去 → 上游 URL 为空 → 500。
      **修法**：把两个钩子整体移到**该函数末尾**（BaseURL/Key 写入之后、`return nil` 之前）。这样无论钩子返回成功还是错误，上下文都是完整可用的。对照验证：此前失败的用量脚本随后恢复 **12/12**。
    - [x] ❗ **第二个同类缺陷**：RPM 的门控写成 `if cfg.ChannelRPMLimit > 0`（只看环境变量），于是**只配渠道策略、不配环境变量时整段被跳过**；修掉后还有第三层：RPM 与并发维度分开处理，而"所有维度都未配置就提前返回"的判断**排在 RPM 解析之前**，导致"只配 RPM、不配并发"时速率限制形同虚设。已把 RPM 解析提到该判断之前，并让空维度判断同时考虑 `hasRPM`。
    - [x] 容量策略接口支持 RPM 维度（`rpm_mode`/`rpm_limit`，同样走四态校验），因此**限额可按渠道配置**，不再依赖进程级开关（这与上一轮记录的"应改为按渠道配置"一致）。
    - 📌 **测试残留问题（本轮排查的触发器）**：上一轮的脚本在复原时只改了 `scope_mode`，**没有清空 `allowed_model_ids`**，导致之后所有其它模型的请求都被拒。脚本已修正为复原时清空全部限制；测试本身也应尽量不留下跨脚本状态。
    - 📌 另需注意：本轮为验证 RPM 把 `APIGX_CHANNEL_RPM_LIMIT=2` 设成了**全局**，它会同时限流其它脚本的请求（对照脚本 `usage-records` 因此失败 2 项）——这**不是**回归证据，但说明该开关目前是进程级而非按渠道配置，后续应改为按渠道/策略配置。
  - [ ] **未完成**：① 候选重选（当前该渠道满载即 429）② **上游 Key / 上游账户池维度**（需渠道→Key/账户映射，仓储层未实现；这是防容量叠加的关键维度）③ RPM/TPM 与预算维度 ④ 上限来源改为 `apigx_capacity_policies` 并替换「未配置=不约束」的过渡行为 ⑤ 对账 ⑥ 异步任务槽位接入
  - 记录：`docs/stage1/capacity-notes.md`
- [x] T1.8 模型获取与供给发布（一键获取 + 手工添加 + 待发布流程）→ 验收脚本 `tools/stage1-verify/fetch-models.mjs`
  - [x] **手工添加 + 发布流程已完成**（`apigx/model/repo_offering.go` + 接口）：供给按「渠道 + 上游模型名」唯一（`OfferingKeyHash` 与 `AliveFlag` 组成墓碑式唯一索引）；**新供给一律从草稿开始**；状态机 草稿 → 待审核 → 已发布/已驳回，**已发布的供给不能被"提交"动作退回待审核**（否则使用者会看到模型突然消失）；能力声明独立存储并递增修订号（底座按模型名前缀硬编码能力，没有"按供给声明"的位置——基线 E4）。
  - [x] 🔒 **审核必须由平台侧完成**：发布/驳回要求 `role >= RoleAdminUser`，普通用户调用返回 **403 `review_required`**（否则"待审核"形同虚设，计划书 §12）。已用测试固定该断言。
  - [x] 顺带把**唯一约束冲突**映射为 **409 `conflict`**（三种方言错误文本不同，按文本匹配），不再落到 500——这是 T1.12 拒绝清单的一部分。
  - [x] 测试：仓储 2 个用例 + 接口 1 个完整生命周期用例（含越权、重复、状态机、审核门槛、删除后可重发）。
  - [x] **一键获取已完成并端到端验收（8/8）**：`POST /api/apigx/channels/:id/fetch-models`（body `{"import":true}` 可一并导入为草稿供给）。复用底座的取模型能力——在 `controller/channel.go` 加 **3 行导出包装** `FetchChannelUpstreamModelIDsForAPIGX`（台账 **D-017**），转调未导出的 `fetchChannelUpstreamModelIDs`（它按渠道类型分派到 40+ 家 provider 适配器；自己重写等于复制这些适配器，违反 CODE_STYLE 第 25 条）。
    - **端到端**：真实从替身上游拉到 3 个模型；预览**不落库**；导入创建 3 条**草稿**供给；**重复导入幂等**（created=0, skipped=3）；他人渠道 → 404。
    - 归属仍由服务端强制：先按 owner 取分享渠道，再用它绑定的底座渠道去拉取。
  - 📌 说明：底座的取模型函数对上游失败只回"获取模型列表失败"，我们对外给 `502 upstream_fetch_failed`（不泄露上游原始报文）。
- [x] T1.9 广场与详情（只启用已完成能力）→ 验收脚本 `tools/stage1-verify/plaza.mjs`
  - [x] **两条硬性可见性判据**：供给必须是**已发布**（草稿/待审核/已驳回都不对外），且所属分享分组默认可见性必须是**公开**。「受限可见」需要 Grant，阶段 1 未实现，因此**不放行**——宁可少展示，也不把没有授权机制的东西暴露出去。
  - [x] **只启用已完成能力**：对外 `capabilities` 只给平台当前真正具备的部分（当前仅 **流式**，由底座中继链路直接支持且已在阶段 0 验证）；分享者**声明但尚未具备**的能力（vision/tools/reasoning）单列在 `unverified_capabilities` 里**如实展示**——既不冒充可用，也不隐瞒。
  - [x] 对外只含与决策相关且不敏感的信息：模型名、来源的对外 public_id、已具备能力、更新时间；**不含**渠道内部 ID、上游地址、凭据、成本价。
  - [x] **真实网关端到端 14/14**：未认证 401；草稿不出现；未发布详情 404（不泄露存在性）；发布后出现；来源可辨识；能力取值正确（streaming=true / vision=false）；声明未具备者单列；详情可取；泄露扫描 4 项全过。
  - 📌 记录一次**断言写错而非代码写错**：能力字段是结构化布尔（无 `omitempty`），因此应断言**取值**而不是"键不存在"——已改正断言并说明原因。
- [x] T1.10 平台 Key 与指定来源（允许模型 / 允许来源 / 固定来源 / 回退）→ 验收脚本 `source-allowlist.mjs` + `pinned-and-models.mjs`
  - [x] **存储层 + 接口已完成**：`apigx/model/repo_apikey.go`（`apigx_api_keys` 承载 APIGX 偏好：范围模式/允许模型/允许来源/固定来源/是否回退；凭据仍由底座 `tokens` 持有，按 TD-07）+ `GET|PUT /api/apigx/platform-keys/:tokenId/preferences`。校验前移：`允许来源`模式必须给出非空列表、`固定来源`模式必须给出 `pinned_offering_id`，否则拒绝落库（避免"看起来配了、实际不生效"）。测试通过。
  - 🐛 **两个真问题（都已修）**：① **磁盘写满**导致链接失败（`not enough space on the disk`，D 盘仅剩 191MB）——已 `go clean -cache` 释放 4.4GB（现 5.3GB）；这是前几轮反复提示的风险，**构建前应先看余量**。② `apigx_api_keys.key_hash` 上有唯一索引，而"偏好行"没有真正的 Key 哈希（凭据在底座），留空会导致**多行空字符串互相冲突**（第二个使用者写偏好即失败）——已改为写入由 `(user_id, token_id)` 推导的**稳定占位哈希**（满足唯一索引 + 保证一令牌一条记录），并在代码注释里写明它不是任何真实凭据的哈希、不用于鉴权。
  - [x] **执行侧（允许来源 + 回退）已完成并端到端验收（5/5）**：`apigx/relayhook/source_guard.go` 挂在 `SetupContextForSelectedChannel`（**每次尝试必经**，与容量预留同处）。语义：未配置偏好或非「允许来源」模式 → 放行；选中的**不是分享渠道** → 拒绝（限制了来源的 Key 不得借道管理员自建渠道）；分享者不在允许列表 → `AllowFallback=true` 返回**可重试**错误让底座重选，否则直接拒绝。偏好带 30s 缓存，**接口写入后立即失效**。
    - **端到端**：某模型仅由分享者 B 的渠道提供；① `allowed=[A]` 不可回退 → 拒绝；② `allowed=[B]` → **200**；③ `allowed=[A]` 可回退 → **仍拒绝**（回退只允许重选，**不能绕过限制**）；④ 复原默认 → 200。
    - 📌 **记录（留给 T1.12）**：拒绝时的对外响应是 `500 upstream error: do request failed`，**语义不准确**（是我们的准入拒绝，不是上游故障）。错误码与状态码的统一属于 T1.12 拒绝清单的范围。
    - 📌 端到端脚本踩到的两个**自测问题**（已修）：① 登录响应里**没有用户 id**，必须另取 `/api/user/self`，否则允许列表写成 `null` —— 测试会"因为错误的原因通过"；② 该脚本的 `login()` 返回对象而调用处仍传 `auth: root`（对象当字符串），导致创建请求未带正确凭据。
  - [x] **允许模型 + 固定来源的执行侧已完成并端到端验收（5/5）**：同一处准入点依次判定三件事——① `允许模型`：Key 配了列表时请求模型必须在列表内；② `固定来源`：范围模式为固定来源时，选中渠道必须正是被固定供给所属渠道；③ `允许来源`。三者共用同一套「可回退 → 让底座重选 / 不可回退 → 403 并中止上下文」的处置逻辑（`denyOrRetry`）。
    - **端到端**：① `allowed_model_ids` 不含本模型 → **403 `model_not_allowed`**；② 含本模型 → **200**；③ 固定来源指向正确供给 → **200**；④ 固定来源不可用 → **403 `pinned_source_unavailable`**；⑤ 复原 → 200。
    - 调用点补充模型名（`SetupContextForSelectedChannel` 自带该参数），未新增挂载点。
  - 📌 小结：T1.10 的四个偏好（允许模型 / 允许来源 / 固定来源 / 回退）**全部落地并有端到端证据**。
- [x] T1.11 用量记录（使用者视角：请求、模型、实际渠道、耗时、估算成本）→ 验收脚本 `tools/stage1-verify/usage-records.mjs`
  - [x] **端到端 12/12**：真实请求后，使用者能在自己的用量里看到 模型 / token 数 / 估算成本 / 耗时 / **实际来源**（把底座的 `channel_id` 翻译成对外的 `channel_public_id` 与所有者，`kind=share`；非分享渠道标 `kind=platform`）；**只看到自己的记录**（他人列表不含本次 request_id）；泄露扫描（上游地址 / 凭据 / 堆栈）全部未出现。
  - [x] **复用底座**：查询直接用 `model.GetUserLogs`——它已处理分页、过滤与**独立日志库（含 ClickHouse）**分支，自己写查询等于把这些重做一遍（CODE_STYLE 第 25 条）。金额换算用底座的 `common.QuotaPerUnit`，不自己写死汇率（实测 3450 微美元，与阶段 0 的计量结论一致）。
  - [x] 📌 **如实暴露精度**：底座日志的 `UseTime` 是**整秒**，因此接口给的是 `elapsed_seconds`；原先换算成"毫秒"会让亚秒请求显示 `0 ms`，属于误导——已改掉。
  - 📌 **环境发现（值得记住）**：底座有「每用户会话数上限」，反复跑 E2E 会累积会话并导致登录返回 **409 `AUTH_SESSION_LIMIT`**（本次发现用户 1 已累积 50 条）。处理：脚本改为**优先复用缓存会话**；开发环境必要时清理 `user_sessions` 表。
  - [ ] 待做：用量记录的**分页/筛选参数的接口层校验**（当前仅有上限保护）与单元测试（目前由端到端覆盖）。
- [x] T1.12 拒绝清单落地（错误码与不泄露：404 / 401 / 429 / 503）→ 验收脚本 `tools/stage1-verify/rejection-matrix.mjs`
  - [x] **端到端 10/10**：A 未认证 → **401**；B 越权 → **404**，且与「不存在」的响应**逐字节一致**（不泄露存在性）；C 来源不允许 → **403 `source_not_allowed`**；D 容量超限 → **429 + `Retry-After: 1`**；E **泄露扫描**（内部渠道 id / 上游地址 / 凭据 / 堆栈）四项全部未出现。
  - [x] 修掉一个语义错误：来源准入被拒时原先对外是 `500 upstream error: do request failed`（把**我们的准入判断**说成**上游故障**）。现在直接写出 **403 + `source_not_allowed`**，且**中止 gin 上下文**——因为 `Distribute` 调用 `SetupContextForSelectedChannel` 时**忽略返回值**，只返回错误并不能阻止请求继续（容量钩子有效正是因为它直接 abort）。
  - 📌 **诚实记录一次误判**：我曾判断 C/D 的异常是"准入错误被当成渠道故障、导致健康渠道被自动禁用"。补上"等待模型可路由"后 C 得到 403、D 得到 429，**说明主因是我这个脚本漏了可路由轮询**（其他脚本都有）：请求拿到的是底座自己的 `model_not_found 503`（"没有渠道"），根本没走到准入层。中止上下文的改动仍然保留（它修的是"调用方忽略返回值"这个真实弱点），但"会误禁用渠道"的结论**未经证实**。
  - 📌 已知偏离（按设计保留）：底座**旧接口**失败时返回 `HTTP 200 + {success:false}`（如 `POST /api/token/:id/key` 越权）。我们自己的 `apigx/**` 接口一律用规范状态码；底座旧接口不改（改动面大且会影响既有前端）。

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
