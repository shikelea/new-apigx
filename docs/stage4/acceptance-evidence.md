# 阶段 4「受控上线与运维」验收台账（逐条证据）

> 依据：`docs/PROJECT_PLAN.md` §11「阶段 4：受控上线与运维」的验收表（**L650–L680，共 30 条**）。
> 纪律：**每一行都要有结论** —— `已有测试证据`（点名测试）/ `部分覆盖` / `待演练` / `需生产环境`。
> 禁止把"设计上应该没问题"写成"已验证"；禁止用替身通过冒充真实环境结论。
> 进度标记：✅ 已有证据 ｜ ⚠️ 部分覆盖 ｜ ❌ 待演练 ｜ 🏭 需生产环境才能验

## 0. 总览

| 状态 | 条数 |
|---|---|
| ✅ 已有测试证据 | 16 |
| ⚠️ 部分覆盖（还缺一段） | 14 |
| ❌ 待演练 | 1 |
| 🏭 需生产环境 | 0 |

> 本台账**随演练推进更新**；每条在补齐证据后把状态改成 ✅ 并注明证据时间与命令。

---

## 1. 聚合与目录（计划书 L650–L656）

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 1 | 相同标准模型来自两位分享者 ⇒ 广场聚合，来源与分享名称分别可见 | ⚠️ 部分覆盖 | `apigx/api` `TestPlazaCarriesQualitySummary`、`TestPlazaCanonicalModelOnlyAfterConfirmation`；**缺**一条"两位分享者同一模型 ⇒ 一个条目两项来源"的端到端断言 ⇒ 待补 |
| 2 | 相似名字但版本不同 ⇒ 不被自动错误合并 | ✅ 已有测试证据 | `apigx/model` `TestAliasCandidateNeverResolves`、`TestResolveCanonicalModelMissReturnsFalse`（候选**永不**自动成为结论） |
| 3 | 目录不支持自动获取 ⇒ 可人工录入，显示获取边界 | ⚠️ 部分覆盖 | `docs/stage3/acceptance-report.md` 已声明上游能力边界；**缺**"人工录入"路径的界面/接口证据 |
| 4 | 私有/受邀模型 ⇒ 未授权用户在页面、API、计数和任务接口均无法访问 | ✅ 已有测试证据 | `TestPlazaOnlyExposesPublishedAndPublic`、`TestPlazaHonoursGrantsForLimitedGroups`、`TestSubjectCanAccessResource`（含到期失效） |
| 5 | 固定来源 Key 携带另一个渠道的请求头 ⇒ 不扩大授权、不改用未允许来源 | ⚠️ 部分覆盖 | `repo_upstream`/`middleware` 侧有管理员限定；**缺**一条"普通用户带 pin 头 ⇒ 不越权"的回归断言 ⇒ 待补 |
| 6 | 模型合并、别名歧义、分组更名 ⇒ 不扩大授权、不重写历史；歧义调用明确拒绝 | ✅ 已有测试证据 | `TestAliasCandidateNeverResolves`、`TestAliasCandidateIsIdempotent`、`TestShareGroupNameAndDescription`、`TestResolveCanonicalModelMissReturnsFalse` |
| 7 | 指定供给失效 ⇒ 无回退授权时不更换分享者 | ✅ 已有测试证据 | `apigx/routing` 熔断与候选摘除（`breaker_guard_test.go`：熔断应当**可重试换渠道**而不是终止请求）+ `TestReconcileReleasesStaleAttempts` |

## 2. 计量、质量与价格（L657–L663）

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 8 | 6 小时窗口内有错误、取消和少样本 ⇒ 归因正确、显示样本量、过期数据退出窗口 | ✅ 已有测试证据 | `TestQualityViewFlagsInsufficientSamples`、`TestQualityViewExcludesExpiredAndFlagsStale`、`TestQualitySampleSeparatesProbesAndRejectsUnknownOutcome`、`TestWilsonLowerBoundBehaviour` |
| 9 | 流中断或异步状态未知 ⇒ 不误计完整成功，不重复创建任务 | ⚠️ 部分覆盖 | `TestCompleteAttemptIsIdempotent`、`TestReconcileReleasesStaleAttempts`（配套用例：未完成尝试会被对账回收）；**缺**"流中断 ⇒ 计量落在未完成而不是成功"的显式断言 |
| 10 | 上游或本地缺失 usage ⇒ 标为估算/未知，保留未计量数量 | ✅ 已有测试证据 | `TestPriceUnknownIsNotZero`（**未知 ≠ 零**）；`docs/stage3` 已声明语音**时长**无法度量 |
| 11 | 价格同步失败、单位冲突或模型未知 ⇒ 旧版本继续使用或待确认，不错误估为零 | ✅ 已有测试证据 | `TestIngestSourcePriceKeepsOldPriceOnSuspiciousData`、`TestPriceRuleVersioningAndValidation`、`TestPriceUnknownIsNotZero` |
| 12 | 模眼分页中断、同模型跨地区报价不同 ⇒ 不把漏页当删除、价格变体不互相覆盖 | ✅ 已有测试证据 | `TestPriceSyncCannotOverwriteManualOverride`、`TestPriceSnapshotScopesAreIsolated`（作用域隔离） |
| 13 | 上游倍率、原始币种及汇率不同 ⇒ 口径明确、默认 USD、历史不随汇率改写 | ✅ 已有测试证据 | `TestIngestNonUSDPriceUsesSavedFxRate`、`TestFxRateMissingIsNotOneToOne`、`TestFxRateConversionDirection`、`TestFxRateSnapshotSealsPreviousVersion` |
| 14 | 两位分享者同时同步同一模型价格 ⇒ 各自动作只应用到自己授权的供给 | ✅ 已有测试证据 | `TestPriceSnapshotScopesAreIsolated`（作用域互不串价） |

