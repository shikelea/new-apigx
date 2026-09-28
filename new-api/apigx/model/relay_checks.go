package apigxmodel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 持续检测的配置与结果。
//
// 三件事刻意分开存：
//   - RelayCheckConfig 是「要不要测、多久测一次、给谁看」，一个中转一行。
//   - RelayProbeSample 是「某个模型某一次测出什么」，按模型分行。一个中转上 GPT 正常
//     而 Claude 挂掉时，单一的总可用率会误导人，所以不做成一个标量。
//   - Crowdtest 是众测，与中转完全无关：提交者用自己的凭据测一次，再自行决定要不要
//     把结果贴出来。贴出来的只是一条结果，不包含被测地址，别人也不能照着重跑。
const (
	// 可见性：关闭 / 仅自己可见 / 公开展示。
	// 「测了但不展示」会白烧上游额度，所以拆成这三档而不是一个布尔。
	CheckVisibilityOff    = 0
	CheckVisibilityOwner  = 1
	CheckVisibilityPublic = 2

	// 监控间隔四个固定挡位（秒）。
	CheckInterval15Min = 900
	CheckInterval30Min = 1800
	CheckInterval1Hour = 3600
	CheckInterval3Hour = 10800

	// 采样类型。
	ProbeKindAvailability = "availability" // 可用率
	ProbeKindDegrade      = "degrade"      // 降智（糖果测试）
	ProbeKindDilution     = "dilution"     // 模型掺水
	ProbeKindSpeed        = "speed"        // TTFT / TPS

	// 众测题型。
	BenchmarkCandy   = "candy"
	BenchmarkPelican = "pelican"

	CrowdtestStatusPending   = "pending"
	CrowdtestStatusScored    = "scored"
	CrowdtestStatusRejected  = "rejected"
	CrowdtestStatusFailed    = "failed"
	CrowdtestVisibilityDraft = 0
	// 一旦公布就对他人只读：提交者之外的人不能改，提交者也不能撤回打分。
	CrowdtestVisibilityPublic = 1

	// 反滥用上限：众测花的是提交者自己的额度，封顶只为拦住脚本刷榜。
	CrowdtestDailyPerUser = 20
)

// CheckIntervals 是允许的监控间隔，按从密到疏排列，用于表单与校验。
var CheckIntervals = []int{CheckInterval15Min, CheckInterval30Min, CheckInterval1Hour, CheckInterval3Hour}

func ValidCheckInterval(seconds int) bool {
	for _, allowed := range CheckIntervals {
		if seconds == allowed {
			return true
		}
	}
	return false
}

func ValidCheckVisibility(mode int) bool {
	return mode == CheckVisibilityOff || mode == CheckVisibilityOwner || mode == CheckVisibilityPublic
}

func ValidBenchmark(name string) bool {
	return name == BenchmarkCandy || name == BenchmarkPelican
}

// RelayCheckConfig 保存一个中转的持续检测设置。提交中转时勾选，之后可改。
type RelayCheckConfig struct {
	ID           int64 `gorm:"primaryKey"`
	RelayShareID int64 `gorm:"bigint;not null;uniqueIndex:idx_apigx_checkcfg_share"`
	OwnerUserID  int64 `gorm:"bigint;not null;index:idx_apigx_checkcfg_owner"`

	// 可用率监控：默认开启且公开，用户可以改成仅自己可见或关闭。
	AvailabilityMode     int   `gorm:"type:int;not null;default:2"`
	AvailabilityInterval int   `gorm:"type:int;not null;default:1800"`
	NextAvailabilityAt   int64 `gorm:"bigint;not null;default:0;index:idx_apigx_checkcfg_nextavail"`

	// 降智持续检测：默认关闭，针对 GPT 模型，题目固定用糖果测试。
	DegradeMode     int    `gorm:"type:int;not null;default:0"`
	DegradeInterval int    `gorm:"type:int;not null;default:3600"`
	DegradeModels   string `gorm:"type:text"`
	NextDegradeAt   int64  `gorm:"bigint;not null;default:0;index:idx_apigx_checkcfg_nextdeg"`

	// 掺水检测：提交时可勾选并多选模型；同步出一次结果，可选是否继续监控。
	DilutionMode     int    `gorm:"type:int;not null;default:0"`
	DilutionInterval int    `gorm:"type:int;not null;default:10800"`
	DilutionModels   string `gorm:"type:text"`
	NextDilutionAt   int64  `gorm:"bigint;not null;default:0;index:idx_apigx_checkcfg_nextdil"`

	// OwnerPaused 是主人的总开关：一键停掉本中转上的全部自动检测与众测消耗。
	OwnerPaused bool `gorm:"not null"`

	CreatedAt int64          `gorm:"bigint;not null;default:0"`
	UpdatedAt int64          `gorm:"bigint;not null;default:0"`
	DeletedAt gorm.DeletedAt `gorm:"index:idx_apigx_checkcfg_del"`
}

