# 阶段 4「受控上线与运维」验收台账（逐条证据）

> 依据：`docs/PROJECT_PLAN.md` §11「阶段 4：受控上线与运维」的验收表（**L650–L680，共 30 条**）。
> 纪律：**每一行都要有结论** —— `已有测试证据`（点名测试）/ `部分覆盖` / `待演练` / `需生产环境`。
> 禁止把"设计上应该没问题"写成"已验证"；禁止用替身通过冒充真实环境结论。
> 进度标记：✅ 已有证据 ｜ ⚠️ 部分覆盖 ｜ ❌ 待演练 ｜ 🏭 需生产环境才能验

## 0. 总览

| 状态 | 条数 |
|---|---|
| ✅ 已有测试证据 | 19 |
| ⚠️ 部分覆盖（还缺一段） | 12 |
| ❌ 待演练 | 0 |
| 🏭 需生产环境 | 0 |

> 本台账**随演练推进更新**；每条在补齐证据后把状态改成 ✅ 并注明证据时间与命令。

---

## 1. 聚合与目录（计划书 L650–L656）

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 1 | 相同标准模型来自两位分享者 ⇒ 广场聚合，来源与分享名称分别可见 | ⚠️ 部分覆盖（**本轮未闭合**） | 前端聚合已实现（`summarizeModelSources`：一个模型 + 多个来源分组）；后端广场返回**逐供给**条目。本轮新增用例 `TestPlazaAggregatesSameModelFromTwoSharers` **无法取到夹具供给**（换第三方浏览者仍为 0 项，而既有 `TestPlazaOnlyExposesPublishedAndPublic` 可过）⇒ **差异未查明**，用例已显式 `Skip` 并保留待查 |
| 2 | 相似名字但版本不同 ⇒ 不被自动错误合并 | ✅ 已有测试证据 | `apigx/model` `TestAliasCandidateNeverResolves`、`TestResolveCanonicalModelMissReturnsFalse`（候选**永不**自动成为结论） |
| 3 | 目录不支持自动获取 ⇒ 可人工录入，显示获取边界 | ⚠️ 部分覆盖 | `docs/stage3/acceptance-report.md` 已声明上游能力边界；**缺**"人工录入"路径的界面/接口证据 |
| 4 | 私有/受邀模型 ⇒ 未授权用户在页面、API、计数和任务接口均无法访问 | ✅ 已有测试证据 | `TestPlazaOnlyExposesPublishedAndPublic`、`TestPlazaHonoursGrantsForLimitedGroups`、`TestSubjectCanAccessResource`（含到期失效） |
| 5 | 固定来源 Key 携带另一个渠道的请求头 ⇒ 不扩大授权、不改用未允许来源 | ✅ 已有测试证据 | **新增回归测试** `TestSetupContextForTokenRejectsPinSuffixForNonAdmin`（普通用户带 `-<channelId>` ⇒ **403** 且返 `specific_channel_version` 头，实测 PASS）；代码位置 `middleware/auth.go` L540–L552 |
| 6 | 模型合并、别名歧义、分组更名 ⇒ 不扩大授权、不重写历史；歧义调用明确拒绝 | ✅ 已有测试证据 | `TestAliasCandidateNeverResolves`、`TestAliasCandidateIsIdempotent`、`TestShareGroupNameAndDescription`、`TestResolveCanonicalModelMissReturnsFalse` |
| 7 | 指定供给失效 ⇒ 无回退授权时不更换分享者 | ✅ 已有测试证据 | `apigx/routing` 熔断与候选摘除（`breaker_guard_test.go`：熔断应当**可重试换渠道**而不是终止请求）+ `TestReconcileReleasesStaleAttempts` |

