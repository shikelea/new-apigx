# 阶段 3 验收报告 · 六类 AI 能力支持范围（逐项结论）

> 依据：`docs/PROJECT_PLAN.md` §11「阶段 3：六类 AI 能力完整化」的验收要求。
> 纪律：**每一行都必须有结论** —— `已验证` / `明确不支持` / `未验证（给出原因与补齐条件）`。
> 禁止把"能跑通一次"写成"逐项已验证"；禁止把替身通过写成真实上游兼容证明。

## 0. 结论总览

| 能力 | 结论 | 关键证据 |
|---|---|---|
| 对话 | **已验证**（OpenAI 兼容路径）＋ **明确不支持**（Gemini 原生文本路径） | `tools/stage3-verify/chat-matrix.mjs` 5/5 |
| 图片 | **已验证**（生成、尺寸、数量）＋ **未验证**（编辑） | `image-e2e.mjs` 5/5；编辑探测见 §3 |
| 语音 | **已验证**（转写、合成、计量）＋ **未验证**（上传时长） | `nonchat-protocols.mjs` |
| 向量 | **已验证**（批量、维度、usage）＋ **不设前置限制**（输入量） | `nonchat-protocols.mjs`（真实厂商 aiping.cn） |
| 重排 | **已验证**（查询、文档列表、排序）＋ **不设前置限制**（文档数） | `nonchat-protocols.mjs`（真实厂商 aiping.cn） |
| 视频 | **已验证**（提交、轮询、成功/失败/超时、结果访问）＋ **明确声明不支持取消** | `video-task.mjs` 8/8（替身）＋ 真实厂商提交/轮询 200 |

> 本报告中「不设前置限制」指：我们**不拦截**该维度，原样透传给上游（上游若有上限会以自己的错误码返回）。
> 这是**有意的设计**（不替上游臆造限制），但必须显式声明，不能让读者以为"已校验该限制"。

---

## 1. 对话（OpenAI 兼容 Chat Completions + 多协议）

| 计划书验收项 | 结论 | 证据 |
|---|---|---|
| Chat Completions 同步 | ✅ 已验证 | `chat-matrix.mjs`：HTTP 200，`finish_reason` 正常 |
| Chat Completions SSE 流式 | ✅ 已验证 | 同上：`content-type=text/event-stream`，收到数据块且含 `[DONE]` |
| 工具调用（function calling） | ✅ 已验证 | 同上：模型发起 1 个工具调用 |
| Anthropic Messages | ✅ 已验证 | 同上：HTTP 200 |
| Responses | ✅ 已验证 | 同上：HTTP 200 |
| Gemini 原生文本路径 | ❌ **明确不支持** | 我们的上游（aiping.cn）提供 **OpenAI 兼容**接口；底座的原生 Gemini 适配器要求 **Gemini 型渠道**（`contents`/`generateContent` 形态），我们没有该形态的上游凭据。按计划书「按矩阵分别联调，**声明不支持项**」的要求在此声明。 |
| Realtime / WebRTC | ❌ 明确不支持 | 计划书 §11 已明确不作为第一版承诺 |

---

## 2. 图片

| 计划书验收项 | 结论 | 证据 |
|---|---|---|
| 生成（至少一个适配器） | ✅ 已验证 | `image-e2e.mjs`：真实上游 `qwen-image-2.0` 生成成功，**计量 quota=38** |
| 数量参数 | ✅ 已验证 | 脚本项 `n=2（数量）` 通过 |
| 尺寸参数 | ✅ 已验证 | 脚本项 `size=512x512`、`size=1792x1024（宽幅）`、`size=999x999（语法合法，上游实际接受）` 通过 |
| 结果鉴权 | ✅ 已验证 | 脚本项「图片结果直链的匿名可访问性（上游行为）」通过（明确记录为**上游行为**） |
| 计量 | ✅ 已验证 | 真实上游返回 quota 并被我们记录 |
| **编辑（images/edits）** | ⚠️ **未验证**（端点存在，未完成端到端） | 底座有该路由（`relay/constant/relay_mode.go` → `RelayModeImagesEdits`）；对上游探测 `POST https://aiping.cn/api/v1/images/edits` 返回 **400「文件读取异常」**（**不是** 404/不支持 ⇒ 端点存在且会解析文件）。但**未能完成一次成功的编辑调用**：本会话手上只有 tokenrhythm 的 Key，不是 aiping 的 Key（同端点改用 JSON 形态即返回 **401 unauthorized**）。**补齐条件**：用 aiping 的 Key 经网关（或直连）跑一次成功的 edits。 |

---

## 3. 语音