func (RelayCheckConfig) TableName() string { return TablePrefix + "relay_check_configs" }

// Models 解析多选模型列表；空值返回空切片而不是报错。
func (c *RelayCheckConfig) Models(field string) []string {
	raw := ""
	switch field {
	case ProbeKindDegrade:
		raw = c.DegradeModels
	case ProbeKindDilution:
		raw = c.DilutionModels
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var models []string
	if err := common.Unmarshal([]byte(raw), &models); err != nil {
		return nil
	}
	return models
}

// Mode 返回某一类检测的可见性（关闭 / 仅自己 / 公开）。未知类型按关闭处理，
// 这样新增类型时调度器不会误跑。
func (c *RelayCheckConfig) Mode(kind string) int {
	switch kind {
	case ProbeKindAvailability:
		return c.AvailabilityMode
	case ProbeKindDegrade:
		return c.DegradeMode
	case ProbeKindDilution:
		return c.DilutionMode
	}
	return CheckVisibilityOff
}

// Interval 返回某一类检测的挡位秒数。未知类型回落到最长挡位。
func (c *RelayCheckConfig) Interval(kind string) int {
	switch kind {
	case ProbeKindAvailability:
		return c.AvailabilityInterval
	case ProbeKindDegrade:
		return c.DegradeInterval
	case ProbeKindDilution:
		return c.DilutionInterval
	}
	return CheckInterval3Hour
}

// SetModels 写入多选模型列表，顺带去重与去空。
func (c *RelayCheckConfig) SetModels(field string, models []string) error {
	cleaned := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		cleaned = append(cleaned, trimmed)
	}
	if len(cleaned) > maxCheckModels {
		return fmt.Errorf("%w: at most %d models per check", ErrInvalidArgument, maxCheckModels)
	}
	encoded, err := common.Marshal(cleaned)
	if err != nil {
		return err
	}
	switch field {
	case ProbeKindDegrade:
		c.DegradeModels = string(encoded)
	case ProbeKindDilution:
		c.DilutionModels = string(encoded)
	default:
		return fmt.Errorf("%w: unknown check field", ErrInvalidArgument)
	}
	return nil
}

// 每类检测最多测几个模型。每个模型每轮都要真实调用上游，必须封顶。
const maxCheckModels = 8

func (c *RelayCheckConfig) Validate() error {
	if c.RelayShareID <= 0 || c.OwnerUserID <= 0 {
		return fmt.Errorf("%w: relay share and owner are required", ErrInvalidArgument)
	}
	for _, mode := range []int{c.AvailabilityMode, c.DegradeMode, c.DilutionMode} {
		if !ValidCheckVisibility(mode) {
			return fmt.Errorf("%w: invalid check visibility", ErrInvalidArgument)
		}
	}
	for _, interval := range []int{c.AvailabilityInterval, c.DegradeInterval, c.DilutionInterval} {
		if !ValidCheckInterval(interval) {
			return fmt.Errorf("%w: interval must be 900, 1800, 3600 or 10800 seconds", ErrInvalidArgument)
		}
	}
	// 开了检测却没选模型，会导致调度器空转。
	if c.DegradeMode != CheckVisibilityOff && len(c.Models(ProbeKindDegrade)) == 0 {
		return fmt.Errorf("%w: degrade check requires at least one model", ErrInvalidArgument)
	}
	if c.DilutionMode != CheckVisibilityOff && len(c.Models(ProbeKindDilution)) == 0 {
		return fmt.Errorf("%w: dilution check requires at least one model", ErrInvalidArgument)
	}
	return nil
}

