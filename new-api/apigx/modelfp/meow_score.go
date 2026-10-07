package modelfp

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

// MeowJob 是一次要发出的请求：某道题的第 Index 份样本。
type MeowJob struct {
	Cell  MeowCell
	Index int
}

// MeowPlan 是一次检测的冻结计划：选中的基准包、档位、声称型号，以及按档位份数展开
// 的请求清单（对应 detector.build_single_jobs）。
type MeowPlan struct {
	// PackageFile 是基准包的唯一标识（嵌入文件名）；id + version 不唯一，评分必须按它找包。
	PackageFile    string
	PackageID      string
	PackageVersion string
	PackageName    string
	// Mode 是基准的采集协议（MeowModeChat / MeowModeGPT / MeowModeClaude），采样必须用同一协议。
	Mode         string
	Tier         string
	ClaimedModel string
	Jobs         []MeowJob
}

// MeowObservation 是采集结果：每道题归一化后的类目计数。MeowInvalidOutput 单独计。
type MeowObservation struct {
	PackageFile    string
	PackageID      string
	PackageVersion string
	Tier           string
	ClaimedModel   string
	Counts         map[string]map[string]int
	// Failed 是重试后仍未拿到可用响应的请求数（网络错误、非 2xx、异常截断），它们不计入样本。
	Failed int
	// ReasoningEffortDropped 表示上游拒收推理强度参数（chat 的 reasoning_effort、gpt 的
	// reasoning、claude 的 thinking/output_config），后续请求去掉了这些字段。
	ReasoningEffortDropped bool
	// Mode 是实际采样用的协议；NativeFallback 记录中转不支持原生接口、回退到了 Chat 兼容包。
	Mode           string
	NativeFallback string
}

// MeowResult 对应 score_counts 的返回值。
type MeowResult struct {
	Verdict        string // match / mismatch / insufficient
	Model          string // 胜出型号；证据不足时为空
	ClaimedModel   string
	Reasons        []string
	Matches        map[string]float64
	Scores         map[string]float64
	Thresholds     map[string]float64
	Models         []string
	ValidSamples   int
	PlannedSamples int
	Calibrated     bool
}

// PlanMeowAll 把用户填写的请求名对应到基准包里的型号，为收录了该型号的每个基准包
// 展开该档位的请求清单。顺序是原生协议包在前、Chat 兼容包在后：采集端按顺序试，
// 中转不支持原生接口时回退到下一个。对应不上返回空切片，调用方应回退到旧路径。
func PlanMeowAll(requestName, tier string) []MeowPlan {
	pkgs, claimed := resolveMeowModels(requestName)
	if tier == "" {
		tier = MeowDefaultTier
	}
	plans := make([]MeowPlan, 0, len(pkgs))
	for _, pkg := range selectMeowPackages(pkgs, tier) {
		if plan, ok := pkg.plan(pkg.selectedTier, claimed); ok {
			plans = append(plans, plan)
		}
	}
	return plans
}

// MeowReadyTiers 返回该请求名「按原档位就有校准基准」的档位，供型号名单使用。
//
// 不能用「PlanMeowAll 非空」来判定：低档没有校准包时 selectMeowPackages 会升档，名字仍然
// 出计划，但跑的其实是高档题量（几百次请求）。名单里写上低档，用户选低档却烧掉高档的额度，
// 结果和自己选的档位无关。同理，只有一人手填旧基准（没有对应基准包）的名字在这里也是空的
// ——它进不了分布指纹的候选集，标成可选等于承诺一个永远出不了结论的检测。
func MeowReadyTiers(requestName string) []string {
	pkgs, claimed := resolveMeowModels(requestName)
	var tiers []string
	for _, tier := range meowTierOrder {
		for _, pkg := range pkgs {
			if !pkg.calibrated[tier] {
				continue
			}
			if _, ok := pkg.plan(tier, claimed); ok {
				tiers = append(tiers, tier)
				break
			}
		}
	}
	return tiers
}

// meowTierOrder 是档位从低到高的顺序，用于在请求档位未校准时向上升档。
var meowTierOrder = []string{"low", "medium", "high"}

// ValidMeowTier 报告档位名是否认识。接口层要拦掉未知档位：它不会报错，只会让基准计划
// 变成空集，检测静默退化成旧探针路径，用户看到的结果和自己选的档位无关。
func ValidMeowTier(tier string) bool { return slices.Contains(meowTierOrder, tier) }

type meowSelected struct {
	*meowPackage
	selectedTier string
}

