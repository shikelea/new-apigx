package modelfp

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
)

// 型号指纹维度的判定完全照搬 meow-llm-detector（gpt56_vnext）：题库、采样份数、
// 60% 完成率规则、校准阈值全部来自它发布的官方基准包，评分是 predictive.py 的
// score_counts 逐行移植。这里只做两件参考项目没有的事：把包嵌进二进制，以及把
// 「用户填的请求名」对应到包里的型号 ID。
//
// 每个基准包绑定一种采集协议（mode）：chat=/v1/chat/completions，gpt=OpenAI 原生
// /v1/responses，claude=Anthropic 原生 /v1/messages。检测时必须用和基准相同的协议采样，
// 分布才可比，所以同一型号同时有原生包和 Chat 兼容包时，由采集端先试原生、不通再回退。

const (
	meowScoringVersion = "meow-fingerprint-v3-predictive"
	meowCalibAlgorithm = "predictive-source-path-pcg64-v1"
	// MeowOtherID 是基准包里代表「其他已知外部型号」的虚拟参照。
	MeowOtherID = "other_known_external"
	// MeowInvalidOutput 是归一化失败的类目，单独计数，不进入分布。
	MeowInvalidOutput = "__INVALID_OUTPUT__"
	meowUnseen        = "__UNSEEN_IN_TRAINING__"
	// meowMaxAnswerLength 对应 normalizers.MAX_ANSWER_LENGTH。
	meowMaxAnswerLength = 65536
	// MeowDefaultTier 是在线检测的默认档位。高档样本充足但请求更多（约 340 次），
	// 中档平衡速度与准确（约 120 次），低档最快但容易证据不足。
	MeowDefaultTier = "high"
)

//go:embed meowdata/*.meow.json
var meowFS embed.FS

// meowFiles 钉死嵌入文件的 sha256，改一个字节都拒绝加载。所有包都是官方基准：
// 数据经官方转发采集，采集与校准流程同参考项目。
var meowFiles = map[string]string{
	"meowdata/meow-gpt-chat-compatible--4.5.4-chat.20260924.2.meow.json":    "5c835e5b6646359e33f3f56663b712f214eaa7eda1108088f3a3764bc0b4db72",
	"meowdata/meow-claude-chat-compatible--4.5.4-chat.20260924.1.meow.json": "db6728ff00d28f58ed21079ce66a82f70ba226a6530c52c5630fddd5cbf963da",
	// 原生协议包：拟合、档位、型号与对应的 Chat 兼容包相同，只是采集协议不同。
	"meowdata/meow-gpt-other-cap98-efficient--4.5.4-predictive.20260924.2.meow.json":    "98f8d12c83100352addf44db15d8b57aa183338a4fb5f83ba30ffdbc06d78612",
	"meowdata/meow-claude-other-cap98-efficient--4.5.4-predictive.20260924.1.meow.json": "df005a15ef72cc805b57fd0caf8c8f6b99496ca37d73c6cc759adaa43158ef8c",
	// 自采集基准包：经 tokenrhythm.studio/v1 中转采集。两个包的 id、version 相同，靠文件名区分。
	// A 包（f44a29d558ce6899）只保留了通过校准的 high 档；B 包（ab763b91fa29b8a6）三档都未通过
	// 校准（target_not_met），并已从型号与阈值里删除 mimo-v2.6-pro / mimo-v2.6-flash。
	"meowdata/f44a29d558ce6899.meow.json": "ecc1cbdd37c524c01bd87446f58fb4f45358c89b968dbefaf9307eee154ec97a",
	"meowdata/ab763b91fa29b8a6.meow.json": "fead40a6ec89ef871f990f33f43103abbedddefd75441d5361b49fb823c09b9c",
}

// 基准包的采集协议，对应参考项目 protocol.build_payload 的 mode。
const (
	MeowModeChat   = "chat"
	MeowModeGPT    = "gpt"
	MeowModeClaude = "claude"
)

type meowModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RequestModel  string `json:"request_model"`
	ReferenceOnly bool   `json:"reference_only"`
}

type meowNormalizer struct {
	ID         string `json:"id"`
	Parameters struct {
		MaxLength int `json:"max_length"`
	} `json:"parameters"`
}

type meowCellSpec struct {
	ID         string `json:"id"`
	System     string `json:"system"`
	Prompt     string `json:"prompt"`
	History    []any  `json:"history"`
	Effort     string `json:"effort"`
	Profile    string `json:"profile"`
	Parameters struct {
		MaxOutputTokens int    `json:"max_output_tokens"`
		ChatTokenField  string `json:"chat_token_field"`
	} `json:"parameters"`
}

type meowProbe struct {
	ID         string         `json:"id"`
	Normalizer meowNormalizer `json:"normalizer"`
	Cells      []meowCellSpec `json:"cells"`
}

type meowTier struct {
	Counts     map[string]int     `json:"counts"`
	Thresholds map[string]float64 `json:"thresholds"`
}

type meowFittedCell struct {
	Categories     []string             `json:"categories"`
	Alpha          map[string][]float64 `json:"alpha"`
	SourceSamples  map[string]int       `json:"source_samples"`
	ReferenceReady bool                 `json:"reference_ready"`
}

