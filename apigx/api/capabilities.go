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
	// Verified/NotVerified 按计划书阶段 3 的**逐项验收范围**记录事实：
	// 哪些子项真的被跑过、哪些没有。声明"没验过"与"验过"同样重要。
	Verified    []string `json:"verified"`
	NotVerified []string `json:"not_verified,omitempty"`
}

// capabilityMatrix 是矩阵本体。改动它时必须同步更新 evidence 与 limitations，
// 因为它是界面"能宣称什么"的依据。
var capabilityMatrix = []capabilityEntry{
	{
		Capability: "chat", Endpoints: []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"},
		Status:   "supported",
		Evidence: "真实上游端到端 5/5：同步、SSE 流式、工具调用、Anthropic Messages、Responses（含视觉输入）",
		Verified: []string{
			"OpenAI Chat Completions 同步与 SSE（真实上游）",
			"工具调用（真实上游）",
			"Anthropic Messages（真实上游）",
			"Responses（真实上游）",
			"视觉输入（真实上游）",
		},
		NotVerified: []string{
			"Gemini 原生文本路径（未联调）",
		},
	},
	{
		Capability: "image", Endpoints: []string{"/v1/images/generations", "/v1/images/edits"},
		Status:   "supported",
		Evidence: "真实上游端到端：生成 200、用量含配额、非法尺寸 400；尺寸语法与范围已加固（台账 D-021）",
		Verified: []string{
			"生成（真实上游，qwen-image-2.0）",
			"尺寸语法与范围校验（台账 D-021）",
			"结果访问需鉴权",
			"计量（用量记录含配额）",
			"数量参数 n 的上限 128（超限 400，实测 n=129）",
		},
		NotVerified: []string{
			"图片编辑（未与真实上游联调）",
		},
		Limitations: []string{
			"上游返回**签名直链**（有效期内持有者可直接访问）：若要求“结果仅授权者可见”，需我方代理或转存",
		},
	},
	{
		Capability: "embedding", Endpoints: []string{"/v1/embeddings"},
		Status:   "supported",
		Evidence: "复用底座适配器；已验协议往返、usage 计量与规格拒绝",
		Verified: []string{
			"批量 embeddings 协议往返",
			"向量维度",
			"usage 计量与落库",
			"输入条数上限 2048（超限 400，实测 2049 条）",
		},
	},
	{
		Capability: "rerank", Endpoints: []string{"/v1/rerank"},
		Status:   "supported",
		Evidence: "复用底座适配器；已验查询/文档往返、计量与空文档拒绝",
		Verified: []string{
			"查询/文档列表往返与排序结果",
			"计量落库",
			"空文档列表被拒",
			"文档条数上限 1000（超限 400，实测 1001 条）",
		},
	},
	{
		Capability: "audio", Endpoints: []string{"/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech"},
		Status:   "supported",
		Evidence: "复用底座适配器；已验转写往返与计量；缺文件返回 4xx（台账 D-024）",
		Verified: []string{
			"转写协议往返与计量落库",
			"语音合成（speech）返回音频与计量落库",
			"缺少音频文件被拒（4xx，台账 D-024）",
			"上传大小上限 25 MiB（超限 400，台账 D-026）",
		},
		NotVerified: []string{
			"上传音频的**时长**限制（由上游判定，平台不做音频解码）",
		},
	},
	{
		Capability: "video", Endpoints: []string{"/v1/videos", "/v1/videos/:id", "/v1/videos/:id/content", "/v1/videos/:id/cancel"},
		Status:   "supported",
		Evidence: "复用底座内置 kling 任务插件：提交/轮询/终态/绑定/跨用户/结果访问 8/8（台账 D-022）",
		Verified: []string{
			"提交与轮询到成功终态",
			"失败终态如实反映",
			"任务绑定正确用户与原渠道",
			"跨用户访问被拒",
			"结果内容访问（授权者取回）",
		},
		NotVerified: []string{
			"超时路径（未构造真实超时）",
		},
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