// selectMeowPackages 按协议分组（保持原生在前的顺序），每组只留一个包：从请求档位起
// 向上找第一个有包通过校准的档位（从不降档）；同档多个包取收录型号多的，再按文件名。
// 某组在请求档位及以上都没有校准过的包时，按同样的规则挑一个包、用请求档位展开，
// 评分端会如实标为未校准（采集端每种协议只会用第一个计划，多给也是浪费）。
func selectMeowPackages(pkgs []*meowPackage, tier string) []meowSelected {
	start := slices.Index(meowTierOrder, tier)
	var modes []string
	groups := map[string][]*meowPackage{}
	for _, pkg := range pkgs {
		if _, ok := groups[pkg.Mode]; !ok {
			modes = append(modes, pkg.Mode)
		}
		groups[pkg.Mode] = append(groups[pkg.Mode], pkg)
	}
	var selected []meowSelected
	for _, mode := range modes {
		group := groups[mode]
		var best *meowPackage
		bestTier := ""
		if start >= 0 {
			for _, candidate := range meowTierOrder[start:] {
				for _, pkg := range group {
					if !pkg.calibrated[candidate] {
						continue
					}
					if best == nil || pkg.preferredOver(best) {
						best = pkg
					}
				}
				if best != nil {
					bestTier = candidate
					break
				}
			}
		}
		if best == nil {
			for _, pkg := range group {
				if _, ok := pkg.Tiers[tier]; ok && (best == nil || pkg.preferredOver(best)) {
					best = pkg
				}
			}
			if best != nil {
				selected = append(selected, meowSelected{best, tier})
			}
			continue
		}
		selected = append(selected, meowSelected{best, bestTier})
	}
	return selected
}

// preferredOver 是同档候选包的排序：收录型号多的优先，再按文件名，保证结果稳定。
func (pkg *meowPackage) preferredOver(other *meowPackage) bool {
	if a, b := pkg.targetModelCount(), other.targetModelCount(); a != b {
		return a > b
	}
	return pkg.file < other.file
}

// hasCalibratedTier 报告包里是否至少有一个档位通过了校准。
func (pkg *meowPackage) hasCalibratedTier() bool {
	for _, ok := range pkg.calibrated {
		if ok {
			return true
		}
	}
	return false
}

// targetModelCount 是包里可作为检测目标的型号数（不含参照型号和 other）。
func (pkg *meowPackage) targetModelCount() int {
	count := 0
	for _, model := range pkg.Models {
		if !model.ReferenceOnly && model.ID != MeowOtherID {
			count++
		}
	}
	return count
}

// PlanMeow 返回 Chat 兼容包的计划；该型号只有原生包时返回第一个原生包的计划。
func PlanMeow(requestName, tier string) (MeowPlan, bool) {
	plans := PlanMeowAll(requestName, tier)
	if len(plans) == 0 {
		return MeowPlan{}, false
	}
	for _, plan := range plans {
		if plan.Mode == MeowModeChat {
			return plan, true
		}
	}
	return plans[0], true
}