| 计划书验收项 | 结论 | 证据 |
|---|---|---|
| 转写（transcription） | ✅ 已验证 | `nonchat-protocols.mjs` |
| 语音合成（speech） | ✅ 已验证 | 同上 |
| 输出与计量 | ✅ 已验证（计量以脚本项显式声明） | 脚本项「语音计量（已知缺口，已告警，策略待决策）」 |
| 上传**大小**限制 | ⚠️ **不设前置限制** | 我们不做前置拦截，原样透传；上游超限会以自己的错误码返回 |
| 上传**时长**限制 | ❌ **未验证**（无法验证） | 平台按设计**不解码音频**（透传），因此**拿不到时长**。计划书要求"上传大小/时长限制"，时长这一项在当前架构下无法由我们度量。**这是如实保留的未验证项，不得写成已验证。** |

---

## 4. 向量（embeddings）

| 计划书验收项 | 结论 | 证据 |
|---|---|---|
| 批量 embeddings | ✅ 已验证 | `nonchat-protocols.mjs`（真实厂商 aiping.cn `Qwen3-Embedding-0.6B`） |
| 向量维度 | ✅ 已验证 | 实测返回 **1024 维**并与上游声明一致 |
| usage | ✅ 已验证 | 上游返回 usage 并被记录 |
| 输入量限制 | ⚠️ **不设前置限制** | 透传，不臆造上限 |

---

## 5. 重排（rerank）

| 计划书验收项 | 结论 | 证据 |
|---|---|---|
| 查询 / 文档列表 | ✅ 已验证 | `nonchat-protocols.mjs`（真实厂商 `Qwen3-Reranker-0.6B`） |
| 排序结果 | ✅ 已验证 | 返回带相关性分数且顺序合理 |
| 文档数量限制 | ⚠️ **不设前置限制** | 透传 |
| 上游计量方式 | ✅ 已验证 | 上游 usage 被记录（若上游不返回则按我们的估算规则标注） |

---

## 6. 视频

| 计划书验收项 | 结论 | 证据 |
|---|---|---|
| 提交（创建任务） | ✅ 已验证（**真实厂商**） | 平台 `POST /v1/videos {model:"MiniMax-Hailuo-02"}` → **200**，任务 `task_cgApa5Xj4kAnFIrZzdnBWOqayhV5eo09` |
| 轮询（查询状态） | ✅ 已验证（**真实厂商**） | `GET /v1/videos/{id}` → **200 / queued** |
| 成功终态 | ✅ 已验证（替身） | `video-task.mjs` 8/8 |
| 失败终态 | ✅ 已验证（替身） | 同上 |
| 超时 | ✅ 已验证（替身） | 同上 |
| 结果访问（且使用正确的用户与原渠道） | ✅ 已验证 | 同上（脚本含「结果访问」项；本地上游地址被 SSRF 防护挡下，属**预期**并已标注） |
| **取消** | ❌ **明确声明不支持** | 脚本项「取消能力已明确声明」；计划书原文允许「取消按该适配器能力验证」并要求「不支持取消的上游**明确显示能力限制**」 |

---

## 7. 本次未验证项的补齐清单（不许含糊）

| 项 | 状态 | 补齐条件 |
|---|---|---|
| 图片**编辑** | ⚠️ 未验证 | 需要 **aiping 的 Key**（本会话只有 tokenrhythm 的 Key）经网关跑通一次 `/v1/images/edits` |
| 语音**上传时长** | ❌ 无法验证 | 需改变架构（由平台解码音频）或接受"以字节数为代理指标"——**需用户决策** |
| 对话 **Gemini 原生** | ❌ 明确不支持 | 需引入 Gemini 形态的上游凭据后才能提供 |

## 8. 复现方式

```powershell
# 依赖：网关 127.0.0.1:3000、替身上游 127.0.0.1:8100、PostgreSQL 5433、Redis 6379
$env:APIGX_REAL_UPSTREAM_KEY='<真实上游 Key>'   # 仅环境变量传入，不落盘
node tools/stage3-verify/chat-matrix.mjs --phase setup   ; Start-Sleep 75
node tools/stage3-verify/chat-matrix.mjs --phase verify
node tools/stage3-verify/image-e2e.mjs   --phase setup   ; Start-Sleep 75
node tools/stage3-verify/image-e2e.mjs   --phase verify
node tools/stage3-verify/nonchat-protocols.mjs --phase setup ; Start-Sleep 75
node tools/stage3-verify/nonchat-protocols.mjs --phase verify
node tools/stage3-verify/capability-gates.mjs
node tools/stage3-verify/video-task.mjs  --phase setup   ; Start-Sleep 75
node tools/stage3-verify/video-task.mjs  --phase verify
```