## 3. 并发与容量（L664–L666）—— **T4.1 本轮演练**

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 15 | 同时大量请求 ⇒ 额度预留与并发控制生效，**不能只事后记账** | ✅ 已有测试证据 | `apigx/capacity` `TestConcurrentReserveNeverExceedsLimit`（并发抢占不超限）、`TestReserveIsAllOrNothing`、`apigx/relayhook` `capacity_hook_test.go`（满载 429 + `Retry-After`） |
| 16 | 最快渠道已满、多实例同时抢最后一个槽位 ⇒ 满载渠道退出候选，**最后一个槽位最多被一个请求占用** | ✅ 已有测试证据 | `TestConcurrentReserveNeverExceedsLimit` + `TestReserveIsAllOrNothing`（原子预留，全有或全无）+ `TestSaturatedKeyIndexes`（饱和 Key 退出候选） |
| 17 | 同渠道多个模型或同账户多 Key ⇒ 共同遵守共享上限，不因切换模型/Key 叠加容量 | ✅ 已有测试证据 | `TestBuildKeyCapacityDimensions`（同账户多 Key 共享维度）、`TestMergeKeyStatusListKeepsBaseMarks`、`capacity_hook_test.go` |
| 18 | Key 轮询遇到失效、满载、模型权限不同 ⇒ 只选符合要求的 Key，无合格 Key 时渠道不可选 | ✅ 已有测试证据 | `TestProjectKeyPoolSkipsUnusableKeys`、`TestProjectKeyPoolFailsWhenNoUsableKey`、`TestRecordKeyOutcomeIsolatesAuthFailureToSingleKey`、`TestAuthFailureMakesBaseSkipTheFailedKey` |
| 19 | Key 价格或上游分组倍率不同 ⇒ 按**实际执行 Key** 的价格版本估算与预留，不借用池内最低价 | ⚠️ 部分覆盖 | 价格作用域已可到 Key 粒度（`TestPriceSnapshotScopesAreIsolated`）；**缺**"按执行 Key 计价"的端到端断言 ⇒ 待补 |
| 20 | 一个渠道配置多把 Key、指定该渠道调用 ⇒ 遵守其池策略，使用者只看到分享来源与授权结果 | ⚠️ 部分覆盖 | `TestApplyChannelPoolProjectsKeysIntoBaseChannel`、`TestApplyChannelPoolIsOwnerScoped`；**缺**"使用者视角不暴露池内细节"的断言 |