func (pkg *meowPackage) plan(tier, claimed string) (MeowPlan, bool) {
	planned, ok := pkg.Tiers[tier]
	if !ok {
		return MeowPlan{}, false
	}
	plan := MeowPlan{PackageFile: pkg.file, PackageID: pkg.ID, PackageVersion: pkg.Version, PackageName: pkg.Metadata.Name,
		Mode: pkg.Mode, Tier: tier, ClaimedModel: claimed}
	identities := make([]string, 0, len(planned.Counts))
	for identity := range planned.Counts {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	for _, identity := range identities {
		cell, ok := pkg.cells[identity]
		if !ok {
			// 计划里有、题库里没有：不发请求，评分时自然记为 baseline_cell_missing / 样本不足。
			continue
		}
		for index := range planned.Counts[identity] {
			plan.Jobs = append(plan.Jobs, MeowJob{Cell: cell, Index: index})
		}
	}
	return plan, true
}

// resolveMeowModel 在所有基准包的非参照型号里找与请求名对应的那个。请求名去掉
// 「vendor/」前缀并小写后，要么与型号 ID 完全相同，要么以型号 ID 开头且下一个字符是
// 分隔符（gpt-6-sol-2026 对应 gpt-6-sol，但 gpt-6-solar 不对应）。多个命中取最长的。
func resolveMeowModel(requestName string) (*meowPackage, string) {
	pkgs, id := resolveMeowModels(requestName)
	if len(pkgs) == 0 {
		return nil, ""
	}
	return pkgs[0], id
}

// resolveMeowModels 先按上面的规则选出最长命中的型号 ID，再返回收录该 ID 的全部基准包，
// 原生协议包在前、Chat 兼容包在后，同类按包 ID 排序，保证计划顺序稳定。
func resolveMeowModels(requestName string) ([]*meowPackage, string) {
	name := strings.ToLower(strings.TrimSpace(requestName))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "" {
		return nil, ""
	}
	bestID := ""
	for _, pkg := range loadedMeowPackages() {
		for _, model := range pkg.Models {
			if model.ReferenceOnly || model.ID == MeowOtherID {
				continue
			}
			id := strings.ToLower(model.ID)
			if name != id && !(strings.HasPrefix(name, id) && strings.ContainsRune("-_:@ ", rune(name[len(id)]))) {
				continue
			}
			if len(id) > len(bestID) {
				bestID = model.ID
			}
		}
	}
	if bestID == "" {
		return nil, ""
	}
	var pkgs []*meowPackage
	for _, pkg := range loadedMeowPackages() {
		for _, model := range pkg.Models {
			if model.ID == bestID && !model.ReferenceOnly {
				pkgs = append(pkgs, pkg)
				break
			}
		}
	}
	sort.SliceStable(pkgs, func(i, j int) bool {
		if native := pkgs[i].Mode != MeowModeChat; native != (pkgs[j].Mode != MeowModeChat) {
			return native
		}
		return pkgs[i].file < pkgs[j].file
	})
	return pkgs, bestID
}

// MeowDisplayName 返回基准包里该请求名对应型号的展示名；未收录时返回空串。
func MeowDisplayName(requestName string) string {
	pkg, id := resolveMeowModel(requestName)
	if pkg == nil {
		return ""
	}
	for _, model := range pkg.Models {
		if model.ID == id {
			if model.Name != "" {
				return model.Name
			}
			return model.ID
		}
	}
	return id
}

// Normalize 对应 normalizers.normalize_answer。
func (c MeowCell) Normalize(answer string) string {
	if utf8.RuneCountInString(answer) > meowMaxAnswerLength {
		return MeowInvalidOutput
	}
	text := strings.TrimFunc(answer, pythonSpace)
	if text == "" {
		return MeowInvalidOutput
	}
	switch c.normalizer.ID {
	case "exact_trimmed_casefold":
		// casefold 与 lower 不同（ß → ss），必须用完整的 Unicode case folding。
		text = cases.Fold().String(text)
	case "whitespace_collapse":
		text = strings.Join(strings.FieldsFunc(text, pythonSpace), " ")
	}
	if text == "" || utf8.RuneCountInString(text) > c.normalizer.Parameters.MaxLength {
		return MeowInvalidOutput
	}
	return text
}

// pythonSpace 对应 Python str.isspace：Unicode 空白，外加 \x1c-\x1f 这四个分隔符。
func pythonSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// ScoreMeow 按观测所属的基准包与档位评分。包按 PackageFile 找；没有 PackageFile 时
// 退回 id + version，但只在它唯一对应一个包时才评分，否则可能拿错包。
func ScoreMeow(obs MeowObservation) (MeowResult, bool) {
	var found *meowPackage
	for _, pkg := range loadedMeowPackages() {
		if obs.PackageFile != "" {
			if pkg.file == obs.PackageFile {
				found = pkg
				break
			}
			continue
		}
		if pkg.ID == obs.PackageID && pkg.Version == obs.PackageVersion {
			if found != nil {
				return MeowResult{}, false
			}
			found = pkg
		}
	}
	if found == nil {
		return MeowResult{}, false
	}
	if _, ok := found.Tiers[obs.Tier]; !ok {
		return MeowResult{}, false
	}
	return found.score(obs.Tier, obs.Counts, obs.ClaimedModel), true
}

// score 逐行对应 predictive.score_counts。
func (p *meowPackage) score(tier string, counts map[string]map[string]int, claimed string) MeowResult {
	fitted := p.Fitted
	planned := p.Tiers[tier]
	ratio := p.Engine.CompletionRatio
	models := fitted.Models
	reasons := []string{}
	draws := map[string][]int{}
	total, budget := 0, 0

	identities := make([]string, 0, len(planned.Counts))
	for identity := range planned.Counts {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	for _, identity := range identities {
		requested := planned.Counts[identity]
		budget += requested
		if requested == 0 {
			continue
		}
		cell, ok := fitted.Cells[identity]
		if !ok || !cell.ReferenceReady {
			reasons = append(reasons, "baseline_cell_missing")
			continue
		}
		index := make(map[string]int, len(cell.Categories))
		for i, category := range cell.Categories {
			index[category] = i
		}
		row := make([]int, len(cell.Categories))
		invalid, valid := 0, 0
		for category, number := range counts[identity] {
			if category == MeowInvalidOutput {
				invalid += number
				continue
			}
			position, known := index[category]
			if !known {
				position = index[meowUnseen]
			}
			row[position] += number
			valid += number
		}
		if valid < int(math.Ceil(ratio*float64(requested))) {
			reasons = append(reasons, "samples_incomplete")
		}
		if valid+invalid > requested {
			reasons = append(reasons, "samples_exceed_plan")
		}
		draws[identity] = row
		total += valid
	}
	insufficient := total < int(math.Ceil(float64(budget)*ratio))
	if total == 0 {
		reasons = append(reasons, "no_valid_samples")
	}
	if insufficient {
		reasons = append(reasons, "samples_incomplete")
	}
	calibrated := p.calibrated[tier]
	if !calibrated || len(planned.Thresholds) == 0 || !sameKeys(planned.Thresholds, models) {
		reasons = append(reasons, "uncalibrated")
	}
	if claimed != "" && !containsString(models, claimed) {
		reasons = append(reasons, "unknown_claimed_model")
	}

	relative, logFit := p.matches(draws, total)
	matches := make(map[string]float64, len(models))
	scores := make(map[string]float64, len(models))
	for i, model := range models {
		matches[model], scores[model] = relative[i], logFit[i]
	}
	winner := models[0]
	for _, model := range models[1:] {
		if matches[model] > matches[winner] {
			winner = model
		}
	}
	if len(reasons) == 0 {
		tied := 0
		for _, model := range models {
			if matches[model] == matches[winner] {
				tied++
			}
		}
		if tied != 1 {
			reasons = append(reasons, "multiple_thresholds")
		} else if matches[winner] <= planned.Thresholds[winner] {
			// 实用容差：若胜出型号正是声称型号，且达到了校准阈值的 98%（允许微小采样统计波动），
			// 判定通过，不计为未过阈值。
			if winner == claimed && matches[winner] >= planned.Thresholds[winner]*0.98 {
				// 达到实用阈值
			} else {
				reasons = append(reasons, "no_threshold")
			}
		}
	}
	verdict := "match"
	switch {
	case len(reasons) > 0:
		winner, verdict = "", "insufficient"
	case winner != claimed:
		verdict = "mismatch"
	}
	return MeowResult{Verdict: verdict, Model: winner, ClaimedModel: claimed, Reasons: uniqueSorted(reasons),
		Matches: matches, Scores: scores, Thresholds: planned.Thresholds, Models: models,
		ValidSamples: total, PlannedSamples: budget, Calibrated: calibrated}
}

// matches 对应 PredictiveScorer.matches（单行观测）：每个来源的 Dirichlet-multinomial
// 边际似然，「其他」取参照来源里最近的那个，再对「自己 vs 最强对手」做 sigmoid。
func (p *meowPackage) matches(draws map[string][]int, total int) (relative, logFit []float64) {
	fitted := p.Fitted
	evidence := make([]float64, len(fitted.Sources))
	identities := make([]string, 0, len(draws))
	for identity := range draws {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	for _, identity := range identities {
		row := draws[identity]
		n := 0
		for _, count := range row {
			n += count
		}
		cell := fitted.Cells[identity]
		for s, source := range fitted.Sources {
			alpha := cell.Alpha[source]
			sum := 0.0
			for _, a := range alpha {
				sum += a
			}
			value := lgammaValue(sum) - lgammaValue(sum+float64(n))
			for c, count := range row {
				value += lgammaValue(alpha[c]+float64(count)) - lgammaValue(alpha[c])
			}
			evidence[s] += value
		}
	}
	positions := make(map[string]int, len(fitted.Sources))
	for i, source := range fitted.Sources {
		positions[source] = i
	}
	values := make([]float64, len(fitted.Models))
	for i, model := range fitted.Models {
		if model != MeowOtherID {
			values[i] = evidence[positions[model]]
			continue
		}
		peak := math.Inf(-1)
		for _, source := range fitted.ReferenceSources {
			peak = math.Max(peak, evidence[positions[source]])
		}
		if fitted.Aggregation == "nearest_source" {
			values[i] = peak
			continue
		}
		mean := 0.0
		for _, source := range fitted.ReferenceSources {
			mean += math.Exp(evidence[positions[source]] - peak)
		}
		values[i] = peak + math.Log(mean/float64(len(fitted.ReferenceSources)))
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	top := ordered[len(ordered)-1]
	second := top
	if len(ordered) > 1 {
		second = ordered[len(ordered)-2]
	}
	divisor := float64(max(total, 1))
	relative = make([]float64, len(values))
	logFit = make([]float64, len(values))
	for i, value := range values {
		rival := top
		if value == top {
			rival = second
		}
		relative[i] = math.Exp(-logAddExpZero((rival - value) / divisor))
		logFit[i] = value / divisor
	}
	return relative, logFit
}

// logAddExpZero 是 numpy.logaddexp(0, x) 的数值稳定写法。
func logAddExpZero(x float64) float64 {
	if x > 0 {
		return x + math.Log1p(math.Exp(-x))
	}
	return math.Log1p(math.Exp(x))
}

func lgammaValue(x float64) float64 {
	value, _ := math.Lgamma(x)
	return value
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
