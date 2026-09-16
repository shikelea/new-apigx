# 免费共享模式（usage_only）实现与验证记录（T1.6）

对象：底座计费链路上的 APIGX 扩展。
依据：计划书 §6（免费模式必须改结算路径而非数值）、§11 阶段 1 验收；`docs/stage0/metering-state.md` §2（落点）、`docs/stage0/gate-review.md`（闸门约束）。

## 1. 实现

| 文件 | 类型 | 内容 |
|---|---|---|
| `service/apigx_usage_only.go` | 新增 | 模式开关（`APIGX_USAGE_ONLY`，`atomic.Bool` + `common.GetEnvOrDefaultBool`）、`UsageOnlyFunding`、`newUsageOnlySession` |
| `service/billing.go` | 修改 | 新增常量 `BillingSourceUsageOnly = "usage_only"` |
| `service/billing_session.go` | 修改 | ① `NewBillingSession` 最前面加分支：模式开启时直接走 `newUsageOnlySession`；② `reserveFunding` 的类型 switch 增加 `case *UsageOnlyFunding` 返回 nil |
| `service/apigx_usage_only_test.go` | 新增 | 4 个单测（见 §3） |

设计要点：

- **只替换资金来源**：`UsageOnlyFunding` 的 `PreConsume/Settle/Refund` 全部是空操作，因此「没有充值余额的合法用户」可以正常调用。
- **保留令牌额度闸门**：`BillingSession.preConsume` 里的令牌额度原子预留完全不变——去掉的是**用户钱包**前置条件，不是平台 Key 的额度约束。
- **保留计量**：结算与用量日志链路不变，估算成本照常记账。
- **不碰 TrustQuota 旁路**：`shouldTrust` 未改动；APIGX 的容量预留将**独立于** `effectiveQuota`（TD-09，见 T1.7）。

**明确没有采用的四条错误路径**（闸门评审已点名）：

| 错误做法 | 为什么不行 |
|---|---|
| 走 `FreeModel` 短路 | `relay/request_billing.go` 会直接 `return nil`：既不建计费会话也不结算，**计量与预算预留全部丢失** |
| 把模型价格/倍率设为 0 | 估算成本恒为 0，且把「分享者声明的真免费模型」与「平台不计费」混为一谈 |
| 给用户超大余额 | 会触发 `shouldTrust` 旁路，连令牌额度预扣都跳过，计量与预留同时失真 |
| 另起一套计量 | 绕过 clamp/负值校验与结算一致性，违反「不另起第二套系统」 |

## 2. 运行时 A/B 验收（`tools/stage1-verify/usage-only.mjs`）

两阶段跑在同一个底座上，唯一差别是启动时是否设 `APIGX_USAGE_ONLY=true`：

| 检查项 | 阶段 A（默认） | 阶段 B（usage_only 开启） |
|---|---|---|
| 用户 B 钱包置 0 | quota=0（确认生效） | quota=0（确认生效） |
| 零余额调用 | **HTTP 403**（`insufficient_user_quota`），0 次出站 | **HTTP 200**，**1 次到达上游** |
| 用户钱包是否被扣 | 未变动（请求被拒） | **quota 仍为 0** —— 只记账 |
| 平台 Key 额度为 0 | **HTTP 401**，0 次出站 | **HTTP 401**，0 次出站（闸门未被放开） |
| 成本是否记账 | 不适用 | **令牌 `used_quota` 1725 → 3450**（+1725） |
| 结果 | **3/3 通过** | **5/5 通过** |

**这两列合起来才是完整的结论**：默认行为零回归，开启后按设计放开用户钱包前置、同时**没有**放开令牌额度闸门，且成本估算仍为正。

## 3. 单元测试（`service/apigx_usage_only_test.go`，4/4 通过）

1. `TestUsageOnlyFundingIsNoOp` —— 三个方法必须都是空操作（防止有人把钱包调用塞回来）。
2. `TestUsageOnlyModeDefaultsOffAndToggles` —— 开关可切换，默认关闭。
3. `TestReserveFundingAcceptsUsageOnlyFunding` —— `reserveFunding` 的类型 switch 必须接住新资金源（否则会落到 `default` 报 `unsupported funding source`，这正是实现时差点漏掉的一处）。
4. `TestReserveFundingStillRejectsUnknownFunding` —— 反向保护：新增 case 不得把 `default` 放宽。

## 4. 回归检查中发现的基线事实（重要）

`go test ./service/` 有 **2 个失败**：

```
--- FAIL: TestObserveChannelAffinityUsageCacheByRelayFormat_MixedMode
--- FAIL: TestObserveChannelAffinityUsageCacheByRelayFormat_UnsupportedModeKeepsEmpty
```

用 **上游固定提交的独立工作树**（`git worktree add .toolchain/pristine-base 69a5002`）对照验证：**这两个用例在原版上同样失败**，与本项目改动无关。

**因此本项目的验收标准应表述为「不新增失败」，而不是「全绿」。** 这两个既存失败属于底座自带问题，已在此登记；若将来要修，属于对上游的额外修改（需登记台账）。
