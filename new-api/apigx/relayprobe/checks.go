package relayprobe

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"gorm.io/gorm"
)

// 持续检测调度。RunDue（既有）只管「中转还活着吗」，按固定 30 分钟跑，写在
// RelayShare 上；这里管用户自选挡位的三类检测，按 RelayCheckConfig 跑，每个模型
// 各写一行 RelayProbeSample。两者互不覆盖：没有配置的中转行为完全不变。
//
// 成本是这里的主要约束——每一次采样都花的是中转主人的上游额度，所以：
//   - 每类检测每轮最多处理 limit 个配置；
//   - 单个配置内部按模型串行，不并发轰上游；
//   - 降智只发一次糖果题，掺水题量更大所以默认挡位是 3 小时；
//   - 不管成功失败都调用 MarkCheckRan，失败的配置不会立刻重试形成风暴。
const checksPerKindLimit = 10

// dueCheckKinds 决定执行顺序：可用率最便宜也最重要，掺水最贵放最后。
var dueCheckKinds = []string{
	apigxmodel.ProbeKindAvailability,
	apigxmodel.ProbeKindDegrade,
	apigxmodel.ProbeKindDilution,
}

// RunDueChecks 执行所有到期的持续检测，返回写入的采样行数。
func RunDueChecks(ctx context.Context, db *gorm.DB, now int64, limit int) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("relay checks: nil database")
	}
	if limit <= 0 || limit > checksPerKindLimit {
		limit = checksPerKindLimit
	}
	written := 0
	for _, kind := range dueCheckKinds {
		if ctx != nil && ctx.Err() != nil {
			return written, ctx.Err()
		}
		configs, err := apigxmodel.ListDueCheckConfigs(db, kind, now, limit)
		if err != nil {
			return written, err
		}
		for i := range configs {
			if ctx != nil && ctx.Err() != nil {
				return written, ctx.Err()
			}
			count, runErr := runConfigCheck(ctx, db, &configs[i], kind, now)
			written += count
			// 单个中转失败（凭据没了、上游挂了）不应中断整轮调度；推进到下一个挡位即可。
			if markErr := apigxmodel.MarkCheckRan(db, configs[i].ID, kind, now, configs[i].Interval(kind)); markErr != nil {
				return written, markErr
			}
			if runErr != nil && errors.Is(runErr, context.Canceled) {
				return written, runErr
			}
		}
	}
	return written, nil
}

// checkTarget 是一个配置解出来的可用检测目标。
type checkTarget struct {
	share  apigxmodel.RelayShare
	key    string
	models []string
}

// resolveTarget 取出中转与明文凭据。中转已下架或凭据不可用时返回 ErrNotFound，
// 调用方据此跳过本轮但仍然推进下一次时间。
func resolveTarget(db *gorm.DB, config *apigxmodel.RelayCheckConfig, kind string) (*checkTarget, error) {
	var share apigxmodel.RelayShare
	err := db.Where("id = ? AND deleted_at IS NULL", config.RelayShareID).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apigxmodel.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if share.Status != apigxmodel.RelayStatusPublished || share.AliveFlag != apigxmodel.AliveFlagAlive {
		return nil, apigxmodel.ErrNotFound
	}
	credential, err := apigxmodel.GetRelayCredential(db, config.OwnerUserID, config.RelayShareID)
	if err != nil {
		return nil, err
	}
	key, err := secretbox.Decrypt(credential.SecretRef)
	if err != nil {
		return nil, err
	}
	models := config.Models(kind)
	if kind == apigxmodel.ProbeKindAvailability {
		// 可用率不挑模型：它测的是中转本身通不通，用固定占位标记这一行。
		models = []string{availabilityModelLabel}
	}
	return &checkTarget{share: share, key: key, models: models}, nil
}

// availabilityModelLabel 让可用率采样与按模型的采样共用一张表而不留空 model 列。
const availabilityModelLabel = "-"

