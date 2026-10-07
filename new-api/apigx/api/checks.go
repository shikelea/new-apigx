package apigxapi

// 持续检测配置、众测、以及工具页面的速度 / 掺水检测接口。
//
// 三块的信任边界完全不同，所以刻意分开：
//
//   - 持续检测（/relay-shares/:publicID/checks）只有中转主人能配，跑的时候用的是
//     库里已加密的凭据，请求体里不出现 Key。
//   - 众测（/crowdtests）是提交者自测：Key 由请求体带来，只在内存里用一次，测完即丢，
//     不入库、不进日志。公布出去的只有分数与证据，**不含被测地址**——否则公开的众测
//     列表就成了一份可用中转清单，而且陌生人能照着烧别人的额度。
//   - 工具页面的速度 / 掺水检测（/tools/*）两种来源都支持：选自己的中转（走库里凭据）
//     或临时填一组地址 + Key（同样只在内存里用一次）。

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/apigx/modelfp"
	"github.com/QuantumNous/new-api/apigx/relayprobe"
	"github.com/QuantumNous/new-api/apigx/secretbox"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterCrowdtestRoutes 注册众测与工具检测接口。众测的读接口对未登录访客开放，
// 写接口要求登录。
func RegisterCrowdtestRoutes(r *gin.RouterGroup, db *gorm.DB) {
	h := &handler{db: db}
	toolsGate := middleware.HeaderNavModuleEnabled("tools")

	crowdtests := r.Group("/crowdtests")
	{
		crowdtests.GET("", h.listPublicCrowdtests)
		crowdtests.GET("/mine", middleware.UserAuth(), h.listMyCrowdtests)
		crowdtests.POST("", middleware.UserAuth(), toolsGate,
			middleware.UserCriticalRateLimit("crowdtest_run"), h.createCrowdtest)
		crowdtests.POST("/:publicID/publish", middleware.UserAuth(), h.publishCrowdtest)
	}

	tools := r.Group("/tools", toolsGate)
	{
		tools.GET("/dilution-models", h.listDilutionModels)
		tools.POST("/target-models", middleware.UserAuth(), middleware.UserCriticalRateLimit("relay_target_models"), h.listTargetModels)
		tools.POST("/speed-test", middleware.UserAuth(), middleware.UserCriticalRateLimit("relay_speed_test"), h.runSpeedTest)
		tools.POST("/dilution-test", middleware.UserAuth(), middleware.UserCriticalRateLimit("relay_dilution_test"), h.runDilutionTest)
	}
}

// listDilutionModels 返回掺水检测的收录型号名单。前端据此渲染型号卡片选择器，而不是让
// 用户自由填一个我们根本没有基准的型号名。
//
// 可选档位一律按基准包现算（MeowReadyTiers），不信收录项里写的那份：写死的名单会和基准脱节，
// 展开不出计划的型号（只有旧手填基准的那些）就会变成点了必然测不出来的卡片。
func (h *handler) listDilutionModels(c *gin.Context) {
	entries := modelfp.Registry()
	models := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		names := make([]string, 0, len(entry.RequestNames))
		var tiers []string
		for _, name := range entry.RequestNames {
			avail := modelfp.MeowReadyTiers(name)
			if len(avail) == 0 {
				continue
			}
			names = append(names, name)
			if tiers == nil {
				tiers = avail
				continue
			}
			// 一条收录项有多个请求名时取交集：宁可少列一档，也不给某个名字承诺它没有的档位。
			shared := make([]string, 0, len(tiers))
			for _, tier := range tiers {
				if slices.Contains(avail, tier) {
					shared = append(shared, tier)
				}
			}
			tiers = shared
		}
		if len(names) == 0 || len(tiers) == 0 {
			continue
		}
		models = append(models, gin.H{
			"id":             entry.ID,
			"display_name":   entry.DisplayName,
			"vendor":         entry.Vendor,
			"request_names":  names,
			"baseline_state": entry.BaselineState,
			"baseline_meta":  entry.BaselineMeta,
			"ready_tiers":    tiers,
		})
	}
	ok(c, gin.H{
		"models":         models,
		"baseline_ready": modelfp.BaselineReady(),
		"disclaimer":     relayprobe.DilutionDisclaimer,
	})
}