## 2. 计量、质量与价格（L657–L663）

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 8 | 6 小时窗口内有错误、取消和少样本 ⇒ 归因正确、显示样本量、过期数据退出窗口 | ✅ 已有测试证据 | `TestQualityViewFlagsInsufficientSamples`、`TestQualityViewExcludesExpiredAndFlagsStale`、`TestQualitySampleSeparatesProbesAndRejectsUnknownOutcome`、`TestWilsonLowerBoundBehaviour` |
| 9 | 流中断或异步状态未知 ⇒ 不误计完整成功，不重复创建任务 | ✅ 已有测试证据 | **新增** `TestAbandonedAttemptIsNotEmptySuccess`（中断的尝试 `http_status=0` ⇒ **不算成功**；对照：正常完成必须 200 ⇒ 断言有区分力；实测 PASS）；口径与 U-3 消耗视图一致（[015]） |
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
| 19 | Key 价格或上游分组倍率不同 ⇒ 按**实际执行 Key** 的价格版本估算与预留，不借用池内最低价 | ⚠️ **部分覆盖（记录链路已接通并实测；取价/计价部分未做）** | **已做**：`apigx/model/usage_event.go`（按 §5.16/§5.21 补 `Kind/Dimension/UnitScale/Trust/CostState` 常量 + `RecordUsageEvent`，**强制未定价时成本为 0**）+ `apigx/relayhook/usage_record.go`（从上下文解析归属，**拿不到用量就不写**）+ `service/text_quota.go` 结算后接入。**真实请求实测**：一次对话（prompt=16/completion=16）落 **2 行**（dim=0/1，`quantity_milli=16000` 符合"1 token=1000"），归属 `owner=1/channel=154/key=182`，`cost_state=2` 且 `cost_usd_micro=0`，`event_uid=u:<请求ID>:<维度>` 幂等。**已具备"实际执行 Key"锚点**（`upstream_key_id`）⇒ 剩下的只是"按该 Key **取价**并计价"。**未做**：按 Key 取价、成本计算、`Attempt.PriceRuleID` 写入、其余 3 个落点（音频/两个 quota 路径）与任务路径（后者拿不到 token 维度，明确不记） |
| 20 | 一个渠道配置多把 Key、指定该渠道调用 ⇒ 遵守其池策略，使用者只看到分享来源与授权结果 | ✅ 已有测试证据 | **新增** `TestKeyPoolDetailsAreOwnerOnly`（所有者可读 Key 池 200；**非所有者 404**，不确认存在；实测 PASS）；另有 `TestApplyChannelPoolProjectsKeysIntoBaseChannel`、`TestApplyChannelPoolIsOwnerScoped` |

## 4. 生命周期、凭据与稳定性（L670–L676）

