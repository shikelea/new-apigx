# 协议与能力支持矩阵

阶段 0 · T0.7 产出。对象：New API 固定提交 `69a50029819a26c53e6babd276d49cfe2f8880ad`（本地 `new-api/`）。
依据：`docs/PROJECT_PLAN.md` §1（六类能力范围）、§8.1、§9.1、**§11 阶段 3 的能力验收范围表**；`docs/stage0/base-lock.md` §4.1、§6。

> **验证边界**：本文全部来自源码阅读。没有编译、没有运行、**没有调用任何上游**。
> 因此「已有实现」只表示「固定提交里存在对应代码路径」，**不表示该协议已端到端联调通过**。
> 凡无法从代码确认的，一律写「未核实」，不做推断。

## 1. 客户端协议入口（`new-api/router/relay-router.go`）

所有转发端点都经过 `middleware.RouteTag("relay")` + `middleware.TokenAuth()`，HTTP 端点再经 `middleware.Distribute()` 选渠道。

| 端点 | 协议格式常量 | 行号 |
|---|---|---|
| `POST /v1/chat/completions`、`POST /v1/completions` | `RelayFormatOpenAI` | `:102-107` |
| `POST /v1/messages` | `RelayFormatClaude` | `:97-99` |
| `POST /v1/responses/compact` | `RelayFormatOpenAIResponsesCompaction` | `:110-112` |
| `POST /v1/alpha/search`（Codex 独立联网搜索） | `RelayFormatOpenAIAlphaSearch` | `:115-117` |
| `POST /v1/edits`、`/v1/images/generations`、`/v1/images/edits` | `RelayFormatOpenAIImage` | `:120-128` |
| `POST /v1/embeddings` | `RelayFormatEmbedding` | `:131-133` |
| `POST /v1/audio/transcriptions`、`/v1/audio/translations`、`/v1/audio/speech` | `RelayFormatOpenAIAudio` | `:136-144` |
| `POST /v1/rerank` | `RelayFormatRerank` | `:147-149` |
| `POST /v1/engines/:model/embeddings`、`POST /v1/models/*path` | `RelayFormatGemini` | `:152-157` |
| `POST /v1/moderations` | `RelayFormatOpenAI` | `:160-162` |
| `POST /v1beta/models/*path`（Gemini 原生） | `RelayFormatGemini` | `:190-210` |
| `GET /v1/responses`（WebSocket） | — | `:78` |
| `GET /v1/realtime`（WebSocket） | `RelayFormatOpenAIRealtime` | `:85-87` |
| `POST /pg/chat/completions`（调试面板，`UserAuth` 而非 `TokenAuth`） | — | `:64-70` |
| `/mj/**`（Midjourney 专用族） | — | `:179-233` |

模型列表端点按请求头**分流协议**（`router/relay-router.go:25-43`）：`x-api-key` + `anthropic-version` → Anthropic；`x-goog-api-key` 或 `?key=` → Gemini；否则 OpenAI。另有 `/v1beta/models`（Gemini，`:46-53`）与 `/v1beta/openai/models`（`:55-62`）。

**已明确未实现**（`RelayNotImplemented`，`:165-176`）：`/v1/images/variations`、`/v1/files`（全部方法）、`/v1/fine-tunes`（全部方法）、`DELETE /v1/models/:model`。
`/v1beta/models/**` 的 `:countTokens` 被显式拒绝（`:194-199`，注释指向 #7283）。

## 2. 上游适配器：注册方式与扩展成本

注册中心是**一个 switch**：

- `new-api/relay/relay_adaptor.go:50` `func GetAdaptor(apiType int) channel.Adaptor` —— 按 `constant.ChannelType*` 返回 `&<pkg>.Adaptor{}`，覆盖 ali / claude / baidu / gemini / openai / palm / tencent / xunfei / zhipu / ollama / perplexity / aws / cohere / dify / jina / cloudflare / siliconflow / vertex / mistral / deepseek / mokaai / volcengine / baidu_v2 / xai / coze / jimeng / moonshot（复用 Claude）/ submodel / minimax / replicate / codex / advancedcustom / sub2api / newapi 等（`:53-125`）。
- 异步任务适配走另一条：`:130 GetTaskPlatform`、`:188 GetTaskAdaptor(platform)`、`:199 getTaskAdaptorForRequest`。
- 适配器实现按目录组织，`new-api/relay/channel/` 下当前有 **40 个目录**（含 `task` 与 `advancedcustom`）。

