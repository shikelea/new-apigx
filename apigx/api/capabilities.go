package apigxapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 能力矩阵：把「六类能力各自支持到什么程度」做成**机器可读的唯一事实来源**。
//
// # 为什么要做成接口，而不是只写在文档里
//
// 计划书对阶段 3 有两条要求：**支持范围逐项记录**，以及**尚未支持的维度不能在界面宣称已生效**。
// 若这些只存在于文档或某个前端的硬编码里，界面迟早会与真实能力**漂移**（宣称了没做的、或漏掉了限制）。
// 因此这里把矩阵固定在**后端一处**：界面、文档、验收脚本都从这里取，谁也不能各自发挥。
//
// # 状态取值（刻意只有三种）
//
//   - `supported`：已用**真实上游**端到端验证过（证据写在 evidence 里）；
//   - `declared`：**未取得真实凭据**，仅以底座适配器 + 替身验证了协议与计量 —— **不得**在界面宣称"已验证"；
//   - `unsupported`：明确不支持（端点不存在或不接受该操作），界面**必须**显示能力限制。
//
// `pending_decisions` 列出"能力本身可用、但产品策略未定"的事项，避免界面把它们说成"完全可用"。
type capabilityEntry struct {
	Capability  string   `json:"capability"`
	Endpoints   []string `json:"endpoints"`
	Status      string   `json:"status"`
	Evidence    string   `json:"evidence"`
	Limitations []string `json:"limitations,omitempty"`
}

// capabilityMatrix 是矩阵本体。改动它时必须同步更新 evidence 与 limitations，
// 因为它是界面"能宣称什么"的依据。
var capabilityMatrix = []capabilityEntry{
	{
		Capability: "chat", Endpoints: []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"},
		Status:   "supported",
		Evidence: "真实上游端到端 5/5：同步、SSE 流式、工具调用、Anthropic Messages、Responses（含视觉输入）",
	},
	{
		Capability: "image", Endpoints: []string{"/v1/images/generations", "/v1/images/edits"},
		Status:   "supported",
		Evidence: "真实上游端到端：生成 200、用量含配额、非法尺寸 400；尺寸语法与范围已加固（台账 D-021）",
		Limitations: []string{
			"图片编辑端点存在但未与真实上游联调",
			"上游返回**签名直链**（有效期内持有者可直接访问）：若要求“结果仅授权者可见”，需我方代理或转存",
		},
	},
	{
		Capability: "embedding", Endpoints: []string{"/v1/embeddings"},
		Status:   "declared",
		Evidence: "未取得真实厂商凭据：以底座适配器 + 替身验证了协议往返、usage 计量与规格拒绝",
	},
	{
		Capability: "rerank", Endpoints: []string{"/v1/rerank"},
		Status:   "declared",
		Evidence: "未取得真实厂商凭据：以底座适配器 + 替身验证了查询/文档往返、计量与空文档拒绝",
	},
	{
		Capability: "audio", Endpoints: []string{"/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech"},
		Status:   "declared",
		Evidence: "未取得真实厂商凭据：以底座适配器 + 替身验证了转写往返与计量；缺文件已改为 4xx（台账 D-024）",
	},
	{
		Capability: "video", Endpoints: []string{"/v1/videos", "/v1/videos/:id", "/v1/videos/:id/content", "/v1/videos/:id/cancel"},
		Status:   "declared",
		Evidence: "按指定使用底座内置 kling 插件 + 替身模拟：提交/轮询/终态/绑定/跨用户/结果访问 8/8（台账 D-022 修复了状态映射导致的永久卡住）",
		Limitations: []string{
			"**不支持取消**：`/v1/videos/:id/cancel` 返回 404 —— 界面必须显示该能力限制，不得显示“可取消”",
			"通用任务 API `/v1/tasks` 在本配置下**未启用**（404）",
			"真实厂商联调未取得凭据",
		},
	},
}

// listCapabilities 返回能力矩阵（只读；任何已登录用户可读，因为界面要靠它如实展示）。
func (h *handler) listCapabilities(c *gin.Context) {
	if _, valid := h.ownerID(c); !valid {
		return
	}
	ok(c, gin.H{
		"matrix": capabilityMatrix,
		// 状态语义也一并给出，避免调用方自行猜测取值含义
		"status_legend": gin.H{
			"supported":   "已用真实上游端到端验证",
			"declared":    "未取得真实凭据；仅以底座适配器+替身验证协议与计量，界面不得宣称“已验证”",
			"unsupported": "明确不支持，界面必须显示能力限制",
		},
		"pending_decisions": []gin.H{
			{"topic": "unknown_price_billing", "detail": "未配置价格的模型当前按 0 计（日志有实证）；拒绝 / 兜底价并标注 / 允许但标记未计费，三种策略待定"},
			{"topic": "result_proxy", "detail": "图片与视频结果为上游签名直链；是否加我方代理以使“结果仅授权者可见”待定"},
		},
	})
}

// 供路由注册使用（保持 handler 风格一致）
var _ = http.StatusOK