const (
	checkModelMaxRunes = 191
	checkKeyMaxLen     = 4096
	crowdtestWindow    = int64(24 * 60 * 60)
)

// probeTargetRequest 是三个检测接口共用的目标描述。二选一：给中转 publicID 走库里的
// 加密凭据，或者直接给地址 + Key 做一次性检测。
type probeTargetRequest struct {
	RelaySharePublicID string `json:"relay_share_public_id"`
	BaseURL            string `json:"base_url"`
	Protocol           string `json:"protocol"`
	Key                string `json:"key"`
	Model              string `json:"model"`
	Tier               string `json:"tier"`
	Concurrency        int    `json:"concurrency"`
}

// resolvedTarget 只在内存里存在。key 从不回写响应，也从不入库。
type resolvedTarget struct {
	baseURL  string
	protocol string
	key      string
	model    string
	shareID  int64
}

// resolveProbeTarget 解析检测目标并做输入校验。失败时已经写好响应，调用方直接 return。
func (h *handler) resolveProbeTarget(c *gin.Context, owner int64, req probeTargetRequest) (*resolvedTarget, bool) {
	target, okBase := h.resolveProbeBase(c, owner, req)
	if !okBase {
		return nil, false
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		fail(c, http.StatusBadRequest, "invalid_argument", "model is required")
		return nil, false
	}
	if utf8.RuneCountInString(model) > checkModelMaxRunes {
		fail(c, http.StatusBadRequest, "invalid_argument", "model is too long")
		return nil, false
	}
	target.model = model
	return target, true
}

// resolveProbeBase 只解析「地址 + 凭据」这一半：查型号清单不需要型号名，其余校验与检测
// 目标一致，不该抄第二份。
func (h *handler) resolveProbeBase(c *gin.Context, owner int64, req probeTargetRequest) (*resolvedTarget, bool) {
	if publicID := strings.TrimSpace(req.RelaySharePublicID); publicID != "" {
		share, err := h.getOwnedRelayByPublicID(owner, publicID)
		if err != nil {
			failFromRepo(c, err)
			return nil, false
		}
		credential, err := apigxmodel.GetRelayCredential(h.db, owner, share.ID)
		if err != nil {
			failFromRepo(c, err)
			return nil, false
		}
		key, err := secretbox.Decrypt(credential.SecretRef)
		if err != nil {
			fail(c, http.StatusServiceUnavailable, "credential_unavailable", "检测凭据不可用")
			return nil, false
		}
		return &resolvedTarget{baseURL: share.BaseURL, protocol: share.Protocol, key: key, shareID: share.ID}, true
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	key := strings.TrimSpace(req.Key)
	if baseURL == "" || key == "" {
		fail(c, http.StatusBadRequest, "invalid_argument", "relay share or base URL with key is required")
		return nil, false
	}
	if len(baseURL) > relayBaseURLMaxLen || len(key) > checkKeyMaxLen {
		fail(c, http.StatusBadRequest, "invalid_argument", "base URL or key is too long")
		return nil, false
	}
	// 这里只做长度与协议白名单校验；SSRF 判定统一交给 relayprobe.ValidateBaseURL，
	// 避免两处规则各自演化。
	// 协议留空即视为 OpenAI 兼容；relayprobe 自己会拒掉不支持的协议。
	return &resolvedTarget{baseURL: baseURL, protocol: strings.TrimSpace(req.Protocol), key: key}, true
}

// listTargetModels 返回该目标实际提供的型号名。掺水检测的型号卡片要同时满足两个条件才能点：
// 我们测过这个型号的基准，且这个中转真的提供这个名字。只看前者会摆出一堆点了必然全部失败
// 的卡片——2026-10-07 实测，某中转只提供国产型号，界面里的 gpt-6-sol／claude-sonnet-5
// 一律回「模型不可用」。
func (h *handler) listTargetModels(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req probeTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	target, okTarget := h.resolveProbeBase(c, owner, req)
	if !okTarget {
		return
	}
	result := relayprobe.Probe(c.Request.Context(), target.baseURL, target.protocol, target.key)
	if len(result.Models) == 0 {
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "目标接口没有返回型号清单"
		}
		fail(c, http.StatusBadRequest, "target_models_unavailable", reason)
		return
	}
	ok(c, gin.H{"models": result.Models})
}