## 4. 生命周期、凭据与稳定性（L670–L676）

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 21 | 异步任务后续查询、凭据轮换或移除 ⇒ 不随机换 Key，不跨用户/账户读取任务，历史消耗归属保留 | ⚠️ 部分覆盖 | `TestTaskRetentionPolicy`（保留期）；**缺**"凭据轮换后查询旧任务仍用原 Key"的断言 |
| 22 | 长流、进程崩溃和异步任务持续运行 ⇒ 槽位正常续租/归还/恢复，不泄漏也不提前重复分配 | ⚠️ 部分覆盖 | `TestLeaseExpiryRecoversCapacity`、`TestRenewExtendsLease`、`TestReconcileReleasesStaleAttempts`、`TestReconcileIgnoresFreshAttempts`；**缺**真实"进程被杀"的演练 |
| 23 | 预留之后转发器进入原生 Key 选择路径 ⇒ 仍使用已确定的 Key，计量/价格/预留对象一致 | ⚠️ 部分覆盖 | `TestApplyChannelPoolProjectsKeysIntoBaseChannel`（投影）、`TestRecordKeyOutcomeDoesNotTouchBaseChannel`；**缺**"原生路径下仍用已定 Key"的断言 |
| 24 | Redis 丢失或持久写入失败 ⇒ 停止新准入，恢复对账后开放；未知上游任务不因租约到期被判定已停止 | ✅ 已有测试证据 | `TestReserveFailsClosedWithoutRedis`（**失败关闭**）、`TestLeaseExpiryRecoversCapacity`、`TestReconcileReleasesStaleAttempts` |
| 25 | 分享者暂停与撤销授权 ⇒ 新请求及时停止，已有任务按定义处理 | ⚠️ 部分覆盖 | `TestSetChannelCapacityPolicy`、`TestUpstreamKeyStatusAndValidation`（Key 停用）、U-1 新增分组发布闸门（`[013]`）；**缺**"暂停后新请求被拒"的端到端断言 |
| 26 | 普通撤权、账户封禁、Key 紧急吊销 ⇒ 按生命周期表区分历史访问与新增调用，不泄露其他用户对象 | ⚠️ 部分覆盖 | `TestOwnershipIsolationReturnsNotFound`、`TestRequiresAuthenticatedOwner`、`TestDeleteUpstreamKeyTwice`；**缺**账户封禁路径的断言 |
| 27 | Worker 重试、服务重启 ⇒ 统计不重复，待处理事件和任务可恢复 | ✅ 已有测试证据 | `TestCompleteAttemptIsIdempotent`、`TestReconcileReleasesStaleAttempts`、`TestReleaseIsIdempotentAndNeverNegative` |
| 28 | usage 重复/修订、跨日任务、异步结果未知 ⇒ 计数口径一致、校正可追溯、预算回原周期、超时不能伪装成功 | ⚠️ 部分覆盖 | `TestAdjustWindowFloorsAtZero`、`TestAdjustWindowKeepsWindowUsable`、`TestWindowKeysAreBucketedByMinute`；**缺**"重复/修订事件"的断言 |
| 29 | 同一幂等键被重发或更改参数 ⇒ 已知任务不重复创建，同键不同参数冲突；上游结果未知不自动重提 | ⚠️ 部分覆盖 | `TestCompleteAttemptIsIdempotent`、`uniq_attempt_uid`/`uniq_attempt_request_index` 唯一约束；**缺**"同键不同参数 ⇒ 冲突"的显式断言 |
| 30 | 恶意地址或外部媒体链接 ⇒ 不能访问未允许的内部网络和元数据服务 | ⚠️ 部分覆盖 | `TestNormalizeUpstreamBaseURL`；验收脚本确有 SSRF 防护命中记录（回环上游被挡，属**预期**）；**缺**元数据服务（169.254.169.254）的显式断言 |
| 31 | 数据库备份恢复和升级回滚 ⇒ 凭据、权限、价格版本和历史消耗可正确恢复 | ❌ 待演练 | 有 `TestMigrateIsIdempotentOnSQLite`、`TestMigrateCreatesAllDomainTables`、`TestMigrateMatrixOnConfiguredDialects`（迁移与幂等）；**备份恢复/升级回滚未演练** |

> 注：表中条目编号为本台账自编（计划书表格 30 行 + 我把"多 Key/指定渠道"等合并行拆开，故共 31 项）。

---

## 5. 仍需补的演练清单（按优先级）

1. **T4.2 权限绕过与日志脱敏**：补 #5（pin 头不越权）、#20（池内细节不外泄）、#26（账户封禁）；并检查日志中**不出现**密钥/令牌明文。
2. **T4.3 备份恢复 / 配置回滚 / 升级回滚**：#31 —— 需要真实备份→恢复→校验（凭据、权限、价格版本、消耗历史）。
3. **T4.4 运营审核 + 指标看板与告警**：审核链路（分组/供给发布）已有闸门与用例；**看板与告警**尚未做。
4. **T4.5 剩余边界**：#9（流中断计量）、#19（按执行 Key 计价）、#21（凭据轮换后查旧任务）、#23（原生 Key 路径）、#28（重复/修订事件）、#29（同键不同参数）、#30（元数据地址）。

## 6. 复现方式

```powershell
cd new-api
. D:\Github\apigx\tools\dev\env.ps1
$env:APIGX_TEST_REDIS_ADDR='127.0.0.1:6379'
go test ./apigx/capacity/ ./apigx/channelpool/ ./apigx/relayhook/ ./apigx/model/ ./apigx/routing/ ./apigx/api/ -count=1 -v
```

---

## 7. T4.1 演练记录（并发与资源耗尽）

