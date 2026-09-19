# 阶段 D：价格与六类计量数据流审查

> 2026-09-19 初始审查基线：内层 `338405d`；阶段 D 最终提交：`6c92779`。各节按审查、实施和最终验证顺序记录，不把未验证能力写成已完成。

## 1. 必须复用的底座能力

- 请求前与请求后结算继续走 `BillingSession`、`SettleBilling` 和计价表达式快照，APIGX 不建立第二套钱包、扣费或表达式执行器。
- New API 本地价格的权威读取入口是 `ratio_setting` 与 `billing_setting`。`apigx/price/base_source.go` 已能读取按次价、旧倍率和表达式；阶段 D 只把这些值投影为渠道或供给自己的不可变参考价格版本。
- 任务插件的数量由 `usageSchema` / `usageProfiles` 与 `extractUsageOnComplete` 提供。字段 `description` 仍表示“计费对象 + 单价”，单位只从 `unit` 读取。
- APIGX 免费共享保持 `usage_only`。金额是 USD 参考成本，不触发充值、余额或分享者结算。
- 2026-09-19 用户确认一键匹配只复用 New API 本地价格，不接入 ModelRadar、模眼或其他外部价格源；此前外部源调研仅作历史参考，不进入运行时依赖。

## 2. 当前价格链

| 环节 | 当前入口 | 可直接复用 | 缺口 |
|---|---|---|---|
| New API 本地价 | `ReadModelPriceFromBase` | 按次价、输入/输出/缓存的旧倍率可映射为绝对 USD 单价 | 尚未由服务端按渠道模型批量调用 |
| 计价表达式 | `ReadModelPriceFromBillingExpr`、`billingexpr` | 表达式版本、哈希、执行和结算快照 | 动态阶梯、条件、图片 token、任务 usage 不能安全压成单一平面单价 |
| APIGX 价格版本 | `PriceRule` / `PriceRuleItem`、`IngestSourcePrice` | 作用域隔离、只追加版本、原币种/汇率、未知不等于零、人工锁定 | `price-sync` 仍信任客户端上传 readings，缺少服务端候选预览与确认 |
| 运行期成本 | `ResolveUsageCost` | Key → Offering → Channel 的价格回退与不可变引用 | 只支持 token 的 `/1M`、`/1K` 两种单位；图片、音频、视频、请求、字符和文档尚未计算 |

价格匹配只做大小写和空白归一，复用 `NormalizeModelIDForAlias`；不得剥离日期、版本、地区或服务档位。优先匹配实际 `upstream_model_id`，只有已确认的模型别名才能作为补充候选。动态表达式无法无损展开时，预览必须返回冲突或需人工输入，不能选取某个样例价格冒充完整规则。

## 3. 六类能力现状

| 能力 | 最终结算入口 | 当前真实数量 | 当前 APIGX 事件问题 | 阶段 D 接法 |
|---|---|---|---|---|
| 对话 | `PostTextConsumeQuota` | 输入、输出、缓存、推理等 token | 只写输入/输出 token | 从最终 `dto.Usage` 生成 token 分项，保留上游/本地估算可信度 |
| 向量 | `PostTextConsumeQuota` | 输入 token | 与对话共用事件且无能力标识 | 记录为向量能力 + 输入 token，不制造输出 token |
| 重排 | `PostTextConsumeQuota` | 输入 token | 与对话共用事件且无能力标识 | 记录为重排能力 + 输入 token |
| 图片 | `ImageHelper` → `PostTextConsumeQuota` | `RequestedImageCount()`；部分模型另有图片 token | 当前把 token 强制补成 1，APIGX 会写假 token | 结算后写图片张数；只有上游明确报告图片 token 时才另写 token 分项 |
| 语音 | `PostAudioConsumeQuota` 或文本回退 | 上游 audio token；OpenAI TTS 本地可取得真实秒数 | 秒数在适配器内被折算成 token 后丢失，无法按秒追溯 | 把真实输入/输出秒数作为显式事实传到统一写入器；拿不到则记录不可得，不能反推伪造秒数 |
| 视频 | `settleTaskBillingOnComplete` | 插件最终 `UsageFacts`，常见 `seconds` | 最终事实只用于底座差额结算，未写 APIGX 用量事件 | 按冻结的插件 schema 把最终事实映射为视频秒/次等分项；任务 ID 作为幂等键 |

## 4. 统一事件契约

同步请求与异步任务都落到同一组不可变分项：

```text
capability + dimension + unit_scale + quantity_milli + trust
price_rule_id + price_rule_item_id + unit_price + currency + fx snapshot
request_id/async_task_id + stable event_uid + optional correction_of_event_id
```