func runConfigCheck(ctx context.Context, db *gorm.DB, config *apigxmodel.RelayCheckConfig, kind string, now int64) (int, error) {
	target, err := resolveTarget(db, config, kind)
	if err != nil {
		if errors.Is(err, apigxmodel.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	visibility := config.Mode(kind)
	if visibility == apigxmodel.CheckVisibilityOff {
		return 0, nil
	}
	written := 0
	for _, model := range target.models {
		if ctx != nil && ctx.Err() != nil {
			return written, ctx.Err()
		}
		sample := runOneSample(ctx, kind, target, model)
		sample.RelayShareID = config.RelayShareID
		sample.Kind = kind
		sample.Model = model
		sample.Visibility = visibility
		sample.ProbedAt = now
		sample.Evidence = truncateEvidence(sample.Evidence)
		if err := apigxmodel.CreateRelayProbeSample(db, sample); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// runOneSample 把一次检测映射成一行采样。三类检测的失败都记成 failed 行而不是
// 丢弃：主人需要看到「这个时间点测过、没通」，不然曲线上会出现无法解释的空洞。
func runOneSample(ctx context.Context, kind string, target *checkTarget, model string) *apigxmodel.RelayProbeSample {
	switch kind {
	case apigxmodel.ProbeKindAvailability:
		result := Probe(ctx, target.share.BaseURL, target.share.Protocol, target.key)
		return &apigxmodel.RelayProbeSample{
			Status:    result.Status,
			LatencyMs: result.LatencyMs,
			Evidence:  result.Reason,
		}
	case apigxmodel.ProbeKindDegrade:
		// 降智持续检测固定糖果题：题面参数每次随机，可机判、成本可控、跨时间可比。
		result, err := RunBenchmark(ctx, apigxmodel.BenchmarkCandy, target.share.BaseURL, target.share.Protocol, target.key, model)
		if err != nil {
			return failedSample(err)
		}
		return &apigxmodel.RelayProbeSample{
			Status:    result.Status,
			Score:     result.Score,
			LatencyMs: result.LatencyMs,
			Evidence:  result.Evidence,
		}
	case apigxmodel.ProbeKindDilution:
		report, err := RunDilutionTest(ctx, target.share.BaseURL, target.share.Protocol, target.key, model)
		if err != nil {
			return failedSample(err)
		}
		return &apigxmodel.RelayProbeSample{
			Status:   dilutionStatus(report.Verdict),
			Score:    report.Score,
			Evidence: dilutionEvidence(report),
		}
	default:
		return failedSample(fmt.Errorf("未知检测类型"))
	}
}

func failedSample(err error) *apigxmodel.RelayProbeSample {
	reason := "检测失败"
	if err != nil {
		reason = err.Error()
	}
	return &apigxmodel.RelayProbeSample{Status: apigxmodel.RelayCheckFailed, Evidence: reason}
}

// dilutionStatus 把八维判定映射到采样状态。inconclusive 不算异常也不算健康——
// 证据不足时标 degraded 会冤枉中转，标 healthy 会给出没有依据的安全感，所以走
// 单独的 unknown 状态由前端灰显。
func dilutionStatus(verdict string) string {
	switch verdict {
	case modelfp.VerdictMatch:
		return apigxmodel.RelayCheckHealthy
	case modelfp.VerdictMismatch:
		return apigxmodel.RelayCheckFailed
	case modelfp.VerdictSuspect:
		return apigxmodel.RelayCheckDegraded
	default:
		return apigxmodel.RelayCheckUnknown
	}
}

// dilutionEvidence 只摘录不一致的维度名：完整八维报告存不进 500 字的列，而主人
// 需要的是「哪一维不对」，细节留给工具页面重跑。
func dilutionEvidence(report modelfp.Report) string {
	problems := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		if finding.Verdict == modelfp.VerdictMismatch || finding.Verdict == modelfp.VerdictSuspect {
			problems = append(problems, finding.Name+"（"+finding.Evidence+"）")
		}
	}
	if len(problems) == 0 {
		return fmt.Sprintf("八维一致性 %d/100，未见与所声称型号不符之处", report.Score)
	}
	return fmt.Sprintf("八维一致性 %d/100，异常维度：%s", report.Score, strings.Join(problems, "；"))
}

// truncateEvidence 与 Evidence 列的 size:500 对齐，按 rune 截断避免切坏中文。
func truncateEvidence(text string) string {
	const maxEvidence = 460
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxEvidence {
		return string(runes)
	}
	return string(runes[:maxEvidence]) + "…"
}