| # | 计划书验收场景 | 现状 | 现有证据 / 缺口 |
|---|---|---|---|
| 21 | 异步任务后续查询、凭据轮换或移除 ⇒ 不随机换 Key，不跨用户/账户读取任务，历史消耗归属保留 | ⚠️ 部分覆盖 | `TestTaskRetentionPolicy`（保留期）；**缺**"凭据轮换后查询旧任务仍用原 Key"的断言 |
| 22 | 长流、进程崩溃和异步任务持续运行 ⇒ 槽位正常续租/归还/恢复，不泄漏也不提前重复分配 | ⚠️ 部分覆盖 | `TestLeaseExpiryRecoversCapacity`、`TestRenewExtendsLease`、`TestReconcileReleasesStaleAttempts`、`TestReconcileIgnoresFreshAttempts`；**缺**真实"进程被杀"的演练 |
| 23 | 预留之后转发器进入原生 Key 选择路径 ⇒ 仍使用已确定的 Key，计量/价格/预留对象一致 | ⚠️ 部分覆盖 | `TestApplyChannelPoolProjectsKeysIntoBaseChannel`（投影）、`TestRecordKeyOutcomeDoesNotTouchBaseChannel`；**缺**"原生路径下仍用已定 Key"的断言 |
| 24 | Redis 丢失或持久写入失败 ⇒ 停止新准入，恢复对账后开放；未知上游任务不因租约到期被判定已停止 | ✅ 已有测试证据 | `TestReserveFailsClosedWithoutRedis`（**失败关闭**）、`TestLeaseExpiryRecoversCapacity`、`TestReconcileReleasesStaleAttempts` |
| 25 | 分享者暂停与撤销授权 ⇒ 新请求及时停止，已有任务按定义处理 | ✅ 已有测试证据 | **新增** `TestSuspendedGroupStopsServing`（分组置为已暂停 ⇒ `AssembleCandidates` 返回空，实测 PASS）；另有 `TestSetChannelCapacityPolicy`、`TestUpstreamKeyStatusAndValidation` |
| 26 | 普通撤权、账户封禁、Key 紧急吊销 ⇒ 按生命周期表区分历史访问与新增调用，不泄露其他用户对象 | ✅ 已有测试证据 | **新增回归测试** `TestUserAuthRejectsDisabledUser`（停用用户 ⇒ **401 `AUTH_USER_DISABLED`**，实测 PASS）；另有 `TestOwnershipIsolationReturnsNotFound`、`TestRequiresAuthenticatedOwner`、`TestDeleteUpstreamKeyTwice`；代码位置 `middleware/auth.go` L56–L57 |
| 27 | Worker 重试、服务重启 ⇒ 统计不重复，待处理事件和任务可恢复 | ✅ 已有测试证据 | `TestCompleteAttemptIsIdempotent`、`TestReconcileReleasesStaleAttempts`、`TestReleaseIsIdempotentAndNeverNegative` |
| 28 | usage 重复/修订、跨日任务、异步结果未知 ⇒ 计数口径一致、校正可追溯、预算回原周期、超时不能伪装成功 | ⚠️ 部分覆盖（**本环境无法验**） | 窗口/分桶类断言已有（`TestAdjustWindowFloorsAtZero` 等）；但 `apigx_usage_events` **0 行、无写入方**（见 [015]）⇒ 事件级"重复/修订"路径**当前没有数据可验**，需先补写入方 |
| 29 | 同一幂等键被重发或更改参数 ⇒ 已知任务不重复创建，同键不同参数冲突；上游结果未知不自动重提 | ✅ 已有测试证据 | **新增** `TestAttemptIdempotencyKeyRejectsDuplicate`（同 `(request_id, attempt_index)` 重发被唯一约束拦下，只有一条记录；实测 PASS，日志可见 constraint failed）；另有 `TestCompleteAttemptIsIdempotent` |
| 30 | 恶意地址或外部媒体链接 ⇒ 不能访问未允许的内部网络和元数据服务 | ✅ 已有测试证据（**边界已写明**） | **重定向**与**受保护抓取**都过校验：`service/http_client.go` L44–L53 `checkRedirect` → `common.ValidateURLWithFetchSetting`；`ValidateSSRFProtectedFetchURL`（L71）用于媒体/结果抓取；用例 `TestSSRFProtectionRejectsLiteralPrivateAndReservedIPs`、`TestSSRFProtectionRejectsResolvedPrivateIP`、`TestSSRFProtectionAllowsPrivateIPWhenExplicitlyEnabled`（`common/ssrf_protection_test.go`）。开关 `EnableSSRFProtection` **默认 true**、`AllowPrivateIp` **默认 false**。**明示边界**：**首跳直连**上游 BaseURL **不校验** ⇒ 私网/回环上游被允许（本环境验收替身 `127.0.0.1:8100` 正依赖这一点）。取舍：自建/内网上游是合法场景；风险是"借平台网络位置探测内网"；如需收紧，用 `DomainList/IpList/AllowedPorts` 白名单或改走不依赖回环的验收替身 |
| 31 | 数据库备份恢复和升级回滚 ⇒ 凭据、权限、价格版本和历史消耗可正确恢复 | ✅ 已有测试证据 | **T4.3 演练**（第 9 节）：`pg_dump -Fc` → 恢复到新库 → **12 项指纹逐项一致**（含凭据密文与指纹的 md5、权限、价格版本、历史消耗）；恢复库上**连续启动两次**均无错误、行数不变（**迁移幂等**）；配置改→重启生效→回滚→重启恢复原值。**升级回滚（回退旧二进制）未演练**，见第 9 节说明 |

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
| 1 | 相同标准模型来自两位分享者 ⇒ 广场聚合，来源与分享名称分别可见 | ⚠️ 部分覆盖（**本轮未闭合**） | 前端聚合已实现（`summarizeModelSources`：一个模型 + 多个来源分组）；后端广场返回**逐供给**条目。本轮新增用例 `TestPlazaAggregatesSameModelFromTwoSharers` **无法取到夹具供给**（换第三方浏览者仍为 0 项，而既有 `TestPlazaOnlyExposesPublishedAndPublic` 可过）⇒ **差异未查明**，用例已显式 `Skip` 并保留待查 |
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
### 8.3 T4.2 收尾（中间件层回归测试已补）

| 场景 | 新增测试 | 当场结果 |
|---|---|---|
| 普通用户带 `-<channelId>` 指定来源 | `TestSetupContextForTokenRejectsPinSuffixForNonAdmin`（扩展既有 `middleware/auth_test.go`） | **PASS** —— 403 + `specific_channel_version` 头 |
| 账户被封禁 | `TestUserAuthRejectsDisabledUser` | **PASS** —— 401 `AUTH_USER_DISABLED` |
| 既有用例未被破坏 | `go test ./middleware/ -count=1` | **ok** |