type crowdtestRequest struct {
	probeTargetRequest
	Benchmark string `json:"benchmark"`
	// Publish 由提交者决定。true 直接公布，false 存成只有自己能看的草稿。
	Publish bool `json:"publish"`
}

func (h *handler) createCrowdtest(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req crowdtestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	benchmark := strings.TrimSpace(req.Benchmark)
	if benchmark == "" {
		benchmark = apigxmodel.BenchmarkCandy
	}
	if !apigxmodel.ValidBenchmark(benchmark) {
		fail(c, http.StatusBadRequest, "invalid_argument", "benchmark must be candy or pelican")
		return
	}
	target, okTarget := h.resolveProbeTarget(c, owner, req.probeTargetRequest)
	if !okTarget {
		return
	}
	// 日配额按提交者算：众测花的是他自己的额度，这道闸只为拦住脚本刷榜。
	used, err := apigxmodel.CountCrowdtestsSince(h.db, common.GetTimestamp()-crowdtestWindow, owner)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	if used >= apigxmodel.CrowdtestDailyPerUser {
		fail(c, http.StatusTooManyRequests, "crowdtest_quota", "今日众测次数已达上限，请明天再试")
		return
	}
	result, runErr := relayprobe.RunBenchmark(c.Request.Context(), benchmark, target.baseURL, target.protocol, target.key, target.model)
	if runErr != nil {
		// 失败也留痕：否则「测了但没出分」在自己的列表里是一个无法解释的空洞。
		record := &apigxmodel.Crowdtest{
			SubmitterUserID: owner, Benchmark: benchmark, Model: target.model,
			Status: apigxmodel.CrowdtestStatusFailed, Evidence: truncateRunes(runErr.Error(), 460),
			Visibility: apigxmodel.CrowdtestVisibilityDraft,
		}
		if createErr := apigxmodel.CreateCrowdtest(h.db, record); createErr != nil {
			failFromRepo(c, createErr)
			return
		}
		fail(c, http.StatusBadRequest, "crowdtest_failed", runErr.Error())
		return
	}
	visibility := apigxmodel.CrowdtestVisibilityDraft
	if req.Publish {
		visibility = apigxmodel.CrowdtestVisibilityPublic
	}
	// 跑完了但没能判定（比如回复被 max_tokens 截断）记 pending，不记 scored：
	// 那是一个 0 分却不代表模型答错的结果，标成「已评分」会误导看榜的人。
	status := apigxmodel.CrowdtestStatusScored
	if result.Status == apigxmodel.RelayCheckUnknown {
		status = apigxmodel.CrowdtestStatusPending
	}
	record := &apigxmodel.Crowdtest{
		SubmitterUserID: owner, Benchmark: benchmark, Model: target.model,
		Status: status, Score: result.Score,
		Evidence: truncateRunes(result.Evidence, 460), Artifact: result.Artifact,
		Visibility: visibility,
	}
	if err := apigxmodel.CreateCrowdtest(h.db, record); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, crowdtestJSON(*record, true))
}

func (h *handler) publishCrowdtest(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	publicID := strings.TrimSpace(c.Param("publicID"))
	if err := apigxmodel.PublishCrowdtest(h.db, publicID, owner); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, gin.H{"public_id": publicID, "visibility": apigxmodel.CrowdtestVisibilityPublic})
}

func (h *handler) listPublicCrowdtests(c *gin.Context) {
	tests, err := apigxmodel.ListPublicCrowdtests(h.db, strings.TrimSpace(c.Query("benchmark")), 50)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	names := crowdtestSubmitterNames(h.db, tests)
	items := make([]gin.H, 0, len(tests))
	for _, test := range tests {
		item := crowdtestJSON(test, false)
		item["submitter"] = names[test.SubmitterUserID]
		items = append(items, item)
	}
	ok(c, gin.H{"items": items, "disclaimer": relayprobe.BenchmarkDisclaimer})
}

func (h *handler) listMyCrowdtests(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	tests, err := apigxmodel.ListMyCrowdtests(h.db, owner, 50)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(tests))
	for _, test := range tests {
		items = append(items, crowdtestJSON(test, true))
	}
	ok(c, gin.H{"items": items, "daily_limit": apigxmodel.CrowdtestDailyPerUser})
}

