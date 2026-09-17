package apigxapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 能力矩阵：**支持范围的事实来源**（端点、是否支持、已知限制）。
//
// 依据计划书：支持范围逐项记录；尚未支持的维度不能在界面宣称已生效。
// 因此界面/文档/验收脚本都从这里取，避免各自发挥。取值只有两种：
//   - `supported`：可用（并列出已知限制）；
//   - `unsupported`：明确不支持，界面必须显示能力限制。
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
			"上游返回**签名直链**（有效期内持有者可直接访问）：若要求“结果仅授权者可见”，需我方代理或转存",
		},
	},
	{
		Capability: "embedding", Endpoints: []string{"/v1/embeddings"},
		Status:   "supported",
		Evidence: "复用底座适配器；已验协议往返、usage 计量与规格拒绝",
	},
	{
		Capability: "rerank", Endpoints: []string{"/v1/rerank"},
		Status:   "supported",
		Evidence: "复用底座适配器；已验查询/文档往返、计量与空文档拒绝",
	},
	{
		Capability: "audio", Endpoints: []string{"/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech"},
		Status:   "supported",
		Evidence: "复用底座适配器；已验转写往返与计量；缺文件返回 4xx（台账 D-024）",
	},
	{
		Capability: "video", Endpoints: []string{"/v1/videos", "/v1/videos/:id", "/v1/videos/:id/content", "/v1/videos/:id/cancel"},
		Status:   "supported",
		Evidence: "复用底座内置 kling 任务插件：提交/轮询/终态/绑定/跨用户/结果访问 8/8（台账 D-022）",
		Limitations: []string{
			"**不支持取消**：`/v1/videos/:id/cancel` 返回 404 —— 界面必须显示该能力限制，不得显示“可取消”",
			"通用任务 API `/v1/tasks` 在本配置下**未启用**（404）",
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
			"supported":   "可用（限制见 limitations）",
			"unsupported": "明确不支持",
		},
	})
}

// 供路由注册使用（保持 handler 风格一致）
var _ = http.StatusOK
