# 阶段 1 验收报告 · 文本共享闭环

日期：2026-09-17　对象：`D:\Github\apigx`（底座固定提交 `69a5002`，fork 分支 `apigx`）
验收方式：每条结论都对应**可复跑的脚本或测试**。复跑前请确认 PostgreSQL(5433)、Redis(6379)、替身(8100)、网关(3000) 在跑。

## 0. 一句话结论

阶段 1 的 **12 个工作包全部实现**；其中 **11 个已端到端验证**，**T1.7 除 TPM 外全部验证**（TPM 有依据推迟，见 §3）。
**所有对外结论都有可复跑的脚本**（`tools/stage1-verify/*.mjs`）与单元测试（`go test ./apigx/... ./middleware/`）。

## 1. 逐工作包验收

| 包 | 交付 | 验证证据 | 结果 |
|---|---|---|---|
| **T1.1** 平台 Key 接入 | 按 TD-07 沿用底座明文存储 + 等值查找；只补可见性约束 | `platform-key.mjs` | **7/7**：等值查找可用、所有者可读明文、**他人拿不到**、拒绝响应不含明文、**越权响应与"不存在"逐字节一致**（不泄露存在性）；另直接查库确认 `logs` 表中明文出现 **0 次** |
| **T1.2** 授权收敛（TD-08） | 令牌「允许模型」校验提取为共用函数并前移到 pin/非 pin 之前 | Go 测试 + `td08-pin-model-limit.mjs` | 修复前 pin 到指定来源可绕过模型限制（**200 且出站**）；修复后 **403 且 0 出站**；反例仍 200；**禁用修复则测试 FAIL**（证明测试有效） |
| **T1.3** 领域表（27 张） | `apigx/model/*`：27 张 `apigx_*` 表 + 迁移 + 契约常量 | `go test ./apigx/model/`、`migrate-pg-matrix.ps1` | **SQLite / PostgreSQL 18.4 / MySQL 8.0.12 三库矩阵全通过**：全新库 27 张；从底座旧库升级 **27 + 37（底座表原样保留）**；均连续迁移两次验幂等；跨库列类型逐类一致 |
| **T1.4** 分享者接口 | `apigx/api` + `apigx/route` + `apigx/secretbox`（分组/渠道/Key 池 CRUD） | `share-api.mjs`、`go test ./apigx/api/` | **16/16**：全链路 200；响应与列表**不含明文与密文信封**；B 对 A 的 5 个资源全 **404**；未登录 **401**；PG 落库核对 `secret_ref` 全为 `enc:v1:` 信封、明文计数 0、墓碑时间戳正确 |
| **T1.5** 多 Key 池与逐 Key 统计 | 加密存储 + 指纹查重 + 投影到底座多 Key + 逐 Key 统计 + 隔离 | `key-isolation.mjs`、`go test ./apigx/channelpool/` | **7/7**：上游 401 → 该 Key `status=2/usable=false`；另一把继续服务（200）；成功/失败统计均落库；**用底座自己的 `GetNextEnabledKey()` 验证它确实跳过失效 Key** |
| **T1.6** 免费计量 `usage_only` | 只替换资金来源（三个方法空操作）+ 跳过钱包前置 | `usage-only.mjs` | **A 默认 3/3 零回归**（零余额 403）；**B 开启 5/5**（零余额 200 且到达上游、**钱包仍为 0**、令牌额度闸门仍 401、成本仍记账 1725→3450） |
| **T1.7** 共享容量控制 | 容量账本（Lua 原子预留/幂等归还/租约）+ 渠道·Key·账户池并发 + RPM + **TPM** + 每日预算 + 对账 + 按尝试预留 + 上限接策略库 | `capacity-limit.mjs`、`account-pool-limit.mjs`、`channel-policy-limit.mjs`、`channel-rpm.mjs`、`go test ./apigx/capacity/` | 账本 **40 并发抢 5 槽位恰好发放 5 个**；渠道并发 **4/4**；账户池 **4/4**（两把 Key 同池、渠道不设限 → 200×1+429×2，**不能因换 Key 叠加容量**）；策略库驱动 **4/4**（明确值/明确不限/未配置三者行为可区分）；RPM **3/3**（串行 200,200,429 → **速率计数不归还**）；TPM **4/4**（`tpm_limit=100` → 429+Retry-After；明确不限与充足上限 → 200；Redis 计数 **8202 = prompt 10 + 输出上限 8192**，即预留量取自**可执行的输出上限**）；预算 **累加 18750→56250 + 上限不足 403**；对账：陈旧→释放+遗弃、**未陈旧的长流不误伤**、完成幂等 |
| **T1.8** 模型获取与供给发布 | 一键获取（复用底座 40+ 适配器）+ 手工添加 + 草稿→待审核→已发布 | `fetch-models.mjs`、`go test`（仓储+接口） | **8/8**：上游拉到 3 个模型；预览不落库；导入为**草稿**；重复导入幂等（created=0/skipped=3）；越权 404；另测**普通用户自行发布被 403 拒绝**（审核门槛） |
| **T1.9** 广场与详情 | 只聚合「已发布 + 分组公开」；**只启用已完成能力** | `plaza.mjs` | **14/14**：未认证 401；草稿不出现；未发布详情 404；发布后出现且来源可辨识；能力 `streaming=true / vision=false`，声明未具备者单列 `unverified_capabilities`；泄露扫描 4 项全过 |
| **T1.10** 平台 Key 四偏好 | 允许模型 / 允许来源 / 固定来源 / 回退 | `source-allowlist.mjs`、`pinned-and-models.mjs` | 允许来源 **5/5**（含**可回退 ≠ 绕过限制**）；允许模型与固定来源 **5/5**（403 `model_not_allowed` / 403 `pinned_source_unavailable`） |
| **T1.11** 用量记录 | 复用底座 `GetUserLogs`，补"实际来源"与金额换算 | `usage-records.mjs` | **12/12**：模型/token/成本/耗时/来源正确；**只看到自己的记录**；泄露扫描全过 |
| **T1.12** 拒绝清单 | 统一状态码与不泄露 | `rejection-matrix.mjs` | **10/10**：401 / **404（与"不存在"逐字节一致）** / **403 `source_not_allowed`** / **429 + Retry-After** / 泄露扫描 4 项 |

