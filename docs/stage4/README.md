# 阶段 4 验收 —— 一页交接总览

> 详细台账见同目录 `acceptance-evidence.md`（500+ 行，含每次失败、每次自我纠正的原始过程）。
> 本页只回答三个问题：**通过了吗／还剩什么／怎么自己复验**。

## 一、结论（计划书 §11 阶段 4 验收表，实为 31 行）

| 状态 | 条数 | 说明 |
|---|---|---|
| ✅ 已有测试证据或实测通过 | **30** | 每行在台账里都给出可复核的出处（测试名／脚本／库内读数） |
| ⚠️ 部分覆盖 / 附明确理由 | **1** | 仅 **#21**，见下 |
| ❌ 未实现或未演练 | **0** | — |

计数方式：脚本按计划表区间取行、按状态列前缀统计（**不手写**）。台账顶部「0. 结论总览」与之同源。

## 二、唯一未闭合项：#21「异步任务后续查询、凭据轮换或移除」

| 子项 | 结论 | 依据 |
|---|---|---|
| 不跨用户/账户读取任务 | ✅ **实测通过** | owner 读真实任务 ⇒ 200；另一账号用自己的平台 Key 读**同一 task id** ⇒ **400 `task_not_exist`**；代码侧 `model/task.go` 的任务查询全部带 `user_id` |
| 凭据轮换/移除后不随机换 Key | ⚠️ **本层不参与；同任务内行为在本环境不可观测** | `apigx_async_tasks` 实测 0 行、`AsyncTask.UpstreamKeyCredentialVersion` **无写入方**；**专程做的实验**：替身上游视频任务提交后把状态推到 `IN_PROGRESS` 再轮询 ⇒ 底座确实联系了上游，但**提交行不写 `multi_key_index`、轮询不产生日志**（轮询不计费）⇒ 无法观测。要判定需**在底座出站路径插桩** |
| 历史消耗归属保留 | ⚠️ **任务路径有意未记录** | 遵守"拿不到用量就不写"（与 #19 同族纪律）；本层任务表亦无写入方。字段（`UserID/OwnerUserID/ChannelID/UpstreamKeyID/CycleKey`）**均已建模**，接上写入即可 |

**为什么不给一个好看的 ✅**：用"本层没有相关代码"声称"不会随机换 Key"是**空泛结论**；
在没有真实用量的前提下写记录行只会产生**误导性的 0 用量**。把判定手段写清楚更有用。

## 三、怎么自己复验

前置：网关 `http://127.0.0.1:3000`、替身上游 `127.0.0.1:8100`、PostgreSQL(5433)/Redis(6379) 已起；
登录 `apigxroot / ApigxDev#2026`；平台 Key `apigx-stage0-verify`。

| 想验什么 | 跑什么 |
|---|---|
| 全部阶段 3 能力（对话/图片/非对话协议/视频） | `tools/stage3-verify/{chat-matrix,image-e2e,nonchat-protocols,video-task,capability-gates}.mjs --phase setup` ⇒ 等 ~75 秒 ⇒ `--phase verify` |
| 阶段 4 并发与资源耗尽 | `go test ./apigx/capacity/ ./apigx/channelpool/ -count=1` |
| **崩溃后槽位靠 TTL 回收（真实杀进程）** | `node tools/stage4-verify/t22-crash-lease-recovery.mjs`（会真的 `taskkill` 网关并用**显式环境**重启） |
| 备份/恢复/升级回滚 | `tools/stage4-verify/t43-backup-restore-drill.ps1`（PowerShell，需 **UTF-8 BOM**） |
| 权限绕过 / 日志脱敏 | `go test ./middleware/ ./apigx/api/ -count=1` + 台账 §8 的脱敏审计 |
| 这几轮的改动是否回归 | `go test ./apigx/model/ ./apigx/api/ ./apigx/relayhook/ ./middleware/ ./apigx/channelpool/ -count=1` |

## 四、最近这几轮的真实改动（与被演练"顺带"抓出的缺陷）

| 改动 | 说明 |
|---|---|
| `apigx/model/usage_event.go`（新） | 用量事件写入方：枚举常量（§5.16/§5.21）、`RecordUsageEvent`（**强制未定价 ⇒ 成本为 0**）、`AppendUsageCorrection`（追加校正，**绝不回写原行**）、`ResolveUsageCost`（按**实际执行 Key** → 供给 → 渠道继承取价） |
| `apigx/relayhook/usage_record.go`（新） | 结算后记录用量：**拿不到用量就不写**、归属解析不到留 0、失败不影响请求；并写入**预留时刻确定**的周期 |
| `relayhook/capacity_hook.go` | 预留时刻记 `YYYYMMDDHHmm` 供"跨日回到原预留周期" |
| `apigx/api/share.go` | **修产品级缺陷**：供给发布后把模型并入底座渠道 `models` 并重建能力（此前 U-1 自助建渠道**永远 `model_not_found`**） |
| `web/.../model-import-dialog.tsx` | 抓取失败**显示真实原因** + 就地**人工录入**（此前两种情形共用一句误导提示） |

回归（本轮实跑）：`apigx/model` ✅ `apigx/api` ✅ `apigx/relayhook` ✅ `middleware` ✅ `apigx/channelpool` ✅；
前端 `typecheck` 0 错、相关用例 13/13 ✅。

## 五、环境注意（交接时最容易踩的）