// crowdtestJSON 序列化一条众测。响应里没有地址、没有凭据，只有结果本身。
func crowdtestJSON(test apigxmodel.Crowdtest, isOwner bool) gin.H {
	payload := gin.H{
		"public_id": test.PublicID, "benchmark": test.Benchmark, "model": test.Model,
		"status": test.Status, "score": test.Score, "evidence": test.Evidence,
		"published": test.PublishedAt > 0, "created_at": test.CreatedAt,
	}
	if test.Artifact != "" {
		payload["artifact"] = test.Artifact
	}
	if isOwner {
		payload["visibility"] = test.Visibility
	}
	return payload
}

func crowdtestSubmitterNames(db *gorm.DB, tests []apigxmodel.Crowdtest) map[int64]string {
	ids := make([]int64, 0, len(tests))
	for _, test := range tests {
		ids = append(ids, test.SubmitterUserID)
	}
	return displayNamesByUserID(db, ids)
}

func (h *handler) runSpeedTest(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req probeTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	target, okTarget := h.resolveProbeTarget(c, owner, req)
	if !okTarget {
		return
	}
	report, err := relayprobe.RunSpeedTest(c.Request.Context(), target.baseURL, target.protocol, target.key, target.model)
	if err != nil {
		fail(c, http.StatusBadRequest, "speed_test_failed", err.Error())
		return
	}
	ok(c, report)
}

func (h *handler) runDilutionTest(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	var req probeTargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	target, okTarget := h.resolveProbeTarget(c, owner, req)
	if !okTarget {
		return
	}
	tier := strings.ToLower(strings.TrimSpace(req.Tier))
	if tier == "" {
		tier = modelfp.MeowDefaultTier
	}
	if !modelfp.ValidMeowTier(tier) {
		fail(c, http.StatusBadRequest, "invalid_tier", "档位只能是 low、medium 或 high")
		return
	}
	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	} else if concurrency > 8 {
		concurrency = 8
	}
	report, err := relayprobe.RunDilutionTestWith(c.Request.Context(), target.baseURL, target.protocol, target.key, target.model, tier, concurrency)
	if err != nil {
		fail(c, http.StatusBadRequest, "dilution_test_failed", err.Error())
		return
	}
	ok(c, report)
}

// checkSettingsRequest 是中转的持续检测设置。分享表单与后续的设置页共用同一结构，
// 省掉一份容易走样的副本。
type checkSettingsRequest struct {
	AvailabilityMode     *int     `json:"availability_mode"`
	AvailabilityInterval *int     `json:"availability_interval"`
	DegradeMode          *int     `json:"degrade_mode"`
	DegradeInterval      *int     `json:"degrade_interval"`
	DegradeModels        []string `json:"degrade_models"`
	DilutionMode         *int     `json:"dilution_mode"`
	DilutionInterval     *int     `json:"dilution_interval"`
	DilutionModels       []string `json:"dilution_models"`
	Paused               *bool    `json:"paused"`
}

// applyTo 把请求里给出的字段写进配置。指针字段没给就保持原值，所以同一个结构既能
// 用于「首次设置」也能用于「只改一项」。
func (req checkSettingsRequest) applyTo(config *apigxmodel.RelayCheckConfig) error {
	assignInt := func(target *int, value *int) {
		if value != nil {
			*target = *value
		}
	}
	assignInt(&config.AvailabilityMode, req.AvailabilityMode)
	assignInt(&config.AvailabilityInterval, req.AvailabilityInterval)
	assignInt(&config.DegradeMode, req.DegradeMode)
	assignInt(&config.DegradeInterval, req.DegradeInterval)
	assignInt(&config.DilutionMode, req.DilutionMode)
	assignInt(&config.DilutionInterval, req.DilutionInterval)
	if req.Paused != nil {
		config.OwnerPaused = *req.Paused
	}
	if req.DegradeModels != nil {
		if err := config.SetModels(apigxmodel.ProbeKindDegrade, req.DegradeModels); err != nil {
			return err
		}
	}
	if req.DilutionModels != nil {
		if err := config.SetModels(apigxmodel.ProbeKindDilution, req.DilutionModels); err != nil {
			return err
		}
	}
	return nil
}