## 2. 跨工作包的决定与偏离（都登记在案）

| 决定 | 内容 | 登记位置 |
|---|---|---|
| TD-07 | 平台 Key 沿用底座**明文存储 + 等值查找**（不加哈希分支） | `docs/architecture/tech-debt.md` |
| 二开第一原则 | 动手前先 grep 底座；把 T1.5 早期"重写多 Key 选择器"作为**永久反面案例** | `CODE_STYLE.md` 第 25 条 |
| 上游改动台账 | D-001…D-017（含 3 行导出包装以复用底座取模型能力、钩子挂载点等） | `docs/stage0/upstream-diff.md` |
| 已知偏离 | 底座**旧接口**失败时返回 `HTTP 200 + {success:false}`；我们自己的 `apigx/**` 一律用规范状态码 | `TASK_LOG.md` §T1.12 |
| 残留风险 | 投影后凭据会以**明文**出现在底座 `channels.key`（底座设计）；消除需改底座 relay 取键路径 | `apigx/channelpool` 包注释、`TASK_LOG.md` |
| 过渡做法 | `apigx_attempts.request_id` 暂由 `AttemptUID` 派生（阶段 1 尚未持久化请求意图） | `repo_attempt.go` 注释 |

## 3. 未完成项（**无**）

**阶段 1 无未完成项。** 原计划的 TPM 已于本轮落地：底座在 `relay.PrepareRequestBilling` 里**已经算出** `TokenCountMeta.MaxTokens`（缺省 8192）与 prompt token 数且尚未发送，因此 TPM 挂在**请求解析之后**完全可行，预留量 = `prompt + 输出上限`（实测 Redis 计数 8202 = 10 + 8192）。该位置还在 `FreeModel` 短路**之前**，免费模型同样受约束。

（此前记录的"因拿不到输出上限而推迟"**已被推翻并更正**——位置本身不是障碍，`apigx/relayhook/request_tpm.go` 即是实现。）

## 1.5 跨工作包回归复跑（2026-09-17，当前二进制含全部改动）

早期工作包在**后续改动叠加之后**是否仍然成立，是验收最该确认的事。已复跑三个覆盖面最广的套件：

| 套件 | 结果 |
|---|---|
| `rejection-matrix.mjs`（T1.12 状态码与不泄露） | **10/10** |
| `plaza.mjs`（T1.9 广场；依赖 T1.8 的供给与发布流程） | **14/14** |
| `fetch-models.mjs`（T1.8 一键获取 + 导入） | **8/8** |

即：T1.12 / T1.9 / T1.8 在**包含 T1.7 全部容量改动**的同一二进制上仍然全绿，未出现跨包回归。

## 4. 如何复跑
```powershell
# 1) 单元/集成测试（需要 Redis；PG 矩阵另需 APIGX_TEST_PG_DSN）
. tools\dev\env.ps1
cd new-api; $env:APIGX_TEST_REDIS_ADDR='127.0.0.1:6379'
go test ./apigx/... ./middleware/ -count=1

# 2) 端到端（需先起 PG/Redis/替身/网关）
node tools/stage1-verify/platform-key.mjs
node tools/stage1-verify/share-api.mjs
node tools/stage1-verify/key-isolation.mjs
node tools/stage1-verify/capacity-limit.mjs --expect on
node tools/stage1-verify/account-pool-limit.mjs
node tools/stage1-verify/channel-policy-limit.mjs
node tools/stage1-verify/channel-rpm.mjs
node tools/stage1-verify/fetch-models.mjs
node tools/stage1-verify/plaza.mjs
node tools/stage1-verify/source-allowlist.mjs
node tools/stage1-verify/pinned-and-models.mjs
node tools/stage1-verify/usage-records.mjs
node tools/stage1-verify/rejection-matrix.mjs
```

**注意（测试环境陷阱）**：端到端脚本会**跨脚本留下状态**（例如平台 Key 偏好、预算计数、限速策略）。复跑单条脚本前，若它依赖"干净状态"，请先清掉相关偏好/策略；否则可能因为**残留状态**而失败——本轮排查中我就被自己上轮留下的 `allowed_model_ids` 误导过一次。

## 5. 我在实现过程中修掉的缺陷（抽样，说明测试确实在起作用）

pin 绕过模型限制（T1.2）、`apply` 只写库不刷缓存、统计回填误写库、索引空间在隔离后分歧、恢复渠道未重建路由索引、**准入钩子提前返回导致 BaseURL 未写入（线上级 500）**、RPM 两处门控缺陷、`base_channel_id` 唯一索引与 0 冲突、渠道可挂到别人分组（越权）、`deleted_at` 写入整数致 PG 报错、MySQL 默认引擎为 MyISAM 导致"无事务"假通过、`key_hash` 唯一索引与偏好行冲突、预算门控/窗口计数等。

**其中相当一部分是"单测全绿但行为错误"**——它们都在"我们的状态"与"底座的状态/缓存"之间的边界上，只有真实链路（网关 + PG + Redis + 替身）才能暴露。
