package modelfp

// 分布指纹的判定内核。
//
// 思路：语言模型不是随机数发生器。让它「随便挑一个」时，输出分布由训练数据、对齐方式和
// 分词共同决定，同一型号高度可复现，不同型号差别明显。把若干道这类短题的答案分布，和事
// 先实测的基准分布比对，就能判断行为特征更像哪个候选。
//
// 判定用 Dirichlet-multinomial 预测证据，而不是「算个相似度再卡阈值」：
//
//   - 基准样本本身有限，相似度会把「基准只测了 30 次」的不确定性当成确定信息；
//   - 候选集里必须留一个「未建模来源」兜底，否则任何没见过的模型都会被硬塞给最近的候选。
//
// 方法学参考公开资料（LLM 行为指纹、采样偏好指纹），实现与基准数据均自采，没有引入任何
// 第三方基准数据或代码。
import (
	"fmt"
	"math"
	"sort"
)

// unseenCategory 是「基准里没出现过的答案」共用的兜底类目。多个不同的新答案全部并入这
// 一类：把它们拆开会凭空放大差异，而我们并不知道每个新答案各自该有多少先验质量。
const unseenCategory = "\x00unseen"

// referenceCandidate 是「未建模的其他来源」。任何一次检测里，如果观测对所有已建模型号都
// 不像，它应该胜出——这正是「测到的不是名单里任何一个」这一结论的载体。
const referenceCandidate = "other_unmodeled"

// priorMass 是每题的先验总质量，均分给基准类目与一个未见类目。取 1 表示「先验只值一次
// 观测」，基准测了几十次时先验影响很小，基准很稀疏时它会自动压住过度自信的判定。
const priorMass = 1.0

// ProbeCounts 是一道分布题的答案计数：归一化后的答案 -> 次数。
type ProbeCounts map[string]int

// Candidate 是一个参与比对的候选，及其在各题上的基准分布。
type Candidate struct {
	ID       string
	Baseline map[string]ProbeCounts
}

// Evidence 是单个候选在一次检测中的累计证据。
type Evidence struct {
	CandidateID string  `json:"candidate_id"`
	LogEvidence float64 `json:"log_evidence"`
}

// dirichletMultinomialLog 返回在参数 alpha 下观测到 counts 的对数预测概率，
// 省略候选间相同的多项式系数（比较时会抵消）：
//
//	lgamma(Σα) - lgamma(Σα+Σn) + Σ[lgamma(α+n) - lgamma(α)]
func dirichletMultinomialLog(alpha, counts map[string]float64) float64 {
	alphaSum, countSum := 0.0, 0.0
	for _, value := range alpha {
		alphaSum += value
	}
	for _, value := range counts {
		countSum += value
	}
	total := lgamma(alphaSum) - lgamma(alphaSum+countSum)
	for category, value := range alpha {
		observed := counts[category]
		if observed == 0 {
			continue
		}
		total += lgamma(value+observed) - lgamma(value)
	}
	return total
}

func lgamma(x float64) float64 {
	value, _ := math.Lgamma(x)
	return value
}

// probeAlpha 把一个候选在某题上的基准计数，转成 Dirichlet 参数。
func probeAlpha(baseline ProbeCounts) map[string]float64 {
	share := priorMass / float64(len(baseline)+1)
	alpha := make(map[string]float64, len(baseline)+1)
	for category, count := range baseline {
		alpha[category] = float64(count) + share
	}
	alpha[unseenCategory] = share
	return alpha
}

// foldCounts 把一次检测的观测映射到某候选的类目空间：基准里没有的答案并入未见类目。
func foldCounts(observed ProbeCounts, baseline ProbeCounts) map[string]float64 {
	folded := make(map[string]float64, len(observed))
	for answer, count := range observed {
		if _, known := baseline[answer]; known {
			folded[answer] += float64(count)
			continue
		}
		folded[unseenCategory] += float64(count)
	}
	return folded
}