// defaultCheckConfig 是新中转的默认值：只开公开的可用率，降智与掺水默认关闭。
// 后两者要花上游额度，不该在主人没点头的情况下自动跑起来。
func defaultCheckConfig(owner, shareID int64) *apigxmodel.RelayCheckConfig {
	return &apigxmodel.RelayCheckConfig{
		RelayShareID: shareID, OwnerUserID: owner,
		AvailabilityMode: apigxmodel.CheckVisibilityPublic, AvailabilityInterval: apigxmodel.CheckInterval30Min,
		DegradeMode: apigxmodel.CheckVisibilityOff, DegradeInterval: apigxmodel.CheckInterval1Hour,
		DilutionMode: apigxmodel.CheckVisibilityOff, DilutionInterval: apigxmodel.CheckInterval3Hour,
	}
}

func (h *handler) setRelayChecks(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	share, err := h.getOwnedRelayByPublicID(owner, relayPublicID(c))
	if err != nil {
		failFromRepo(c, err)
		return
	}
	var req checkSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	config, err := apigxmodel.GetRelayCheckConfig(h.db, share.ID)
	if err != nil {
		config = defaultCheckConfig(owner, share.ID)
	}
	if err := req.applyTo(config); err != nil {
		failFromRepo(c, err)
		return
	}
	if err := apigxmodel.UpsertRelayCheckConfig(h.db, config); err != nil {
		failFromRepo(c, err)
		return
	}
	ok(c, checkConfigJSON(config))
}

func (h *handler) getRelayChecks(c *gin.Context) {
	owner, valid := h.ownerID(c)
	if !valid {
		return
	}
	share, err := h.getOwnedRelayByPublicID(owner, relayPublicID(c))
	if err != nil {
		failFromRepo(c, err)
		return
	}
	config, err := apigxmodel.GetRelayCheckConfig(h.db, share.ID)
	if err != nil {
		config = defaultCheckConfig(owner, share.ID)
	}
	ok(c, checkConfigJSON(config))
}

func checkConfigJSON(config *apigxmodel.RelayCheckConfig) gin.H {
	return gin.H{
		"availability_mode": config.AvailabilityMode, "availability_interval": config.AvailabilityInterval,
		"degrade_mode": config.DegradeMode, "degrade_interval": config.DegradeInterval,
		"degrade_models": config.Models(apigxmodel.ProbeKindDegrade),
		"dilution_mode":  config.DilutionMode, "dilution_interval": config.DilutionInterval,
		"dilution_models": config.Models(apigxmodel.ProbeKindDilution),
		"paused":          config.OwnerPaused, "intervals": apigxmodel.CheckIntervals,
	}
}

// listRelaySamples 返回一个中转的检测采样。访客只看得到公开项；判断是否为主人靠登录态，
// 未登录一律按访客处理。
func (h *handler) listRelaySamples(c *gin.Context) {
	publicID := relayPublicID(c)
	if publicID == "" {
		fail(c, http.StatusBadRequest, "invalid_id", "invalid public id")
		return
	}
	var share apigxmodel.RelayShare
	err := h.db.Where("public_id = ? AND status = ? AND alive_flag = ?", publicID, apigxmodel.RelayStatusPublished, apigxmodel.AliveFlagAlive).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusNotFound, "not_found", "relay share not found")
		return
	}
	if err != nil {
		failFromRepo(c, err)
		return
	}
	viewerIsOwner := int64(c.GetInt("id")) == share.OwnerUserID && share.OwnerUserID > 0
	kind := strings.TrimSpace(c.Query("kind"))
	samples, err := apigxmodel.ListRelayProbeSamples(h.db, share.ID, kind, viewerIsOwner, 100)
	if err != nil {
		failFromRepo(c, err)
		return
	}
	items := make([]gin.H, 0, len(samples))
	for _, sample := range samples {
		items = append(items, gin.H{
			"kind": sample.Kind, "model": sample.Model, "status": sample.Status, "score": sample.Score,
			"latency_ms": sample.LatencyMs, "first_token_ms": sample.FirstTokenMs, "output_tps": sample.OutputTPS,
			"evidence": sample.Evidence, "probed_at": sample.ProbedAt,
		})
	}
	ok(c, gin.H{"items": items, "viewer_is_owner": viewerIsOwner})
}

// truncateRunes 按 rune 截断，避免把中文切成半个字符写进定长列。
func truncateRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