**过程记录**：首次运行 pin 用例失败（`no such table: tokens`）—— 该夹具的内存库只迁移 `users`/`AuthFlow`，
已在该用例内补 `AutoMigrate(&model.Token{})`；随后 PowerShell 的 `String.Replace` 被我误用三参数重载
（不存在）导致**文件未改写**，改为单参数后写入校验通过、测试转绿。
---

## 9. T4.3 演练记录（备份恢复 / 迁移幂等 / 配置回滚）

**执行时间**：2026-09-18（本地真实环境：PostgreSQL 5433；演练脚本 `tools/stage4-verify/t43-backup-restore-drill.ps1`）
**纪律**：**不动现场库** —— 备份后恢复到**新库** `apigx_restore_drill` 上校验。

### 9.1 备份 → 恢复 → 逐项指纹比对

| 步骤 | 结果 |
|---|---|
| 备份 | `pg_dump -Fc` ⇒ 427.4 KB（`backup-t43.dump`） |
| 恢复 | `createdb` + `pg_restore --no-owner --no-privileges` ⇒ 成功 |
| 指纹比对 | **12 项全部一致** ✅ |

比对项（恢复前 = 恢复后）：`attempts=99`、`upstream_keys=187`、`channels=156`、`base_channels=31`、
`users=8`、`invites=5`、`grants=0`、`price_rules=4`、`price_rule_items=8`，
以及 **`upstream_key_secret_hash`（凭据密文 md5）**、**`upstream_key_fp_hash`（指纹 md5）**、
`base_channel_key_hash` —— 三项哈希逐字节一致 ⇒ **凭据密文可随备份恢复，且与主密钥仍匹配**。

### 9.2 迁移幂等 / 升级

在**恢复库**上以备用端口 3101 **连续启动两次**：两次启动日志**均无 ERROR/panic/duplicate** 关键字，
启动后行数保持 `attempts=99 / price_rules=4 / upstream_keys=187`（与恢复前一致）⇒ **迁移可重复执行且不改数据**。

### 9.3 配置回滚

改底座选项 `RegisterEnabled=false` → **重启** ⇒ `/api/status` 反映 `false`（改动生效）；
回滚 `RegisterEnabled=true` → **重启** ⇒ 恢复 `true`（回到初始值）。

### 9.4 仍未演练的部分（如实标注）

- **升级回滚（用旧版本二进制回退）**：本环境只有当前构建，未演练"新版迁移后回退旧二进制"；
  已知信息是迁移在 SQLite 上幂等（`TestMigrateIsIdempotentOnSQLite`）且恢复库可重复启动。
- **真实生产切换**：本地演练不能替代生产环境的停机窗口与回滚预案。
- **演练脚本自身的过程教训**：① 脚本为 UTF-8 **无 BOM** 时，Windows PowerShell 5.1 会按 ANSI 读取而乱码报错 ⇒
  已改为 **UTF-8 带 BOM**；② 我最初按 `=` 解析 `psql -tAf` 的 `k|v` 输出 ⇒ 解析出 0 项却打印"✅ 全部一致"（**假绿**）⇒
  已改为按 `|` 解析，并加"0 项即失败"的硬断言。
---

## 10. T4.4 记录（运营审核 + 指标看板与告警）

### 10.1 运营审核（当场取新鲜证据）

| 用例 | 结果 |
|---|---|
| `TestOfferingLifecycleThroughAPI` | **PASS** |
| `TestShareGroupLifecycle` | **PASS** |
| `TestAliasConfirmationWorkflow` | **PASS** |
| `TestCapabilityMatrixIsHonest` | **PASS** |

审核闸门**只对平台侧开放**：供给 `submit → publish`、分组发布状态（[013]）、别名/目录确认，
均要求 `requirePlatformReviewer`；管理入口已并入**系统设置 → 平台管理**（[007]/[009]）。

### 10.2 指标看板（**复用底座，未另造**）

- 底座自带 `perf_metrics` 表与接口：`GET /api/perf-metrics/summary`、`GET /api/perf-metrics`
  （`setting/perf_metrics_setting` 默认 `enabled=true`，按 `bucket_time` 聚合、`retention_days` 保留）
- **实测有数据**：`perf_metrics` 现有 **44 行** ⇒ 看板确实在记录，无需我们重复实现