type meowFitted struct {
	ScoringVersion   string                    `json:"scoring_version"`
	PriorMass        float64                   `json:"prior_mass"`
	Models           []string                  `json:"models"`
	Sources          []string                  `json:"sources"`
	ReferenceSources []string                  `json:"reference_sources"`
	Aggregation      string                    `json:"aggregation"`
	Cells            map[string]meowFittedCell `json:"cells"`
}

type meowCalibCase struct {
	Scope     string `json:"scope"`
	Source    string `json:"source"`
	Pattern   string `json:"pattern"`
	Batches   int    `json:"batches"`
	Qualified int    `json:"qualified"`
	Wrong     int    `json:"wrong"`
	Strong    int    `json:"strong"`
}

type meowCalibResult struct {
	Status              string                    `json:"status"`
	Counts              map[string]int            `json:"counts"`
	Thresholds          map[string]float64        `json:"thresholds"`
	CorrectTarget       float64                   `json:"correct_target"`
	PerSourceBatches    map[string]int            `json:"per_source_batches"`
	SourceOutcomes      map[string]map[string]int `json:"source_outcomes"`
	PartialVerification struct {
		MinimumSampleRatio float64         `json:"minimum_sample_ratio"`
		SameAnswerPrefixes bool            `json:"same_answer_prefixes"`
		Patterns           []string        `json:"patterns"`
		BatchesPerSource   int             `json:"batches_per_source"`
		MaxPathWrong       float64         `json:"max_path_wrong"`
		Cases              []meowCalibCase `json:"cases"`
	} `json:"partial_verification"`
}

type meowPackage struct {
	Mode     string      `json:"mode"`
	ID       string      `json:"id"`
	Version  string      `json:"version"`
	Models   []meowModel `json:"models"`
	Probes   []meowProbe `json:"probes"`
	Metadata struct {
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	} `json:"metadata"`
	Engine struct {
		ScoringVersion  string  `json:"scoring_version"`
		CompletionRatio float64 `json:"completion_ratio"`
	} `json:"engine"`
	Tiers       map[string]meowTier `json:"tiers"`
	Fitted      meowFitted          `json:"fitted"`
	Calibration struct {
		Status string `json:"status"`
		Tiers  map[string]struct {
			Algorithm string          `json:"algorithm"`
			Result    meowCalibResult `json:"result"`
		} `json:"tiers"`
	} `json:"calibration"`

	// 以下为加载后派生的字段。
	// file 是嵌入文件名，是包的唯一标识（自采集的两个包 id、version 相同）。
	file       string
	cells      map[string]MeowCell
	calibrated map[string]bool
}

// MeowCell 是一道要发给上游的题。
type MeowCell struct {
	ID              string
	System          string
	Prompt          string
	Effort          string
	MaxOutputTokens int
	ChatTokenField  string
	normalizer      meowNormalizer
}

var (
	meowOnce     sync.Once
	meowPackages []*meowPackage
	meowErr      error
)

// LoadMeowPackages 加载并校验嵌入的基准包。重复调用只加载一次。
func LoadMeowPackages() error {
	meowOnce.Do(func() {
		names := make([]string, 0, len(meowFiles))
		for name := range meowFiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			pkg, err := loadMeowPackage(name, meowFiles[name])
			if err != nil {
				meowErr = fmt.Errorf("%s: %w", name, err)
				meowPackages = nil
				return
			}
			pkg.file = name
			meowPackages = append(meowPackages, pkg)
		}
	})
	return meowErr
}

func loadedMeowPackages() []*meowPackage {
	if LoadMeowPackages() != nil {
		return nil
	}
	return meowPackages
}