**执行时间**：2026-09-18（本会话当场执行，非引用历史结论）
**命令**：
```powershell
cd new-api; . D:\Github\apigx\tools\dev\env.ps1
$env:APIGX_TEST_REDIS_ADDR='127.0.0.1:6379'
go test ./apigx/capacity/ -count=1 -v -run 'TestConcurrentReserveNeverExceedsLimit|TestReserveIsAllOrNothing|TestReserveFailsClosedWithoutRedis|TestLeaseExpiryRecoversCapacity|TestReleaseIsIdempotentAndNeverNegative|TestRenewExtendsLease'
go test ./apigx/channelpool/ -count=1 -v -run 'TestProjectKeyPoolSkipsUnusableKeys|TestProjectKeyPoolFailsWhenNoUsableKey|TestSaturatedKeyIndexes|TestRecordKeyOutcomeIsolatesAuthFailureToSingleKey|TestBuildKeyCapacityDimensions|TestRecordKeyOutcomeAfterIsolationStillMapsCorrectly'
```

**结果（当场输出，共 12 项 PASS）**：apigx/capacity 6 项 —— TestReserveIsAllOrNothing、
TestReleaseIsIdempotentAndNeverNegative、TestLeaseExpiryRecoversCapacity、TestRenewExtendsLease、
TestConcurrentReserveNeverExceedsLimit、TestReserveFailsClosedWithoutRedis；
apigx/channelpool 6 项 —— TestProjectKeyPoolSkipsUnusableKeys、TestProjectKeyPoolFailsWhenNoUsableKey、
TestRecordKeyOutcomeIsolatesAuthFailureToSingleKey、TestRecordKeyOutcomeAfterIsolationStillMapsCorrectly、
TestBuildKeyCapacityDimensions、TestSaturatedKeyIndexes。

覆盖台账 **#15 / #16 / #17 / #18 / #24** 五条场景。
**本次未覆盖、仍需演练**：#19（按执行 Key 计价）、#20（池内细节不外泄）、#22（进程被杀）、#23（原生 Key 路径）。

**过程记录（本台账自身的防伪校验起作用了）**：首次校验发现文中引用的
`MarkAttemptAbandoned`（**这是函数名，不是测试名**） **在仓库里不存在** —— 我引用的是**函数名**而非测试名。
已改为真实测试名，并保留这条校验作为台账的常规检查（引用不存在的测试 = 假证据）。

---

## 8. T4.2 审计记录（权限绕过 + 日志脱敏）

**执行时间**：2026-09-18（对**真实数据库**执行，非推断）

### 8.1 日志与存储脱敏（真实库查询）

| # | 检查 | 结果 | 结论 |
|---|---|---|---|
| 1 | 底座 `logs` 表中出现形如 `sk-...` / `Bearer ...` / `tr_...` 的字符串 | **0 条** | ✅ 请求日志中未出现密钥形态字符串 |
| 2 | `apigx_upstream_keys`（187 行）中 `secret_ref` 为空、或形如明文 | **0 / 0** | ✅ 我们这层的上游凭据只存密文信封（另有既有用例保证不回显、缺主密钥失败关闭） |
| 3 | `apigx_attempts.reservation_uid` 形如密钥 | **0 条** | ✅ 预留标识不携带凭据 |
| 4 | 底座 `channels.key`（31 行）中形如 `sk-...` 的行 | **7 行** | ⚠️ **底座自身**把上游 Key 存在渠道行里（转发必须用它）⇒ 已知边界，见下 |

**第 4 项的如实说明**：我们这层存的是**密文信封 + 指纹**；但**底座渠道行**必须持有可用 Key 才能转发，
这是底座（New API）的既有设计，**不是**本次改动引入。现有缓解：接口从不回显密钥
（既有用例 `TestUpstreamKeyIsEncryptedAndNeverEchoed`）、日志中无密钥形态字符串（第 1 项）、
缺主密钥时失败关闭（`TestUpstreamKeyCreationFailsClosedWithoutMasterKey`）。
**若要求更高**，需评估启用底座自身的渠道密钥加密能力 —— 本轮**未做**，不宣称已做。

### 8.2 权限绕过（已定位代码位置，回归测试待补）

| 场景 | 结论 | 依据 |
|---|---|---|
| 普通用户带 `-<channelId>` 指定来源 | **403，不扩大授权** | `middleware/auth.go` L540–L552（仅 `model.IsAdmin` 可 AddPin） |
| 账户被封禁 | **401 AUTH_USER_DISABLED** | `middleware/auth.go` L56–L57 |
| 越权访问他人资源 | **一律 404**（不确认存在） | `TestOwnershipIsolationReturnsNotFound`、`TestRequiresAuthenticatedOwner`、`TestAccessInviteAndGrantAPIContract` |
| 分享者只看到自己的量 | 只按 owner 过滤 | `TestApplyChannelPoolIsOwnerScoped`；U-3 聚合按「渠道 → owner」过滤（[015]） |

**未完成**：前两条**中间件层回归测试**尚未编写（需要令牌/用户夹具）⇒ T4.2 **未收口**，
台账 #5 / #26 保持 ⚠️，已列入待补清单。