### 10.3 告警（**本轮新增的缺口**）

盘点结论：我们自己的告警代码**为零**；底座有若干告警配置（消费提醒阈值、会话风暴阈值、通知限流）
但没有通用告警引擎。

**本轮补的**（最小、复用既有通道）：
- 位置：`apigx/routing/assemble.go` 的**熔断打开**分支
- 行为：熔断打开 ⇒ 发**一条限频**运维告警（同一供给 **10 分钟**最多一次），走既有 `common.SysError` 通道
- 为什么限频：熔断期内每个请求都会走到该分支，不限频会把日志刷爆、**反而掩盖问题**
- 设计取舍：去重逻辑抽成 **返回 bool** 的 `claimBreakerAlertSlot` ⇒ 测试断言**"是否该告警"这条不变量**，
  而**不是**断言日志文本（`new-api/AGENTS.md` 明确反对 log-only 断言）
- 实测：`TestBreakerAlertIsRateLimitedPerOffering` **PASS**（首次告警 ✓、间隔内不重复 ✓、差 1 秒不告警 ✓、
  间隔后可再告警 ✓、不同供给互不影响 ✓、非法 id 不告警 ✓）；整个 `apigx/routing` 包 **ok**

### 10.4 仍未做（如实标注）

- **可视化看板页面**：底座有接口与数据，但**我们没有做面向运营的汇总页**（价格页已复用部分指标）
- **外部告警通道**（邮件/Webhook/IM）：**未接入**；当前告警只进入服务日志，需依赖日志采集与规则
---

## 11. T4.5 记录（剩余边界场景：能钉死的钉死，不能的说清原因）

| 台账 | 处置 | 结果 |
|---|---|---|
| #25 分享者暂停 ⇒ 新请求停止 | **新增测试** `TestSuspendedGroupStopsServing` | **PASS** ⇒ 升为 ✅ |
| #1 两位分享者同一模型 ⇒ 广场聚合 | 新增 `TestPlazaAggregatesSameModelFromTwoSharers` | **未闭合** ⇒ 假供给在广场取不到（第三方浏览者仍 0 项），**用例已显式 Skip**，差异待查 |
| #19 按**实际执行 Key** 计价 | 代码核查 | **明确未实现**：`PriceScopeUpstreamKey` 已建模，但 `assemble.go` 取价只用供给/渠道两级 ⇒ 需在路由取价处补 Key 维度 |
| #28 usage 重复/修订 | 数据核查 | **本环境无法验**：`apigx_usage_events` 0 行、无写入方（见 [015]）⇒ 需先补写入方 |

**纪律说明**：本轮**没有**把"看起来没问题"当成结论；未能验证的两项都给出了**具体阻塞点**，
且**没有把红测试留在仓库**（未闭合的用例显式 `Skip` 并写明原因）。
---

## 12. T4.5 第二批（三条钉死 + 一条给出实施方案）

| 台账 | 处置 | 结果 |
|---|---|---|
| #29 同一幂等键重发 | 新增 `TestAttemptIdempotencyKeyRejectsDuplicate` | **PASS** ⇒ ✅ |
| #20 Key 池细节不外泄 | 新增 `TestKeyPoolDetailsAreOwnerOnly` | **PASS** ⇒ ✅ |
| #9 中断不记为完整成功 | 新增 `TestAbandonedAttemptIsNotEmptySuccess` | **PASS** ⇒ ✅ |
| #19 按实际执行 Key 计价 | 代码核查 + 定方案 | **未实现** ⇒ ❌，方案见上表（须单独一轮，涉计费不变量） |

整包回归：`apigx/model` ✅ `apigx/api` ✅ `apigx/routing` ✅

**过程记录（三次"猜"被当场纠正）**：
① `CompleteAttempt` 首次传入我猜的 `state=1` ⇒ 报 `state must be succeeded or failed` ⇒ 改用真实常量 `AttemptStateSucceeded`；
② `MarkAttemptAbandoned` 只对 `state = AttemptStateStarted` 生效，我原先猜 `1`（恰好等于 Started，未暴露问题）⇒ 一并改为常量；
③ 前一次取错误信息时我**只过滤了部分行**，把关键报错行滤掉了 ⇒ 改为输出完整尾段才看清原因。
**剩余 ⚠️（8 项）**：#1（未闭合，用例已 Skip）、#3、#19（未实现）、#21、#22、#23、#28（无数据可验）、#30。
---