// RelayProbeSample 是一次采样结果，按「中转 + 模型 + 类型」分行。
type RelayProbeSample struct {
	ID           int64  `gorm:"primaryKey"`
	RelayShareID int64  `gorm:"bigint;not null;index:idx_apigx_probesample_share,priority:1"`
	Kind         string `gorm:"size:16;not null;default:'';index:idx_apigx_probesample_share,priority:2"`
	Model        string `gorm:"size:191;not null;default:'';index:idx_apigx_probesample_share,priority:3"`
	ProbedAt     int64  `gorm:"bigint;not null;default:0;index:idx_apigx_probesample_share,priority:4"`

	Status string `gorm:"size:16;not null;default:''"`
	// Score 含义随 Kind 变化：降智是 0–100 得分，掺水是一致性得分，可用率为 0/100。
	Score        int   `gorm:"type:int;not null;default:0"`
	LatencyMs    int64 `gorm:"bigint;not null;default:0"`
	FirstTokenMs int64 `gorm:"bigint;not null;default:0"`
	OutputTPS    int   `gorm:"type:int;not null;default:0"`
	// Evidence 已脱敏且截断，可以直接对外展示。
	Evidence string `gorm:"size:500;not null;default:''"`
	// Visibility 在写入时就定好，读取端不需要再回查配置。
	Visibility int   `gorm:"type:int;not null;default:0;index:idx_apigx_probesample_vis"`
	CreatedAt  int64 `gorm:"bigint;not null;default:0"`
}

func (RelayProbeSample) TableName() string { return TablePrefix + "relay_probe_samples" }

// Crowdtest 是一次众测：提交者自己出凭据测一次，然后自行决定要不要把结果贴出来。
//
// 它刻意不与中转关联，也不记录被测地址。贴出来的只有「哪个模型、拿了多少分、
// 凭据是什么」这三样，别人只能看，不能照着再跑一次。这样做的理由有两条：一是
// 别人的上游额度不该被陌生人消耗；二是一旦记下地址，公开的众测表就变成了一份
// 可用中转清单，那是另一回事，不是这个功能要做的。
type Crowdtest struct {
	ID              int64  `gorm:"primaryKey"`
	PublicID        string `gorm:"size:32;not null;default:'';uniqueIndex:idx_apigx_crowdtest_pub"`
	SubmitterUserID int64  `gorm:"bigint;not null;index:idx_apigx_crowdtest_user"`

	Benchmark string `gorm:"size:16;not null;default:''"`
	Model     string `gorm:"size:191;not null;default:''"`
	Status    string `gorm:"size:16;not null;default:''"`
	Score     int    `gorm:"type:int;not null;default:0"`
	Evidence  string `gorm:"size:500;not null;default:''"`
	// Artifact 保存鹈鹕测试产出的 SVG 原文，供展示；糖果测试为空。
	Artifact string `gorm:"type:text"`

	// Visibility 由提交者选择。公布后对所有人只读，包括提交者自己。
	Visibility int `gorm:"type:int;not null;default:0;index:idx_apigx_crowdtest_vis"`
	// PublishedAt 记录公布时刻；非 0 即视为已锁定。
	PublishedAt int64          `gorm:"bigint;not null;default:0"`
	CreatedAt   int64          `gorm:"bigint;not null;default:0"`
	UpdatedAt   int64          `gorm:"bigint;not null;default:0"`
	DeletedAt   gorm.DeletedAt `gorm:"index:idx_apigx_crowdtest_del"`
}

func (Crowdtest) TableName() string { return TablePrefix + "crowdtests" }

func (c *Crowdtest) Validate() error {
	c.Model = strings.TrimSpace(c.Model)
	if c.SubmitterUserID <= 0 || c.Model == "" {
		return fmt.Errorf("%w: submitter and model are required", ErrInvalidArgument)
	}
	if !ValidBenchmark(c.Benchmark) {
		return fmt.Errorf("%w: benchmark must be candy or pelican", ErrInvalidArgument)
	}
	if c.Visibility != CrowdtestVisibilityDraft && c.Visibility != CrowdtestVisibilityPublic {
		return fmt.Errorf("%w: invalid crowdtest visibility", ErrInvalidArgument)
	}
	return nil
}

