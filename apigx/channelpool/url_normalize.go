package channelpool

import "strings"

// NormalizeUpstreamBaseURL 把用户填写的上游 Base URL 规范成**底座期望的形式**。
//
// # 为什么需要它（真实 API 回扫发现）
//
// 用户自然会填"API 基址"这种写法——OpenAI 官方文档就把 `https://api.openai.com/v1`
// 称作 base URL。但底座的 OpenAI 适配器会在 base_url 之后再拼 `/v1/chat/completions`，
// 于是 `https://host/v1` 会变成 `https://host/v1/v1/chat/completions` →
// 上游返回 404（实测真实上游的响应体是它自己的 `{"message":"接口不存在"}`）。
//
// 这种错**很难自查**：平台侧一切正常、上游也确实被访问到了，只有一个 404，
// 而错误信息来自上游、看起来像"上游不支持"。因此这里主动归一化：
// 对会在后面拼 `/v1/...` 的协议族，把结尾多余的 `/v1` 去掉一层。
//
// # 保守边界
//
// 只在**结尾恰好是 `/v1`**（允许结尾斜杠）时处理，且只处理一层：
//   - `https://host/v1`      → `https://host`
//   - `https://host/v1/`     → `https://host`
//   - `https://host/api/v1`  → `https://host/api`（该路径下 OpenAI 兼容端点通常就是 /api/v1/...）
//   - `https://host`         → 原样
//   - `https://host/api`     → 原样（不敢猜它是不是等价于 /v1）
//
// 非 OpenAI 系的协议（如 Anthropic 用 `/v1/messages`）同样由各自适配器拼 `/v1/...`，
// 因此一视同仁；若将来遇到"上游根路径本身就含 /v1 且适配器不再拼"的情况，
// 应由调用方显式关闭归一化，而不是在这里猜。
func NormalizeUpstreamBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	// 去掉结尾斜杠（保留协议部分的 `//`）
	withoutTrailing := strings.TrimRight(trimmed, "/")
	if withoutTrailing == "" {
		return trimmed
	}
	if !strings.HasSuffix(withoutTrailing, "/v1") {
		return withoutTrailing
	}
	base := strings.TrimRight(strings.TrimSuffix(withoutTrailing, "/v1"), "/")
	if base == "" {
		return withoutTrailing
	}
	// 只处理带协议或明显是主机名的情况，避免把相对路径改坏
	if !strings.Contains(base, "://") && !strings.Contains(base, ".") && !strings.HasPrefix(base, "localhost") {
		return withoutTrailing
	}
	return base
}