**新增一个上游协议/厂商需要动的四处**（阶段 3 的扩展成本就在这里）：
1. 在 `constant` 里新增渠道类型；
2. 在 `relay/channel/<name>/` 新增适配器实现（请求构造、响应转换、流式）；
3. 在 `relay/relay_adaptor.go:50` 的 switch 加一个 case；
4. 若是异步任务平台，另在 `GetTaskAdaptor` 链路登记。

## 3. 六类能力 × 客户端协议矩阵

「已有实现」= 固定提交存在该路径的代码；「需适配」= 需要新增转换或适配器；「未核实」= 无法从本轮阅读确认。

| 能力 | OpenAI 兼容（Chat Completions） | OpenAI Responses | Anthropic Messages | Gemini 原生 | Midjourney / 任务插件 |
|---|---|---|---|---|---|
| **对话** | ✅ 已有实现（`relay/compatible_handler.go`、`relay/relay_adaptor.go:61 openai`）；SSE 走同一 handler | ✅ 已有实现（`relay/responses_handler.go`、`relay/responses_request.go`、`relay/chat_completions_via_responses.go`；WebSocket 见 `relay/responses_websocket.go`） | ✅ 已有实现（`relay/claude_handler.go`，路由 `:97-99`） | ✅ 已有实现（`relay/gemini_handler.go`，路由 `:207-209` + `:152-157`） | 不适用 |
| **图片** | ✅ 已有实现（`relay/image_handler.go`，路由 `:120-128`） | 未核实 | 未核实 | 需适配（Gemini 图像生成未在路由里单独暴露） | ✅ 已有实现（`relay/mjproxy_handler.go`、`router/relay-router.go:213-233`） |
| **语音** | ✅ 已有实现（`relay/audio_handler.go`，转写/翻译/合成三端点 `:136-144`） | 未核实 | 未核实 | 未核实 | 不适用 |
| **向量** | ✅ 已有实现（`relay/embedding_handler.go`，路由 `:131-133`） | 未核实 | 不适用（Anthropic 无 embeddings） | ✅ 已有实现（`/v1/engines/:model/embeddings` `:152-154`；`/v1beta` 侧未核实） | 不适用 |
| **重排** | ✅ 已有实现（`relay/rerank_handler.go`，路由 `:147-149`） | 不适用 | 不适用 | 未核实 | 不适用 |
| **视频** | ✅ 已有实现（任务式：`router/video-router.go:11-25` 提交、`:30` 查询、`:31` remix） | 不适用 | 不适用 | 未核实 | ✅ 已有实现（任务插件族：`router/task-router.go:15-18` 提交、`relay/relay_task.go`） |

补充：`RelayFormat*` 常量定义在 `relaykit/types`（`router/relay-router.go:10` 引入 `relaykit/types`），协议转换与 DTO 集中在**独立模块 `relaykit/`**，`go.mod` 用 `replace github.com/QuantumNous/new-api/relaykit => ./relaykit`。

## 4. 能力差异细项（会影响调度，需要逐供给记录）

| 维度 | 底座现状 | 结论 |
|---|---|---|
| 工具调用 / function call | **广泛支持**：转换层 `relaykit/relayconvert/internal/toolconv/{encode,decode}.go`、`relaykit/relayconvert/internal/oai_chat/to_gemini_chat_req.go:262`、`relaykit/relayconvert/internal/oai_responses/to_claude_messages_req.go:94-98`；计费侧 `relay/common/tool_usage.go:42`、`relay/channel/openai/relay-openai.go:188-189`、`relay/channel/gemini/relay-gemini.go:94-106` | ✅ 可复用，但**逐协议核对**，不能假定全部适配器一致 |
| 流式（SSE） | `relay/common/stream_status.go`、各 handler 的流式分支；模型列表分流见 `router/relay-router.go:25-43` | ✅ 有统一基础 |
| 视觉输入 | 未找到显式的「模型能力」布尔字段（检索 `SupportVision` / `VisionEnabled` 无命中） | **未核实**；能力表达很可能散落在各适配器的请求构造里 |
| 上下文长度 / 最大输出 | 检索 `ContextLength` / `MaxContextTokens` 无命中 | **未核实** |
| JSON 格式、缓存读写 | 未定位到统一的供给级声明 | **未核实** |
| 音色、视频规格 | 未定位 | **未核实** |
| 任务取消 | 未定位到统一的 Cancel 接口（任务适配器在 `relay/channel/task/**`、`relay/relay_adaptor.go:188`） | **未核实**；阶段 3 需按适配器逐个确认 |

