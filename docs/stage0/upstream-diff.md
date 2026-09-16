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
| — | — | — | （暂无） | — | — | — | — |

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