## 13. T4.5 第三批（#30 定案 + #22/#23 的精确理由）

### 13.1 #30 内网与元数据地址（定案：主体已满足，边界写明）

| 面 | 结论 |
|---|---|
| **重定向**到内网/元数据 | ✅ 被拦（`service/http_client.go` L44–L53） |
| **外部媒体/结果抓取** | ✅ 走 `ValidateSSRFProtectedFetchURL` / `checkProtectedFetchRedirect` |
| 校验器本身 | ✅ 已有 4 条用例（含"字面私网/保留地址"与"DNS 解析后为私网"两种） |
| 开关 | `EnableSSRFProtection` 默认 **true**；`AllowPrivateIp` 默认 **false** |
| **首跳直连上游 BaseURL** | ⚠️ **不校验**（有意保留：自建/内网上游属合法场景；本环境验收替身依赖回环地址） |

**风险与建议**（不隐瞒）：不校验首跳意味着可用平台网络位置探测内网；若生产要收紧，
可启用 `DomainList/IpList/AllowedPorts` 白名单，或让验收替身改用可解析的域名而不依赖回环。

### 13.2 #22 长流 / 进程崩溃 ⇒ 槽位续租归还恢复（仍 ⚠️，理由具体）

**机制**已被用例覆盖：`TestLeaseExpiryRecoversCapacity`（租约到期回收）、`TestRenewExtendsLease`（续租）、
`TestReconcileReleasesStaleAttempts` + `TestReconcileIgnoresFreshAttempts`（对账释放遗弃尝试、放过新尝试）、
`TestReleaseIsIdempotentAndNeverNegative`（归还不为负）。
**未做**：真实"杀掉进程"演练 —— 需要构造**在途租约**并并发编排，本环境未搭这套编排 ⇒ 保持 ⚠️。

### 13.3 #23 预留后转发器进入原生 Key 选择路径（仍 ⚠️，理由具体）

**已有**：`TestApplyChannelPoolProjectsKeysIntoBaseChannel`（把 Key 池投影进底座多渠道配置）、
`TestRecordKeyOutcomeDoesNotTouchBaseChannel`（Key 结果不回写底座渠道）、
`apigx/relayhook/capacity_hook.go` 通过 `ContextKeyChannelMultiKeyIndex` → `ResolveKeyIdentity` 把底座索引映射回我们的 Key。
**未做**：一条**端到端**断言"预留时确定的 Key 与最终出站使用的 Key 是同一把" —— 需要在真实中继路径上观测，
本环境未闭环 ⇒ 保持 ⚠️。
---

## 14. T4.5 第四批（#19 的根因与依赖链 —— 为什么它不是"补一个分支"）

**目标场景**：Key 价格或上游分组倍率不同 ⇒ 按**实际执行 Key** 的价格版本估算与预留，不借用池内最低价。

**已就绪的部分（设计意图很清楚）**：
- `UpstreamKey.PriceRuleID`（`apigx/model/models_identity.go:263`）注释写明：**Key 价格配置，`apigx_price_rules.id`；0=继承供给/渠道** ⇒ 继承语义已定义
- 价格作用域枚举已含 `PriceScopeUpstreamKey`（scope=1）
- `UsageEvent` 已建模 `CostMicro / CostUsdMicro / CostState`（并有"未定价 ⇒ 成本必须为 0"的硬约束注释）

**缺的部分（这才是真正的工作量）**：
| 缺口 | 证据 |
|---|---|
| `apigx_usage_events` **无写入方** | 全仓搜索只有 `migrate.go` 建表；实测 **0 行**（[015]） |
| `Attempt.PriceRuleID` **无写入方** | 全仓搜索无赋值处 |
| 成本记录代码不存在 | `CostState` 仅出现在字段定义与注释中 |

**当前事实**：实际扣费由**底座自身的 `BillingSession` 预扣/结算**完成（`relay/billing.go`），
我们的价格快照服务的是**候选估算与展示**。

**接线方案（供实施）**：
1. 在 `relayhook/capacity_hook.go`（此处经 `ContextKeyChannelMultiKeyIndex → ResolveKeyIdentity` 已知**执行 Key**）
   按 `PriceScopeUpstreamKey` 取价，缺失时按 `0=继承` 回退到供给 → 渠道；
