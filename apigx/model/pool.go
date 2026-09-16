package apigxmodel

import (
	"errors"
	"fmt"
	"strings"
)

// 本文件只保留**底座没有**的那部分能力：批量导入文本的解析。
//
// # 为什么这里没有选择策略
//
// 底座的 `model.Channel.GetNextEnabledKey` 已经实现了多 Key 的选择与健康：
//   - 轮询（`MultiKeyModePolling`，游标存在渠道缓存里、线程安全）/ 随机（`MultiKeyModeRandom`）；
//   - 跳过逐个 Key 的禁用状态（`MultiKeyStatusList`，含禁用原因与时间）；
//   - 全部 Key 都不可用时返回明确错误（`ErrorCodeChannelNoAvailableKey`），而不是硬用一把；
//   - 单 Key 故障隔离与「全部失效才停渠道」见 `model/channel.go` 的 `handlerMultiKeyUpdate`。
//
// 而且底座在 `SetupContextForSelectedChannel` 里已经完成了**唯一的选路决定**
// （计划书 §9.2 要求「一个权威决定点」）。在 APIGX 侧再写一套选择器会制造第二个决定点，
// 也会与底座的轮询游标互相竞争——因此这里**不提供**选择函数，选择一律交给底座。
//
// APIGX 自己的领域表（`apigx_upstream_keys`）负责的是底座表达不了的东西：
// 稳定凭据 ID（跨轮换保持身份，底座用位置索引，删一把就整体错位）、分享者归属、
// 逐 Key 容量策略、加密存储（底座 `channels.key` 是明文），以及逐 Key 统计。

// 分享渠道的池策略取值（`apigx_channels.key_pool_mode`）。
//
// **只映射到底座已有的两种选择方式**——用户已裁决不为「加权/剩余容量」扩展底座。
// 这里定义的是我们列里的枚举值，真正的选择由 `model.Channel.GetNextEnabledKey` 执行。
const (
	// KeyPoolModePolling 轮询（默认）。
	KeyPoolModePolling int = 0
	// KeyPoolModeRandom 随机。
	KeyPoolModeRandom int = 1
)

// MaxImportBatch 是单次批量导入允许的 Key 数量上限。
const MaxImportBatch = 200

// importedKeyMinLength 是接受的最短凭据长度（明显短于此值的多半是粘贴错误）。
const importedKeyMinLength = 8

// ErrInvalidArgument 之外的导入错误统一用这个哨兵，便于接口层映射为 400。
var ErrImportRejected = errors.New("apigx: import rejected")

// ImportedKeys 是批量导入的解析结果。
type ImportedKeys struct {
	// Accepted 是可用的凭据明文，按首次出现顺序。
	Accepted []string
	// DuplicateLines 是批内重复的行号（1-based），便于回显给分享者。
	DuplicateLines []int
	// InvalidLines 是格式不合法的行号（1-based）。
	InvalidLines []int
}

// ParseImportedKeys 解析批量粘贴的凭据文本。
//
// 规则：一行一把；忽略空行与以 `#` 开头的注释行；去掉首尾空白；批内去重（保留首次出现）；
// 单批超过 MaxImportBatch 直接拒绝。底座的批量导入是写在渠道控制器里的内联逻辑，
// 无法作为库复用；这里对齐它的行为（去空白、丢空行），并额外做批内去重与行号回显。
//
// 注意：这里只检测**批内**重复。与库里已有凭据的重复由
// `UpstreamKey.SecretFingerprint` 的唯一索引兜住（密文带随机 nonce，无法比对密文）。
func ParseImportedKeys(raw string) (ImportedKeys, error) {
	result := ImportedKeys{}
	seen := make(map[string]struct{})

	lineNo := 0
	for line := range strings.SplitSeq(raw, "\n") {
		lineNo++
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(trimmed) < importedKeyMinLength || len(trimmed) > 512 {
			result.InvalidLines = append(result.InvalidLines, lineNo)
			continue
		}
		if _, dup := seen[trimmed]; dup {
			result.DuplicateLines = append(result.DuplicateLines, lineNo)
			continue
		}
		seen[trimmed] = struct{}{}
		result.Accepted = append(result.Accepted, trimmed)
		if len(result.Accepted) > MaxImportBatch {
			return ImportedKeys{}, fmt.Errorf("%w: at most %d keys per import", ErrImportRejected, MaxImportBatch)
		}
	}
	return result, nil
}