func UpsertRelayCheckConfig(db *gorm.DB, config *RelayCheckConfig) error {
	if config == nil {
		return fmt.Errorf("%w: nil check config", ErrInvalidArgument)
	}
	if err := config.Validate(); err != nil {
		return err
	}
	now := common.GetTimestamp()
	config.UpdatedAt = now
	// 首次保存时把三个到期时间设为「立刻」，让用户提交后马上看到第一次结果。
	if config.ID == 0 {
		config.CreatedAt = now
		if config.AvailabilityMode != CheckVisibilityOff && config.NextAvailabilityAt == 0 {
			config.NextAvailabilityAt = now
		}
		if config.DegradeMode != CheckVisibilityOff && config.NextDegradeAt == 0 {
			config.NextDegradeAt = now
		}
		if config.DilutionMode != CheckVisibilityOff && config.NextDilutionAt == 0 {
			config.NextDilutionAt = now
		}
	}
	var existing RelayCheckConfig
	err := db.Where("relay_share_id = ?", config.RelayShareID).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return db.Create(config).Error
	case err != nil:
		return err
	}
	config.ID, config.CreatedAt = existing.ID, existing.CreatedAt
	return db.Model(&RelayCheckConfig{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"availability_mode": config.AvailabilityMode, "availability_interval": config.AvailabilityInterval,
		"degrade_mode": config.DegradeMode, "degrade_interval": config.DegradeInterval, "degrade_models": config.DegradeModels,
		"dilution_mode": config.DilutionMode, "dilution_interval": config.DilutionInterval, "dilution_models": config.DilutionModels,
		"owner_paused": config.OwnerPaused, "updated_at": now,
	}).Error
}

func GetRelayCheckConfig(db *gorm.DB, relayShareID int64) (*RelayCheckConfig, error) {
	var config RelayCheckConfig
	err := db.Where("relay_share_id = ? AND deleted_at IS NULL", relayShareID).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &config, err
}

// ListDueCheckConfigs 返回某一类检测到期的配置。主人暂停的一律跳过。
func ListDueCheckConfigs(db *gorm.DB, kind string, now int64, limit int) ([]RelayCheckConfig, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	modeColumn, nextColumn := "", ""
	switch kind {
	case ProbeKindAvailability:
		modeColumn, nextColumn = "availability_mode", "next_availability_at"
	case ProbeKindDegrade:
		modeColumn, nextColumn = "degrade_mode", "next_degrade_at"
	case ProbeKindDilution:
		modeColumn, nextColumn = "dilution_mode", "next_dilution_at"
	default:
		return nil, fmt.Errorf("%w: unknown probe kind", ErrInvalidArgument)
	}
	var configs []RelayCheckConfig
	err := db.Where(modeColumn+" <> ? AND owner_paused = ? AND deleted_at IS NULL AND ("+nextColumn+" = 0 OR "+nextColumn+" <= ?)",
		CheckVisibilityOff, false, now).
		Order(nextColumn + " ASC, id ASC").Limit(limit).Find(&configs).Error
	return configs, err
}

// MarkCheckRan 把下一次到期时间推到 now+interval。用条件更新避免两个调度节点重复跑。
func MarkCheckRan(db *gorm.DB, id int64, kind string, now int64, interval int) error {
	nextColumn := ""
	switch kind {
	case ProbeKindAvailability:
		nextColumn = "next_availability_at"
	case ProbeKindDegrade:
		nextColumn = "next_degrade_at"
	case ProbeKindDilution:
		nextColumn = "next_dilution_at"
	default:
		return fmt.Errorf("%w: unknown probe kind", ErrInvalidArgument)
	}
	if !ValidCheckInterval(interval) {
		return fmt.Errorf("%w: invalid interval", ErrInvalidArgument)
	}
	return db.Model(&RelayCheckConfig{}).
		Where("id = ? AND ("+nextColumn+" = 0 OR "+nextColumn+" <= ?)", id, now).
		UpdateColumns(map[string]any{nextColumn: now + int64(interval), "updated_at": now}).Error
}