**由此得到一条设计结论**：底座**缺少统一的「模型供给能力声明」**。计划书 §8.2 末段要求「同一模型的工具调用、视觉输入、上下文长度、JSON 格式、音色和视频规格可能因渠道而异，记录在供给层，自动路由先按请求能力过滤」——**这个供给层能力记录必须由我们新增**（见 `docs/stage0/schema-draft.md` 的 `KeyModelCapability` / `ModelOffering`），不能指望从上游读出来。

## 5. 异步任务链路

| 环节 | 入口 | 处理 |
|---|---|---|
| 提交 | `POST /v1/tasks/:key`（`router/task-router.go:15-18`，带 `PrepareTaskPluginSubmit` + `Distribute`） | `controller.RelayTask` → `relay/relay_task.go` |
| 视频提交 | `router/video-router.go:11-25`（共享组，`:15` 起为多行注册） | `controller.RelayTask` |
| 查询 | `GET /v1/tasks/:key`（`router/task-router.go:24`）；`GET /v1/video/generations/:task_id`（`router/video-router.go:30`） | `controller.GetTask` / `RelayTaskFetch` |
| 产物列表 | `GET /v1/tasks/:key/artifacts`（`router/task-router.go:25`） | `controller.GetTaskArtifacts`（`controller/task.go:66`） |
| 产物下载 | `GET /v1/tasks/:key/artifacts/:artifact_key/content`（`router/task-router.go:28-34`） | `controller.TaskArtifactContent`（`controller/task.go:279`），带产物访问中间件 |
| 平台解析 | — | `relay/relay_adaptor.go:130 GetTaskPlatform`、`:188 GetTaskAdaptor` |

产物访问的授权与签名现状见 `docs/stage0/reuse-modify.md` 第 3.4 节（这是我们要收紧的默认策略）。

## 6. 第一版明确不承诺的项（有代码证据）

| 项 | 证据 |
|---|---|
| `/v1/files` 全部端点 | `router/relay-router.go:166-170` 为 `RelayNotImplemented` |
| `/v1/fine-tunes` 全部端点（模型训练） | `:171-175` |
| `/v1/images/variations` | `:165` |
| `DELETE /v1/models/:model` | `:176` |
| Gemini `:countTokens` | `:194-199` 显式拒绝 |
| Batch 批处理管理 | 上述路由清单中**未见** batch 端点 |
| 任意第三方插件安装、开放式透传管理端点 | 与计划书 §9.1 末段一致：未纳入统一授权/计量的端点默认关闭 |

`/v1/realtime` 与 `/v1/responses`（WebSocket）在代码里**存在**，但计划书 §11 阶段 3 明确把 Realtime/WebRTC 列为「不作为六类能力第一版的隐含承诺」。因此它们属于**存在但不在第一版验收范围**的路径，是否纳入需另行决策。

## 7. 阶段 3 真实联调的最小清单

要让六类能力各有至少一个适配器端到端通过，需要：

| 能力 | 需要的外部输入 |
|---|---|
| 对话 | 至少 1 个上游的 OpenAI 兼容 Chat Completions（含 SSE）与工具调用；Anthropic / Gemini 原生路径按矩阵分别联调 |
| 图片 | 至少 1 个支持生成/编辑的上游，并确认其支持的尺寸与数量档位 |
| 语音 | 至少 1 个上游分别验证转写与合成，并确认上传大小/时长限制 |
| 向量 | 至少 1 个上游的批量 embeddings，确认维度与输入量限制 |
| 重排 | 至少 1 个上游的 rerank，确认文档数量限制与计量方式 |
| 视频 | 一个可取得测试权限的上游，覆盖提交、轮询、成功/失败/超时、结果访问；取消能力按该适配器单独确认 |

原则（计划书 §11）：**替身通过不等于真实兼容**。上述每项都要用真实凭据联调后才算通过。

## 8. 未核实清单

1. 「视觉输入 / 上下文长度 / JSON 格式 / 缓存读写 / 音色 / 视频规格」是否有可复用的能力表达：本轮**未找到**统一的声明字段，需要时须在 `relay/channel/**` 与 `setting/model_setting/**` 逐个适配器确认。
2. 任务**取消**能力：未定位统一接口。
3. 图片与视频在 Gemini / Anthropic 原生路径下的支持情况。
4. `/v1beta` 侧的 embeddings 支持情况。
5. 各适配器对**流式 + 工具调用**组合的实际支持差异（计费侧代码存在，不代表每个适配器都支持）。
6. 本文**未做任何运行验证**；矩阵中的「已有实现」仅表示代码路径存在。