- `quantity_milli` 永远表示数量的千分之一；1 token、1 张、1 秒、1 次都写 1000，允许毫秒级时长。
- 同一请求或任务的不同维度各写一行；事件 UID 包含来源 ID、能力和维度。
- 原事件不更新。最终数量晚于估算到达时，用 `kind=correction` 追加修订。
- 已定价、待汇率、未定价、明确零价必须分开；未定价的 USD 成本保持 0，但聚合时进入“未定价数量”而不是金额合计。
- 价格、表达式、源币种、汇率来源/时间/版本在用量发生时固化。后续价格同步不得回写历史事件。

## 5. 实施顺序

1. 服务端读取渠道供给与 New API 本地定价，返回逐模型候选和不可自动展开原因；确认时重新校验候选令牌后才写不可变价格快照。
2. 把 `UsageEvent` 补齐能力标识和全单位安全计算，再把文本/向量/重排接到统一写入器。
3. 移除图片的假 token 事件，补图片张数；把语音真实秒数从适配器传出；把任务最终 `UsageFacts` 写入异步事件。
4. 分享者汇总以用量事件为准，分别聚合已定价 USD、待汇率和未定价数量；`attempts` 只继续提供成功率、延迟、失败与重试次数。

## 6. D3 实施结果（2026-09-19）

- `UsageEvent` 已覆盖八类能力、十二种维度、八种单位与四种可信度；价格计算支持 `/1M tokens`、`/1K tokens`、`/张`、`/秒`、`/分钟`、`/次`、`/1K 字符`、`/文档`。
- 同步结算将输入、输出、缓存读、缓存写与推理 token 拆成不重叠分项；向量和重排保留各自能力标识。
- 图片始终记录实际/请求张数；只有上游明确返回 usage 时才记录 token，底座兼容用的 `1 token` 不进入 APIGX 事件。
- STT 上传文件和 OpenAI TTS 响应解析出的真实时长以毫秒保存；无法取得时写 `trust=不可得`，不从 token 反推秒数。
- 成功异步任务把最终 `seconds` 或图片张数写入事件；任务使用提交时冻结的价格版本，稳定事件 UID 和 `settled_usage_event_id` 防止重复结算。
- 定向用例与 `apigx/model`、`apigx/price`、`apigx/api`、`apigx/relayhook`、`relay/channel/openai`、`service`、`controller` 联合回归通过。三库、全仓、HTTP 和补丁重建仍按 D5 执行。

## 7. D4 分享者聚合与界面结果（2026-09-19）

- `Attempt` 只聚合请求数、上游尝试数、重试、成功/失败/未完成和首字延迟；金额与多模态数量不再从 Attempt 或 token 反推。
- `UsageEvent` 按渠道、稳定 Key 和请求时的标准模型 ID 聚合能力/维度数量；上游上报、本地估算、未知与不可得保持可区分。
- 校正行统一指向根事件，汇总只取根事件及其校正中的最新一行；原值与历史校正保留审计轨迹，但不会重复进入净用量和成本。
- USD 总额只包含已估算状态；待汇率、未定价、明确零价分别返回事件数。未定价行的 `0` 不会显示成零成本。
- 汇总接口默认最近 30 天，单次最多 90 天，页大小最多 100，并按 owner 直接过滤；响应不含上游 Key 明文、内部地址或使用者身份。
- 「我的分享」内的消耗卡支持渠道/Key/模型、7/30/90 天和翻页，展示实际计量、估算标记、不可得计量与四种成本状态；未新增重复的独立用量页面。
- `apigx/model`、`apigx/api`、`apigx/relayhook` 联合回归通过；前端 typecheck、相关 11 个用例和本次组件静态检查通过。最终验证与交付结果见 D5。

## 8. D5 最终验证与交付（2026-09-19）

- 阶段 D 可控 HTTP 验收 20/20；SQLite 3.50.4、PostgreSQL 18.4、MySQL 5.7.26 覆盖迁移幂等、异步任务和分享者聚合语义，MySQL 表使用 InnoDB。
- `go test ./... -count=1 -timeout=10m` 全仓通过；Windows HTTP/2 GOAWAY 与断路器缓存偶发点分别定向重复 20 次通过；`relaykit` 在 `GOWORK=off` 下独立通过。
- 前端生产构建、typecheck、分享用量卡与 i18n 定向用例通过；按「前端构建 → Go 构建 → 重启」生成的最终候选二进制在 `http://127.0.0.1:3000/api/status` 返回 200。
- 内层提交为 `6c927799038205180d4fcfd683fcf86c4a11bf68`；第 163 个补丁已导出。从锁定基线顺序应用 163 个补丁后，重建 tree `8910ff2ef81e31f8dd5e19fd8112b0c4207c416d` 与 fork HEAD 完全一致。
- 以上证明本地可控环境中的阶段 D 功能、数据库兼容和交付可重建性；不代表真实第三方全能力、生产多实例、持续负载或正式安全验收已经完成。