// FingerprintOutcome 是分布指纹的判定结果。
type FingerprintOutcome struct {
	// Best 是证据最高的候选，Runner 是次高的那个。
	Best   string `json:"best"`
	Runner string `json:"runner"`
	// Advantage 是 Best 相对 Runner 的「每份有效答案」平均对数优势。除以样本数是为了
	// 让分数不随采样次数单调上涨：多测几次应当提升可信度，而不是自动抬高分数。
	Advantage float64 `json:"advantage"`
	// Confidence 是 Advantage 过 sigmoid 后的展示值（0–1）。它不是「是该型号的概率」，
	// 只是把优势压到一个可读区间，用于排序与展示。
	Confidence float64 `json:"confidence"`
	// Samples 是参与判定的有效答案总数。
	Samples int `json:"samples"`
	// Ranked 是全部候选的证据，按从高到低排序，便于展示与复核。
	Ranked []Evidence `json:"ranked"`
}

// ScoreFingerprint 比对观测与各候选基准，返回判定结果。
//
// candidates 必须已包含兜底候选（由 registry 装配）。只统计两边都有的题目：基准没测过
// 的题不参与，避免拿缺失当证据。
func ScoreFingerprint(observed map[string]ProbeCounts, candidates []Candidate) (FingerprintOutcome, bool) {
	if len(observed) == 0 || len(candidates) < 2 {
		return FingerprintOutcome{}, false
	}
	samples, scored := 0, make([]Evidence, 0, len(candidates))
	for _, candidate := range candidates {
		total, used := 0.0, 0
		for probeID, counts := range observed {
			baseline, ok := candidate.Baseline[probeID]
			if !ok || len(baseline) == 0 || len(counts) == 0 {
				continue
			}
			total += dirichletMultinomialLog(probeAlpha(baseline), foldCounts(counts, baseline))
			used++
		}
		if used == 0 {
			continue
		}
		scored = append(scored, Evidence{CandidateID: candidate.ID, LogEvidence: total})
	}
	if len(scored) < 2 {
		return FingerprintOutcome{}, false
	}
	// 有效答案数按题累加一次即可，与候选数无关。
	for probeID, counts := range observed {
		if !probeInAnyBaseline(probeID, candidates) {
			continue
		}
		for _, count := range counts {
			samples += count
		}
	}
	if samples == 0 {
		return FingerprintOutcome{}, false
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].LogEvidence == scored[j].LogEvidence {
			return scored[i].CandidateID < scored[j].CandidateID
		}
		return scored[i].LogEvidence > scored[j].LogEvidence
	})
	advantage := (scored[0].LogEvidence - scored[1].LogEvidence) / float64(samples)
	return FingerprintOutcome{
		Best:       scored[0].CandidateID,
		Runner:     scored[1].CandidateID,
		Advantage:  advantage,
		Confidence: 1 / (1 + math.Exp(-advantage)),
		Samples:    samples,
		Ranked:     scored,
	}, true
}

func probeInAnyBaseline(probeID string, candidates []Candidate) bool {
	for _, candidate := range candidates {
		if len(candidate.Baseline[probeID]) > 0 {
			return true
		}
	}
	return false
}

// describeOutcome 把判定结果写成给人看的证据串。
func describeOutcome(claimed string, outcome FingerprintOutcome, threshold float64) (string, string) {
	best := outcome.Best
	switch {
	case outcome.Confidence < threshold:
		return VerdictInconclusive, fmt.Sprintf(
			"%d 份有效答案：最接近 %s，但相对次优候选 %s 的优势未达判定线（%.3f < %.3f），证据不足",
			outcome.Samples, candidateLabel(best), candidateLabel(outcome.Runner), outcome.Confidence, threshold)
	case best == referenceCandidate:
		return VerdictMismatch, fmt.Sprintf(
			"%d 份有效答案：答案分布对名单内任何型号都不贴合，最像「未建模的其他来源」（优势 %.3f，判定线 %.3f）",
			outcome.Samples, outcome.Confidence, threshold)
	case best == claimed:
		return VerdictMatch, fmt.Sprintf(
			"%d 份有效答案：分布与 %s 的实测基准一致，明显优于次优候选 %s（优势 %.3f）",
			outcome.Samples, candidateLabel(best), candidateLabel(outcome.Runner), outcome.Confidence)
	default:
		return VerdictMismatch, fmt.Sprintf(
			"%d 份有效答案：分布更像 %s，而不是所声称的 %s（优势 %.3f，判定线 %.3f）",
			outcome.Samples, candidateLabel(best), candidateLabel(claimed), outcome.Confidence, threshold)
	}
}

func candidateLabel(id string) string {
	if id == referenceCandidate {
		return "未建模的其他来源"
	}
	if entry, ok := LookupRegistry(id); ok {
		return entry.DisplayName
	}
	return id
}
