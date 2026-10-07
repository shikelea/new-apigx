package modelfp

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testdata/meow_parity.json 由参考实现 gpt56_vnext.predictive.score_counts 与
// normalizers.normalize_answer 生成，覆盖两个基准包三个档位、每个来源的模拟样本，以及
// 样本不足、超额、未见类目、无效答案、未知声称型号等分支。
type parityCase struct {
	Package string                    `json:"package"`
	Tier    string                    `json:"tier"`
	Claimed string                    `json:"claimed"`
	Source  string                    `json:"source"`
	Counts  map[string]map[string]int `json:"counts"`
	Verdict string                    `json:"verdict"`
	Model   string                    `json:"model"`
	Reasons []string                  `json:"reasons"`
	Matches map[string]float64        `json:"matches"`
	Scores  map[string]float64        `json:"scores"`
	Valid   int                       `json:"valid"`
	Planned int                       `json:"planned"`
}

func loadParity(t *testing.T) (cases []parityCase, normalize []struct{ In, Out string }) {
	t.Helper()
	raw, err := os.ReadFile("testdata/meow_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Cases     []parityCase `json:"cases"`
		Normalize []struct {
			In  string `json:"in"`
			Out string `json:"out"`
		} `json:"normalize"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, n := range data.Normalize {
		normalize = append(normalize, struct{ In, Out string }{n.In, n.Out})
	}
	return data.Cases, normalize
}

func meowPackageByID(t *testing.T, id string) *meowPackage {
	t.Helper()
	for _, pkg := range loadedMeowPackages() {
		if pkg.ID == id {
			return pkg
		}
	}
	t.Fatalf("package %s not loaded", id)
	return nil
}

func TestMeowPackagesLoadAndCalibrated(t *testing.T) {
	if err := LoadMeowPackages(); err != nil {
		t.Fatal(err)
	}
	if len(loadedMeowPackages()) != len(meowFiles) {
		t.Fatalf("want %d packages, got %d", len(meowFiles), len(loadedMeowPackages()))
	}
	modes := map[string]int{}
	for _, pkg := range loadedMeowPackages() {
		modes[pkg.Mode]++
	}
	if modes[MeowModeChat] != 4 || modes[MeowModeGPT] != 1 || modes[MeowModeClaude] != 1 {
		t.Fatalf("expect 4 chat, 1 gpt, 1 claude packages, got modes %v", modes)
	}
	// 官方包（4 个）要求所有档位都校准过；自采集包不做此要求。
	officialPkgs := []string{
		"meow-gpt-chat-compatible", "meow-claude-chat-compatible",
		"meow-gpt-other-cap98-efficient", "meow-claude-other-cap98-efficient",
	}
	for _, pkg := range loadedMeowPackages() {
		isOfficial := false
		for _, prefix := range officialPkgs {
			if len(pkg.ID) >= len(prefix) && pkg.ID[:len(prefix)] == prefix {
				isOfficial = true
				break
			}
		}
		if !isOfficial {
			continue
		}
		for _, tier := range []string{"low", "medium", "high"} {
			if !pkg.calibrated[tier] {
				t.Errorf("official package %s %s should pass calibration", pkg.ID, tier)
			}
		}
	}
}

func TestMeowScoreParity(t *testing.T) {
	cases, _ := loadParity(t)
	if len(cases) < 100 {
		t.Fatalf("parity fixture too small: %d", len(cases))
	}
	checkParity(t, cases)
}

// testdata/meow_parity_native.json 同样由参考实现 score_counts 生成，覆盖两个原生协议包。
func TestMeowScoreParityNative(t *testing.T) {
	raw, err := os.ReadFile("testdata/meow_parity_native.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Cases []parityCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range data.Cases {
		seen[c.Package] = true
	}
	if len(data.Cases) < 100 || !seen["meow-gpt-other-cap98-efficient"] || !seen["meow-claude-other-cap98-efficient"] {
		t.Fatalf("native parity fixture incomplete: %d cases, packages %v", len(data.Cases), seen)
	}
	checkParity(t, data.Cases)
}

func checkParity(t *testing.T, cases []parityCase) {
	t.Helper()
	for i, c := range cases {
		pkg := meowPackageByID(t, c.Package)
		got := pkg.score(c.Tier, c.Counts, c.Claimed)
		label := c.Package + "/" + c.Tier + "/" + c.Source + "/" + c.Claimed
		if got.Verdict != c.Verdict || got.Model != c.Model || got.ValidSamples != c.Valid || got.PlannedSamples != c.Planned {
			t.Errorf("#%d %s: got %s/%s valid=%d planned=%d, want %s/%s valid=%d planned=%d",
				i, label, got.Verdict, got.Model, got.ValidSamples, got.PlannedSamples, c.Verdict, c.Model, c.Valid, c.Planned)
		}
		if !reflect.DeepEqual(got.Reasons, c.Reasons) {
			t.Errorf("#%d %s: reasons %v, want %v", i, label, got.Reasons, c.Reasons)
		}
		for model, want := range c.Matches {
			if math.Abs(got.Matches[model]-want) > 1e-9 {
				t.Errorf("#%d %s: match[%s]=%.12f, want %.12f", i, label, model, got.Matches[model], want)
			}
			if math.Abs(got.Scores[model]-c.Scores[model]) > 1e-9*math.Max(1, math.Abs(c.Scores[model])) {
				t.Errorf("#%d %s: score[%s]=%.12f, want %.12f", i, label, model, got.Scores[model], c.Scores[model])
			}
		}
	}
}

func TestMeowNormalizeParity(t *testing.T) {
	_, normalize := loadParity(t)
	pkg := meowPackageByID(t, "meow-gpt-chat-compatible")
	var cell MeowCell
	for _, c := range pkg.cells {
		cell = c
		break
	}
	for _, n := range normalize {
		if got := cell.Normalize(n.In); got != n.Out {
			t.Errorf("Normalize(%q)=%q, want %q", n.In, got, n.Out)
		}
	}
}

func TestPlanMeowFollowsTierCounts(t *testing.T) {
	cases := map[string]string{
		"gpt-6-sol":                 "gpt-6-sol",
		"openai/gpt-6-sol":          "gpt-6-sol",
		"GPT-6-Astra-2026-09":       "gpt-6-astra",
		"anthropic/claude-opus-5.5": "claude-opus-5.5",
		"claude-haiku-4.5-20260901": "claude-haiku-4.5",
	}
	for name, want := range cases {
		plan, ok := PlanMeow(name, "")
		if !ok || plan.ClaimedModel != want || plan.Tier != MeowDefaultTier {
			t.Fatalf("PlanMeow(%q) = %+v, %v; want claimed %s", name, plan.ClaimedModel, ok, want)
		}
		pkg := meowPackageByID(t, plan.PackageID)
		per := map[string]int{}
		for _, job := range plan.Jobs {
			per[job.Cell.ID]++
			if job.Cell.MaxOutputTokens != 128 || job.Cell.ChatTokenField != "max_tokens" || job.Cell.System != "." || job.Cell.Effort != "low" {
				t.Fatalf("unexpected cell spec %+v", job.Cell)
			}
		}
		for identity, n := range pkg.Tiers[MeowDefaultTier].Counts {
			if per[identity] != n {
				t.Fatalf("%s: cell %s planned %d, want %d", name, identity, per[identity], n)
			}
		}
	}
	for _, name := range []string{"gpt-6-solar", "claude-opus-5", "deepseek-chat", "other_known_external", ""} {
		if plan, ok := PlanMeow(name, ""); ok {
			t.Fatalf("PlanMeow(%q) should not resolve, got %s", name, plan.ClaimedModel)
		}
		if plans := PlanMeowAll(name, ""); len(plans) != 0 {
			t.Fatalf("PlanMeowAll(%q) should be empty, got %d plans", name, len(plans))
		}
	}
}

// 同一型号同时有原生包和 Chat 兼容包：原生包排在前面，PlanMeow 仍返回 Chat 兼容包；
// 两种包的计划都严格按各自档位份数展开。
func TestPlanMeowAllPrefersNative(t *testing.T) {
	cases := map[string]string{"gpt-6-sol": MeowModeGPT, "claude-opus-5.5": MeowModeClaude}
	for name, native := range cases {
		plans := PlanMeowAll(name, "")
		if len(plans) != 2 || plans[0].Mode != native || plans[1].Mode != MeowModeChat {
			t.Fatalf("PlanMeowAll(%q) modes = %v", name, planModes(plans))
		}
		for _, plan := range plans {
			pkg := meowPackageByID(t, plan.PackageID)
			if pkg.Mode != plan.Mode || plan.ClaimedModel != name {
				t.Fatalf("%s: plan %+v does not match package mode %s", name, plan.PackageID, pkg.Mode)
			}
			total := 0
			for _, n := range pkg.Tiers[MeowDefaultTier].Counts {
				total += n
			}
			if len(plan.Jobs) != total {
				t.Fatalf("%s %s: %d jobs, want %d", name, plan.Mode, len(plan.Jobs), total)
			}
			for _, job := range plan.Jobs {
				if job.Cell.MaxOutputTokens != 128 || job.Cell.System != "." || job.Cell.Effort != "low" {
					t.Fatalf("unexpected cell spec %+v", job.Cell)
				}
			}
		}
		if chat, ok := PlanMeow(name, ""); !ok || chat.Mode != MeowModeChat {
			t.Fatalf("PlanMeow(%q) should stay on the chat package, got %s", name, chat.Mode)
		}
	}
}

func planModes(plans []MeowPlan) []string {
	modes := make([]string, 0, len(plans))
	for _, plan := range plans {
		modes = append(modes, plan.Mode)
	}
	return modes
}

func TestEvaluateUsesMeowFingerprint(t *testing.T) {
	cases, _ := loadParity(t)
	for _, want := range []string{"match", "mismatch", "insufficient"} {
		for _, c := range cases {
			if c.Verdict != want {
				continue
			}
			pkg := meowPackageByID(t, c.Package)
			obs := Observations{Meow: &MeowObservation{PackageID: pkg.ID, PackageVersion: pkg.Version,
				Tier: c.Tier, ClaimedModel: c.Claimed, Counts: c.Counts}}
			finding := evaluateFingerprint(c.Claimed, obs)
			expected := map[string]string{"match": VerdictMatch, "mismatch": VerdictMismatch, "insufficient": VerdictInconclusive}[want]
			if finding.Verdict != expected || !strings.HasPrefix(finding.Evidence, "Meow 官方基准") {
				t.Fatalf("%s: got %+v", want, finding)
			}
			break
		}
	}
}

// 证据里写明采样协议；回退到 Chat 兼容包时说明原因。
func TestMeowEvidenceNamesProtocol(t *testing.T) {
	plans := PlanMeowAll("gpt-6-sol", "")
	native := MeowObservation{PackageID: plans[0].PackageID, PackageVersion: plans[0].PackageVersion,
		Tier: MeowDefaultTier, ClaimedModel: "gpt-6-sol", Mode: MeowModeGPT, Counts: map[string]map[string]int{}}
	if got := evaluateMeowFingerprint(native).Evidence; !strings.Contains(got, "OpenAI Responses 原生接口") {
		t.Fatalf("native evidence = %q", got)
	}
	chat := MeowObservation{PackageID: plans[1].PackageID, PackageVersion: plans[1].PackageVersion,
		Tier: MeowDefaultTier, ClaimedModel: "gpt-6-sol", Mode: MeowModeChat, NativeFallback: MeowModeGPT,
		Counts: map[string]map[string]int{}}
	got := evaluateMeowFingerprint(chat).Evidence
	if !strings.Contains(got, "Chat Completions 接口") || !strings.Contains(got, "中转不支持OpenAI Responses 原生接口") {
		t.Fatalf("fallback evidence = %q", got)
	}
}

func TestMeowRegistryEntries(t *testing.T) {
	entry, ok := LookupRegistry("gpt-6-sol")
	if !ok || entry.BaselineState != BaselineStateReady || entry.Vendor != "OpenAI" || entry.BaselineMeta.SamplesPerProbe != 30 {
		t.Fatalf("unexpected entry %+v", entry)
	}
	if entry, ok := LookupRegistry("claude-opus-5.5"); !ok || entry.Vendor != "Anthropic" {
		t.Fatalf("unexpected entry %+v", entry)
	}
	// 原生包和 Chat 兼容包收录同一批型号，收录名单里每个型号只出现一次，来源列出两个包。
	seen := map[string]int{}
	for _, e := range meowRegistryEntries() {
		seen[e.ID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("model %s listed %d times", id, n)
		}
	}
	if !strings.Contains(entry.BaselineMeta.Source, "meow-gpt-chat-compatible") ||
		!strings.Contains(entry.BaselineMeta.Source, "meow-gpt-other-cap98-efficient") {
		t.Fatalf("source should list both packages: %s", entry.BaselineMeta.Source)
	}
	if strings.Count(entry.BaselineMeta.Source, "官方基准") != 2 || strings.Contains(entry.BaselineMeta.Source, "自测") {
		t.Fatalf("official packages should be labelled official: %s", entry.BaselineMeta.Source)
	}
	if !BaselineReady() {
		t.Fatal("baseline should be ready")
	}
	if got := Evaluate("openai/gpt-6-sol", Observations{}).Family; got == "" || got == "未知型号" {
		t.Fatalf("meow model should get its package display name, got %q", got)
	}
	if got := Evaluate("totally-unknown-model", Observations{}).Family; got != "未知型号" {
		t.Fatalf("unknown model family = %q", got)
	}
}

// fakeMeowPackage 只填 selectMeowPackages 用到的字段：协议、ID、型号表和各档校准结果。
func fakeMeowPackage(mode, id string, targets int, calibrated ...string) *meowPackage {
	pkg := &meowPackage{Mode: mode, ID: id, file: id + ".meow.json", calibrated: map[string]bool{},
		Tiers: map[string]meowTier{"low": {}, "medium": {}, "high": {}}}
	for i := 0; i < targets; i++ {
		pkg.Models = append(pkg.Models, meowModel{ID: fmt.Sprintf("%s-m%d", id, i)})
	}
	// 参照型号和 other 都不算检测目标，混进来验证 targetModelCount 会跳过它们。
	pkg.Models = append(pkg.Models,
		meowModel{ID: "ext-" + id, ReferenceOnly: true},
		meowModel{ID: MeowOtherID},
	)
	for _, tier := range calibrated {
		pkg.calibrated[tier] = true
	}
	return pkg
}

func selectionResult(t *testing.T, pkgs []*meowPackage, tier string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, sel := range selectMeowPackages(pkgs, tier) {
		if prev, dup := got[sel.ID]; dup {
			t.Fatalf("package %s selected twice (%s, %s)", sel.ID, prev, sel.selectedTier)
		}
		got[sel.ID] = sel.selectedTier
	}
	return got
}

// 请求档位没有校准过的包时向上升档，且绝不降档；同档多个包按收录型号数、再按 ID 取一个。
func TestSelectMeowPackagesUpgradesTier(t *testing.T) {
	cases := []struct {
		name string
		pkgs []*meowPackage
		tier string
		want map[string]string
	}{{
		name: "请求档位已校准时原地使用",
		pkgs: []*meowPackage{fakeMeowPackage(MeowModeChat, "a", 4, "low", "medium", "high")},
		tier: "medium",
		want: map[string]string{"a": "medium"},
	}, {
		name: "低档未过则升到 high",
		pkgs: []*meowPackage{fakeMeowPackage(MeowModeChat, "a", 4, "high")},
		tier: "low",
		want: map[string]string{"a": "high"},
	}, {
		name: "只有低档过也不降档，保持请求档位并如实标未校准",
		pkgs: []*meowPackage{fakeMeowPackage(MeowModeChat, "a", 4, "low")},
		tier: "high",
		want: map[string]string{"a": "high"},
	}, {
		name: "同档两个包取收录型号多的",
		pkgs: []*meowPackage{
			fakeMeowPackage(MeowModeChat, "a", 4, "high"),
			fakeMeowPackage(MeowModeChat, "b", 8, "high"),
		},
		tier: "high",
		want: map[string]string{"b": "high"},
	}, {
		name: "型号数相同按文件名",
		pkgs: []*meowPackage{
			fakeMeowPackage(MeowModeChat, "b", 4, "high"),
			fakeMeowPackage(MeowModeChat, "a", 4, "high"),
		},
		tier: "high",
		want: map[string]string{"a": "high"},
	}, {
		name: "低档能过的包优先于高档能过但型号更多的包",
		pkgs: []*meowPackage{
			fakeMeowPackage(MeowModeChat, "a", 4, "medium"),
			fakeMeowPackage(MeowModeChat, "b", 8, "high"),
		},
		tier: "medium",
		want: map[string]string{"a": "medium"},
	}, {
		name: "每个协议各自升档，互不影响",
		pkgs: []*meowPackage{
			fakeMeowPackage(MeowModeGPT, "n", 4, "high"),
			fakeMeowPackage(MeowModeChat, "c", 4, "low", "medium", "high"),
		},
		tier: "medium",
		want: map[string]string{"n": "high", "c": "medium"},
	}, {
		name: "整组都没校准时退回请求档位，只留型号多的一个包",
		pkgs: []*meowPackage{
			fakeMeowPackage(MeowModeChat, "a", 4),
			fakeMeowPackage(MeowModeChat, "b", 8),
		},
		tier: "medium",
		want: map[string]string{"b": "medium"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectionResult(t, tc.pkgs, tc.tier); !maps.Equal(got, tc.want) {
				t.Fatalf("selectMeowPackages(%s) = %v, want %v", tc.tier, got, tc.want)
			}
		})
	}
}

// 真实嵌入包上选包不改协议顺序（原生在前、Chat 兼容在后），也不降档。
func TestSelectMeowPackagesKeepsModeOrder(t *testing.T) {
	if err := LoadMeowPackages(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gpt-6-sol", "claude-opus-5.5"} {
		pkgs, _ := resolveMeowModels(name)
		if len(pkgs) < 2 {
			t.Fatalf("%s should resolve to a native and a chat package, got %d", name, len(pkgs))
		}
		for _, tier := range meowTierOrder {
			selected := selectMeowPackages(pkgs, tier)
			var gotModes, wantModes []string
			for _, sel := range selected {
				gotModes = append(gotModes, sel.Mode)
				if slices.Index(meowTierOrder, sel.selectedTier) < slices.Index(meowTierOrder, tier) {
					t.Fatalf("%s %s: package %s downgraded to %s", name, tier, sel.ID, sel.selectedTier)
				}
			}
			for _, pkg := range pkgs {
				if !slices.Contains(wantModes, pkg.Mode) {
					wantModes = append(wantModes, pkg.Mode)
				}
			}
			if !slices.Equal(gotModes, wantModes) {
				t.Fatalf("%s %s: modes = %v, want %v", name, tier, gotModes, wantModes)
			}
			if gotModes[len(gotModes)-1] != MeowModeChat {
				t.Fatalf("%s %s: chat package should come last, got %v", name, tier, gotModes)
			}
		}
	}
}

// 自采集的两个包 id、version 相同，评分必须落到计划选中的那个包上，不能按 id + version
// 碰到哪个算哪个。qwen3.8-max 两个包都收录：A 包 high 档已校准，B 包各档都未校准。
func TestSelfCollectedPackagesScoreTheirOwnPlan(t *testing.T) {
	files := map[string]bool{}
	for _, pkg := range loadedMeowPackages() {
		if files[pkg.file] {
			t.Fatalf("duplicate package file %s", pkg.file)
		}
		files[pkg.file] = true
	}
	plans := PlanMeowAll("qwen3.8-max", MeowDefaultTier)
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d", len(plans))
	}
	plan := plans[0]
	if plan.PackageFile != "meowdata/f44a29d558ce6899.meow.json" || plan.Tier != "high" {
		t.Fatalf("want A package high tier, got %s %s", plan.PackageFile, plan.Tier)
	}
	result, ok := ScoreMeow(MeowObservation{PackageFile: plan.PackageFile, PackageID: plan.PackageID,
		PackageVersion: plan.PackageVersion, Tier: plan.Tier, ClaimedModel: plan.ClaimedModel})
	if !ok || !result.Calibrated {
		t.Fatalf("scoring should use the calibrated A package, got ok=%v calibrated=%v", ok, result.Calibrated)
	}
	// 没有 PackageFile 时 id + version 对应两个包，宁可不评也不能拿错包。
	if _, ok := ScoreMeow(MeowObservation{PackageID: plan.PackageID, PackageVersion: plan.PackageVersion, Tier: plan.Tier}); ok {
		t.Fatal("ambiguous id+version must not be scored")
	}
}

// 只在未校准的 B 包里的型号：仍按 B 包采样（评分如实报未校准），但收录状态不能标 ready。
func TestUncalibratedOnlyModelIsPending(t *testing.T) {
	plans := PlanMeowAll("qwen3.8-flash", MeowDefaultTier)
	if len(plans) != 1 || plans[0].PackageFile != "meowdata/ab763b91fa29b8a6.meow.json" || plans[0].Tier != MeowDefaultTier {
		t.Fatalf("unexpected plans %+v", plans)
	}
	states := map[string]string{}
	for _, entry := range meowRegistryEntries() {
		states[entry.ID] = entry.BaselineState
		if strings.Count(entry.BaselineMeta.Source, "apigx-cn-chat") > 1 {
			t.Errorf("%s source label duplicated: %s", entry.ID, entry.BaselineMeta.Source)
		}
	}
	for id, want := range map[string]string{
		"qwen3.8-flash": BaselineStatePending, "glm-5.3-flash": BaselineStatePending,
		"qwen3.8-max": BaselineStateReady, "kimi-k3": BaselineStateReady,
	} {
		if states[id] != want {
			t.Errorf("%s baseline state = %q, want %q", id, states[id], want)
		}
	}
	for id := range states {
		if strings.HasPrefix(id, "mimo-") {
			t.Errorf("mimo model %s should have been removed", id)
		}
	}
}

// TestMeowReadyTiers 钉住型号名单的可选项：只列「该档有校准基准」的型号和档位。
// 名单是前端唯一能选的集合，写错就会摆出一张点了必然测不出来的卡片。
func TestMeowReadyTiers(t *testing.T) {
	for name, want := range map[string][]string{
		// 官方请求名，只有高档采过。低/中档会升档跑成几百次高档题量，不能列为可选。
		"deepseek-flash": {"high"},
		"qwen3.8-max":    {"high"},
		// 参考项目的官方基准包三档都过校准。
		"gpt-6-sol":        {"low", "medium", "high"},
		"claude-haiku-4.5": {"low", "medium", "high"},
		// 只出现在未校准包里 / 根本没有基准包（旧手填基准）的名字一律不可选。
		"glm-5.3-flash":       nil,
		"deepseek-v4.1-flash": nil,
		"deepseek-chat":       nil,
		"gpt-4o":              nil,
	} {
		if got := MeowReadyTiers(name); !slices.Equal(got, want) {
			t.Errorf("MeowReadyTiers(%q) = %v, want %v", name, got, want)
		}
	}
}