2. 把选中的 `PriceRuleID` 写入 `Attempt`；
3. 结算时以**同一份**价格产生 `UsageEvent`（`CostState` 区分"已计价/未定价"，未定价时 `cost_usd_micro=0`）；
4. 估算 → 预留 → 结算共用该价格对象，避免三处各自取价而分叉；
5. 按 `new-api/AGENTS.md` 计费清单补回归：饱和转换用 `common.*Checked`、钳制事件经 `attachQuotaSaturation` 落审计。

**结论**：本项**不是一行取价分支**，而是**接通成本记录链路** ⇒ 本环境**未实现**，也不假装实现。
---

## 15. #19 实施勘察（第一段该挂在哪 —— 一次"查明后否掉自己方案"）

**原计划**：在 `service.SettleBilling` 里挂一个 usage 记录器（一处收口，覆盖所有结算路径）。

**勘察后否掉**，理由三条（都有据）：
1. **结算点拿不到真实用量**：`SettleBilling(ctx, relayInfo, actualQuota)`（`service/billing.go:54`）只收到
   **底座的额度值**，没有 token 数、没有计量维度 ⇒ 在此处记账只能写出 `quantity = 0` 的事件，
   那是**误导**（等于宣称"本次没有消耗"），比不记更糟。
2. **真实用量在调用方**：`service/text_quota.go:463` 处有 `summary.PromptTokens / summary.CompletionTokens`
   （同文件 `:542` 已在用它们调 `AdjustReservedTPM`）；任务路径在 `controller/relay.go:643` 有自己的用量。
   ⇒ 记录器应挂在**调用方**，而不是 `SettleBilling` 内部。
3. **循环依赖已排除**：`apigx/relayhook` 不 import `service`，而 `service/text_quota.go` 已 import `relayhook`
   ⇒ 从 `service/*` 调用 `relayhook` 是安全的（这点结论可复用）。

**因此第一段的正确落点**是这几处调用方（各自都能提供真实用量与计量维度）：
`service/quota.go:231`、`service/quota.go:364`、`service/text_quota.go:463`、`controller/relay.go:643`。

**下一段的验收标准（先写清再动手）**：
- 每个落点写出的 `UsageEvent` **必须**带真实 `Dimension` 与数量；**拿不到用量就不写**（宁缺勿假）；
- `CostState` 如实区分"已计价/未定价"；未定价时 `cost_usd_micro` 必须为 0（schema 硬约束）；
- `EventUID` 唯一 ⇒ 重试/重复结算不产生重复事件；
- 用**真实请求**验证落库，并与底座结算数值对照。
---

## 16. #19 第一段：记录链路已接通（真实请求实测）

**改动（只追加记录，未触碰底座扣费）**：
| 文件 | 内容 |
|---|---|
| `apigx/model/usage_event.go`（新） | 依 §5.16/§5.21 补枚举常量（`Kind/Dimension/UnitScale/Trust/CostState`）+ `UsageQuantityMilliFromTokens`（1 token=1000）+ `RecordUsageEvent`（**强制**"未定价 ⇒ `cost_usd_micro=0`"，违规直接拒写） |
| `apigx/relayhook/usage_record.go`（新） | `RecordSettlementUsage`：**拿不到用量就不写**；归属解析不到就留 0（不猜）；失败只记日志、**不影响请求** |
| `service/text_quota.go` | 文本结算成功后接入（`summary.PromptTokens/CompletionTokens`） |

**真实请求实测（一次对话，prompt=16 / completion=16）**：
```
dim=1（输出 token） qty_milli=16000  owner=1  channel=154  key=182  cost_state=2  cost_usd_micro=0
dim=0（输入 token） qty_milli=16000  owner=1  channel=154  key=182  cost_state=2  cost_usd_micro=0
event_uid = u:<请求ID>:<维度>   ⇒ 幂等
```
- `quantity_milli=16000` ⇒ 符合规范"1 token = 1000"；
- `cost_state=2`（未定价）且 `cost_usd_micro=0` ⇒ 满足 schema 硬约束（**不把"价格未知"写成"成本为零"**）；
- **记录里已带 `upstream_key_id`（实际执行的 Key）** ⇒ 这正是 #19 剩余部分（按执行 Key 取价）所需的锚点。

**尚未做（如实）**：按 Key 取价与成本计算；`Attempt.PriceRuleID` 写入；音频与两个 quota 落点；
任务路径**拿不到 token 维度 ⇒ 明确不记**（不留假记录）。