- **宿主磁盘**：`C:` 300 GB 仅剩 ~2.9 GB ⇒ 底座会按设计返回 `503 system disk overloaded`（阈值 95%）。腾出空间后真实请求类验收才稳。
- **登录会话上限**：反复登录会触发 `409 AUTH_SESSION_LIMIT`（我曾在 root 名下积到 50 个会话）；清 `user_sessions` 即恢复。
- **演练夹具**：要验"槽位被占住"必须让请求真的挂住 ⇒ 需**上游指向替身(8100)** 且**该模型已发布**；替身的注入接口是 `POST /_control/profile`（`{hang,delayMs,ttftMs,status,...}`），任务状态序列是 `POST /_control/task`。
- **两个已知的底座边界**（非缺陷，属设计）：底座渠道行**必须持有可用上游 Key**（31 行中 7 行形如 `sk-...`）；**首跳直连**上游 BaseURL **不做 SSRF 校验**（自建/内网上游是合法场景）。

## 六、最终确认复跑结果（目标完成后按上表执行）

| 项 | 结果 |
|---|---|
| 备份 → 恢复 → 指纹比对（`t43-backup-restore-drill.ps1`） | ✅ **12/12 项一致**，`exit=0` |
| `chat-matrix` | ✅ 5/5（支持 5，未通过 0） |
| `image-e2e` | ✅ 5/5（隔离重跑；首次失败为相邻流量/异步记账的时序竞态） |
| `video-task` | ✅ 7/7（重做 setup 后；绑定项改用数据库核对 ⇒ user=1、落在合法可路由渠道） |
| `capability-gates` | ✅ 12/12 |
| `nonchat-protocols` | ✅ 16/16 |

**合计 45 项能力/场景 + 12 项备份指纹全部通过，未发现产品回归。** 详见 `acceptance-evidence.md` §21。

**复跑时的两个坑（省你时间）**：① 用 PowerShell 捕获 node 输出前必须设 `[Console]::OutputEncoding = [Text.Encoding]::UTF8`，
否则中文全乱码且信息不可恢复；② **同一套件不要重复跑 setup** —— 会造出多条等价夹具渠道，
使"任务落在哪个渠道"这类断言失败（本次 `video-task` 就是这么失败的，经查不是缺陷）。
## 七、还差什么（收尾清单，截至 2026-09-18）

> 分三类：**需要你的环境/决定**、**计划内但未做的功能**、**验收层面的已知残留**。每项都写明"卡在哪"，不含糊。

### A. 需要你处理（我动不了或不该动）

| # | 事项 | 卡在哪 / 影响 |
|---|---|---|
| A1 | **把 phpStudy 的 `my.ini` 里 `default-storage-engine=MyIsam` 改成 `InnoDB`** 并重启 MySQL | 不改的话，**底座自身的表**在该实例上建不出来（实测 `Error 1071 … passkey_credentials … max key length is 1000 bytes`）⇒ **整台应用在这台 MySQL 上都起不来**。我们的表已显式 InnoDB（不依赖服务端默认值），但底座表不归我们管。 |
| A2 | **清理宿主 `C:` 盘**（仅剩约 2.8 GB；回收站占 3.47 GB） | 底座的磁盘守护（阈值 95%）会间歇性返回 `503 system disk overloaded`，使真实请求类验收不稳定。 |
| A3 | 决定是否**开 UI 那一轮** | 你早先说过「UI 可以先放一下，等项目做完再弄」；计划块已完成，UI 尚未开始。 |

### B. 计划内但未做的功能项

| # | 事项 | 现状 |
|---|---|---|
| B1 | `perf_metrics` 的**可视化看板页面** | 阶段 4 T4.4 只做到「复用底座指标 + 告警」，**没有做前端看板页**。 |
| B2 | **外部告警通道**（邮件 / Webhook / IM） | 目前告警只落进程内日志（`breakerAlertInterval` 限流的那条），**没有对外通道**。 |
| B3 | **UI 那一轮**（整体界面收尾） | 未开始（见 A3）。 |

### C. 验收层面的已知残留（都有据可查，不是"没做"）

| # | 事项 | 说明 |
|---|---|---|
| C1 | #21 的 `offering_id = 0` | 任务归属快照里供给 ID 为空：结算点的上下文未携带供给，**我没有猜填**。 |
| C2 | #21 的「底座轮询是否复用同一把 Key」 | **本环境不可观测**（底座不为轮询记账；提交行也不写 `multi_key_index`）。我们这一侧已记录"服务该任务的 Key"。 |
| C3 | 阶段 3 两项 DECLARED | ① 图片结果直链由**上游签名**（若产品要求"仅授权者可见"，需我们侧代理或转存）；② **视频取消不支持**（平台/上游 404）。 |
| C4 | T3.9 两项无法验证 | ① 图片**编辑**：端点存在但**缺 aiping 凭据**；② 语音**上传时长**：平台不解码音频。 |
| C5 | `image-e2e` 的「最新一条用量」断言 | 对**相邻流量**敏感（读的是"最新一条"）⇒ 紧邻其它套件运行时可能误报；隔离重跑 5/5 通过。属**套件时序**，非缺陷。 |
| C6 | **156 个补丁的"从零重建"未验证** | `deploy/patches` + `deploy/base.lock.json` 已同步，但没有做过"在干净基线上依次 apply 全部补丁再构建"的演练。 |
| C7 | 升级回滚的**范围** | 只验了「回滚可用性」（旧二进制能启动、能读、迁移幂等、新表不丢），**未跑旧版本功能全量回归**。 |
| C8 | MySQL 上「**应用整体跑起来**」未验 | 被 A1 挡住（底座表建不出来）。三库矩阵本身已 ✅（含 MySQL）。 |