package dto

type ChannelPinSource string

const (
	PinSourceToken      ChannelPinSource = "token"       // Rank 0, highest
	PinSourceOriginTask ChannelPinSource = "origin_task" // Rank 10
)

const (
	PinRankToken      = 0
	PinRankOriginTask = 10
)

type PinRetryMode int

const (
	PinRetrySameChannel PinRetryMode = iota
	PinRetrySingleAttempt
)

func (m PinRetryMode) Stricter(other PinRetryMode) PinRetryMode {
	if m == PinRetrySingleAttempt || other == PinRetrySingleAttempt {
		return PinRetrySingleAttempt
	}
	return PinRetrySameChannel
}

type ChannelPin struct {
	ChannelId int
	Source    ChannelPinSource
	Rank      int
	RetryMode PinRetryMode
}

type ChannelFilterKind string

const (
	FilterRequestPath        ChannelFilterKind = "request_path"
	FilterTaskPluginIdentity ChannelFilterKind = "task_plugin_identity"
	FilterResponsesWebSocket ChannelFilterKind = "responses_websocket"
	// FilterDeniedChannelIds 排除列出的渠道 ID。APIGX 用它在不影响管理员自建渠道的前提下，
	// 从亲和、随机选择和失败重试中移除调用者无权访问的分享渠道。
	FilterDeniedChannelIds ChannelFilterKind = "denied_channel_ids"
	// FilterAllowedChannelIds 只保留列出的渠道 ID。用于**请求级**的候选限定：
	// 调用方（例如 APIGX 的路由策略）已经算好"本次该用哪些渠道"，这里只表达结果，
	// 不把排序逻辑塞进选择器（台账 D-020）。
	FilterAllowedChannelIds ChannelFilterKind = "allowed_channel_ids"
)

type ChannelFilter struct {
	Kind                   ChannelFilterKind
	RequestPath            string
	TaskPluginKey          string
	TaskPluginKeys         []string
	TaskPluginChannelTypes []int
	// AllowedChannelIds 仅当 Kind == FilterAllowedChannelIds 时使用；**空列表表示不允许任何渠道**
	// （与"没有这个过滤器"是两回事：前者是明确的"没有合格候选"）。
	AllowedChannelIds []int
	// DeniedChannelIds 仅当 Kind == FilterDeniedChannelIds 时使用。
	DeniedChannelIds []int
}

type ChannelConstraints struct {
	Pins    []ChannelPin
	Filters []ChannelFilter
}

func (cc *ChannelConstraints) AddPin(p ChannelPin) {
	if cc == nil {
		return
	}
	cc.Pins = append(cc.Pins, p)
}

func (cc *ChannelConstraints) AddFilter(f ChannelFilter) {
	if cc == nil {
		return
	}
	cc.Filters = append(cc.Filters, f)
}

// ReplaceAllowedChannelIDs replaces the request-level allow-list filter in place.
// There must be one effective allow-list: multiple allow-list filters are
// conjunctive, so appending a second one would accidentally intersect rather
// than expand the route fallback set.
func (cc *ChannelConstraints) ReplaceAllowedChannelIDs(ids []int) bool {
	if cc == nil {
		return false
	}
	clean := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	found := false
	for i := range cc.Filters {
		if cc.Filters[i].Kind != FilterAllowedChannelIds {
			continue
		}
		cc.Filters[i].AllowedChannelIds = append([]int(nil), clean...)
		found = true
	}
	if !found {
		cc.Filters = append(cc.Filters, ChannelFilter{
			Kind: FilterAllowedChannelIds, AllowedChannelIds: append([]int(nil), clean...),
		})
	}
	return true
}

// DeniedChannelIDSet returns all channels excluded by request-level filters.
func (cc *ChannelConstraints) DeniedChannelIDSet() map[int]struct{} {
	denied := make(map[int]struct{})
	if cc == nil {
		return denied
	}
	for _, filter := range cc.Filters {
		if filter.Kind != FilterDeniedChannelIds {
			continue
		}
		for _, id := range filter.DeniedChannelIds {
			if id > 0 {
				denied[id] = struct{}{}
			}
		}
	}
	return denied
}

// ResolvedPin returns the winning pin after priority resolution.
// Lowest Rank wins. Pins that name the same channel are merged (stricter RetryMode).
// overridden lists pins that lost to a different channel (for warn logging).
func (cc *ChannelConstraints) ResolvedPin() (ChannelPin, bool, []ChannelPin) {
	if cc == nil || len(cc.Pins) == 0 {
		return ChannelPin{}, false, nil
	}

	merged := make(map[int]ChannelPin, len(cc.Pins))
	order := make([]int, 0, len(cc.Pins))
	for _, pin := range cc.Pins {
		existing, seen := merged[pin.ChannelId]
		if !seen {
			merged[pin.ChannelId] = pin
			order = append(order, pin.ChannelId)
			continue
		}
		existing.RetryMode = existing.RetryMode.Stricter(pin.RetryMode)
		if pin.Rank < existing.Rank {
			existing.Rank = pin.Rank
			existing.Source = pin.Source
		}
		merged[pin.ChannelId] = existing
	}

	winner := merged[order[0]]
	for _, channelID := range order[1:] {
		candidate := merged[channelID]
		if candidate.Rank < winner.Rank {
			winner = candidate
		}
	}

	var overridden []ChannelPin
	for _, channelID := range order {
		candidate := merged[channelID]
		if candidate.ChannelId != winner.ChannelId {
			overridden = append(overridden, candidate)
		}
	}
	return winner, true, overridden
}

func (cc *ChannelConstraints) SuppressesRetry() bool {
	pin, found, _ := cc.ResolvedPin()
	return found && pin.RetryMode == PinRetrySingleAttempt
}