func loadMeowPackage(name, digest string) (*meowPackage, error) {
	raw, err := meowFS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, fmt.Errorf("sha256 mismatch")
	}
	var pkg meowPackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, err
	}
	switch pkg.Mode {
	case MeowModeChat, MeowModeGPT, MeowModeClaude:
	default:
		return nil, fmt.Errorf("unsupported package mode %q", pkg.Mode)
	}
	if pkg.Engine.ScoringVersion != meowScoringVersion || pkg.Fitted.ScoringVersion != meowScoringVersion {
		return nil, fmt.Errorf("unsupported package mode or scoring version")
	}
	if pkg.Engine.CompletionRatio <= 0 || pkg.Engine.CompletionRatio > 1 {
		return nil, fmt.Errorf("invalid completion ratio")
	}
	pkg.cells = map[string]MeowCell{}
	for _, probe := range pkg.Probes {
		switch probe.Normalizer.ID {
		case "exact_trimmed_casefold", "exact_trimmed", "whitespace_collapse":
		default:
			return nil, fmt.Errorf("unsupported normalizer %q", probe.Normalizer.ID)
		}
		if probe.Normalizer.Parameters.MaxLength == 0 {
			probe.Normalizer.Parameters.MaxLength = 128
		}
		for _, cell := range probe.Cells {
			if len(cell.History) != 0 || cell.Profile == "codex-like" {
				return nil, fmt.Errorf("unsupported probe context %s", cell.ID)
			}
			// 对应 benchmark.py：claude-code 画像只属于 Claude 原生协议。
			if cell.Profile == "claude-code" && pkg.Mode != MeowModeClaude {
				return nil, fmt.Errorf("profile %s not allowed in %s mode", cell.Profile, pkg.Mode)
			}
			spec := MeowCell{ID: cell.ID, System: cell.System, Prompt: cell.Prompt, Effort: cell.Effort,
				MaxOutputTokens: cell.Parameters.MaxOutputTokens, ChatTokenField: cell.Parameters.ChatTokenField,
				normalizer: probe.Normalizer}
			if spec.System == "" {
				spec.System = "."
			}
			if spec.Effort == "" {
				spec.Effort = "low"
			}
			if spec.MaxOutputTokens == 0 {
				spec.MaxOutputTokens = 256
			}
			if spec.ChatTokenField == "" {
				spec.ChatTokenField = "max_tokens"
			}
			pkg.cells[cell.ID] = spec
		}
	}
	// 拟合结果的形状必须自洽，否则评分时会越界。
	if len(pkg.Fitted.Models) == 0 {
		return nil, fmt.Errorf("fitted models empty")
	}
	positions := map[string]bool{}
	for _, source := range pkg.Fitted.Sources {
		positions[source] = true
	}
	for _, model := range pkg.Fitted.Models {
		if model != MeowOtherID && !positions[model] {
			return nil, fmt.Errorf("fitted model %s has no source", model)
		}
	}
	for _, source := range pkg.Fitted.ReferenceSources {
		if !positions[source] {
			return nil, fmt.Errorf("reference source %s missing", source)
		}
	}
	for identity, cell := range pkg.Fitted.Cells {
		for _, source := range pkg.Fitted.Sources {
			if len(cell.Alpha[source]) != len(cell.Categories) {
				return nil, fmt.Errorf("cell %s alpha shape mismatch", identity)
			}
		}
	}
	pkg.calibrated = map[string]bool{}
	for tier := range pkg.Tiers {
		pkg.calibrated[tier] = pkg.calibrationMatches(tier)
	}
	return &pkg, nil
}

// calibrationMatches 移植 predictive_calibration.calibration_matches 的结构性检查。
// 参考实现最后还要比对一份 canonical JSON 的 contract 哈希；这里改为在加载时钉死整
// 个文件的 sha256，保证文件未被改动，但不重算 contract 哈希（自采集包是在校准后手工
// 删改过的，原 contract 已对不上）。
func (p *meowPackage) calibrationMatches(tier string) bool {
	entry, ok := p.Calibration.Tiers[tier]
	planned, okTier := p.Tiers[tier]
	if !ok || !okTier || p.Calibration.Status != "calibrated" {
		return false
	}
	result := entry.Result
	models, sources := p.Fitted.Models, p.Fitted.Sources
	if entry.Algorithm != meowCalibAlgorithm || result.Status != "target_met" ||
		!equalIntMaps(result.Counts, planned.Counts) || !equalFloatMaps(result.Thresholds, planned.Thresholds) ||
		!sameKeys(planned.Thresholds, models) || result.CorrectTarget < .5 || result.CorrectTarget > 1 ||
		!sameKeys(result.SourceOutcomes, sources) || !sameKeys(result.PerSourceBatches, sources) {
		return false
	}
	for _, threshold := range planned.Thresholds {
		if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < .5 || threshold > .98 {
			return false
		}
	}
	for _, source := range sources {
		n := result.PerSourceBatches[source]
		counts := result.SourceOutcomes[source]
		if n <= 0 || len(counts) != 3 {
			return false
		}
		sum := 0
		for _, key := range []string{"correct", "wrong", "insufficient"} {
			value, present := counts[key]
			if !present || value < 0 {
				return false
			}
			sum += value
		}
		if sum != n || float64(counts["correct"])/float64(n) < result.CorrectTarget {
			return false
		}
	}
	partial := result.PartialVerification
	if partial.MinimumSampleRatio != .6 || !partial.SameAnswerPrefixes || len(partial.Patterns) == 0 ||
		partial.BatchesPerSource <= 0 || partial.MaxPathWrong < 0 || partial.MaxPathWrong >= .5 || len(partial.Cases) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range partial.Cases {
		identity := c.Scope + "\x00" + c.Source + "\x00" + c.Pattern
		if seen[identity] || !(0 <= c.Wrong && c.Wrong <= c.Strong && c.Strong <= c.Qualified && c.Qualified <= c.Batches) || c.Batches <= 0 ||
			(c.Qualified > 0 && float64(c.Wrong)/float64(c.Qualified) > partial.MaxPathWrong) {
			return false
		}
		seen[identity] = true
	}
	for _, source := range sources {
		for _, pattern := range partial.Patterns {
			if !seen["check\x00"+source+"\x00"+pattern] {
				return false
			}
		}
	}
	return true
}

func equalIntMaps(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func equalFloatMaps(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func sameKeys[V any](values map[string]V, keys []string) bool {
	if len(values) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := values[key]; !ok {
			return false
		}
	}
	return true
}