func CreateRelayProbeSample(db *gorm.DB, sample *RelayProbeSample) error {
	if sample == nil || sample.RelayShareID <= 0 || sample.Kind == "" || sample.Model == "" {
		return fmt.Errorf("%w: relay share, kind and model are required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	if sample.ProbedAt == 0 {
		sample.ProbedAt = now
	}
	sample.CreatedAt = now
	return db.Create(sample).Error
}

// ListRelayProbeSamples 返回一个中转最近的采样。viewerIsOwner 为 false 时只返回公开项。
func ListRelayProbeSamples(db *gorm.DB, relayShareID int64, kind string, viewerIsOwner bool, limit int) ([]RelayProbeSample, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := db.Where("relay_share_id = ?", relayShareID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	if !viewerIsOwner {
		q = q.Where("visibility = ?", CheckVisibilityPublic)
	}
	var samples []RelayProbeSample
	return samples, q.Order("probed_at DESC, id DESC").Limit(limit).Find(&samples).Error
}

func CreateCrowdtest(db *gorm.DB, test *Crowdtest) error {
	if test == nil {
		return fmt.Errorf("%w: nil crowdtest", ErrInvalidArgument)
	}
	if err := test.Validate(); err != nil {
		return err
	}
	if test.PublicID == "" {
		test.PublicID = newRelayPublicID()
	}
	if test.Status == "" {
		test.Status = CrowdtestStatusPending
	}
	now := common.GetTimestamp()
	test.CreatedAt = now
	test.UpdatedAt = now
	if test.Visibility == CrowdtestVisibilityPublic && test.PublishedAt == 0 {
		test.PublishedAt = now
	}
	return db.Create(test).Error
}

// PublishCrowdtest 把一条众测公布出去。公布是单向的：已公布的记录不再接受修改，
// 所以这里用 published_at = 0 作为条件，重复公布是无害的空操作。
func PublishCrowdtest(db *gorm.DB, publicID string, submitterUserID int64) error {
	if strings.TrimSpace(publicID) == "" || submitterUserID <= 0 {
		return fmt.Errorf("%w: crowdtest and submitter are required", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	updated := db.Model(&Crowdtest{}).
		Where("public_id = ? AND submitter_user_id = ? AND published_at = 0 AND deleted_at IS NULL", publicID, submitterUserID).
		UpdateColumns(map[string]any{"visibility": CrowdtestVisibilityPublic, "published_at": now, "updated_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected == 0 {
		// 不区分「不存在」与「不属于你」，避免通过公布接口探测他人记录。
		return ErrNotFound
	}
	return nil
}

// CountCrowdtestsSince 统计某个用户在时间窗内的众测次数。众测花的是提交者自己的
// 额度，限流只是为了拦住脚本刷榜，所以只按用户算。
func CountCrowdtestsSince(db *gorm.DB, since int64, submitterUserID int64) (int64, error) {
	if submitterUserID <= 0 {
		return 0, fmt.Errorf("%w: submitter is required", ErrInvalidArgument)
	}
	var count int64
	return count, db.Model(&Crowdtest{}).
		Where("created_at >= ? AND submitter_user_id = ? AND deleted_at IS NULL", since, submitterUserID).
		Count(&count).Error
}

// ListPublicCrowdtests 列出已公布的众测，最新在前。纯展示，没有分组维度。
func ListPublicCrowdtests(db *gorm.DB, benchmark string, limit int) ([]Crowdtest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := db.Where("visibility = ? AND deleted_at IS NULL", CrowdtestVisibilityPublic)
	if trimmed := strings.TrimSpace(benchmark); trimmed != "" {
		if !ValidBenchmark(trimmed) {
			return nil, fmt.Errorf("%w: benchmark must be candy or pelican", ErrInvalidArgument)
		}
		q = q.Where("benchmark = ?", trimmed)
	}
	var tests []Crowdtest
	return tests, q.Order("created_at DESC, id DESC").Limit(limit).Find(&tests).Error
}

// ListMyCrowdtests 列出提交者自己的众测，含未公布的草稿。
func ListMyCrowdtests(db *gorm.DB, submitterUserID int64, limit int) ([]Crowdtest, error) {
	if submitterUserID <= 0 {
		return nil, fmt.Errorf("%w: submitter is required", ErrInvalidArgument)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var tests []Crowdtest
	return tests, db.Where("submitter_user_id = ? AND deleted_at IS NULL", submitterUserID).
		Order("created_at DESC, id DESC").Limit(limit).Find(&tests).Error
}
