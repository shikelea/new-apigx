package apigxmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 模型供给条目（`apigx_model_offerings`）：
// **同一个上游模型名，在不同分享渠道上就是不同的供给**——价格、能力、限额都可以不同。
//
// 唯一性用 `OfferingKeyHash`（渠道 + 上游模型名）表达，并与 `AliveFlag` 组成唯一索引：
// 存活期间同一渠道内同一模型只有一条供给；软删除后可重新发布（与其它表同一套墓碑语义）。

// OfferingStatus 取值：供给的发布状态。
const (
	// OfferingStatusDraft 草稿（分享者在编辑，不对外可见）。
	OfferingStatusDraft int = 0
	// OfferingStatusPending 待审核（已提交，等待平台审核）。
	OfferingStatusPending int = 1
	// OfferingStatusPublished 已发布（对外可见、可被路由）。
	OfferingStatusPublished int = 2
	// OfferingStatusRejected 已驳回（审核未通过，分享者可修改后重新提交）。
	OfferingStatusRejected int = 3
)

// Capability 描述一个模型在**这条供给**上实际支持的能力。
//
// 底座的 `relaykit` 转换器是按**模型名前缀**硬编码能力的，没有"按供给声明能力"的位置；
// 因此 APIGX 必须自己承载它（见 docs/architecture/baseline.md 的 E4）。
type Capability struct {
	Streaming bool `json:"streaming"`
	Vision    bool `json:"vision"`
	Tools     bool `json:"tools"`
	Reasoning bool `json:"reasoning"`
}

// OfferingReviewContext 是平台审核动作的脱敏上下文。
// ReasonCode 只接受短理由码，不接收自由文本，避免管理员误把凭据写入不可变审计表。
type OfferingReviewContext struct {
	ReviewerUserID int64
	ReviewerRole   int
	ReviewerKeyID  int64
	ReasonCode     string
	RequestID      string
	IPHash         string
	UserAgent      string
}

// OfferingReviewResult 返回审核对象的 owner 与状态变化，供 API 响应和后续投影使用。
type OfferingReviewResult struct {
	OfferingID       int64
	OfferingPublicID string
	OwnerUserID      int64
	FromStatus       int
	ToStatus         int
}

// OfferingReviewItem 是管理员待审核队列所需的最小跨 owner 投影。
// 不包含上游地址、Key、账户池或任何凭据材料。
type OfferingReviewItem struct {
	OfferingID          int64
	OfferingPublicID    string
	OwnerUserID         int64
	ShareGroupID        int64
	ShareGroupPublicID  string
	ShareGroupName      string
	GroupVisibility     int
	ChannelID           int64
	ChannelPublicID     string
	ChannelName         string
	UpstreamModelID     string
	CanonicalModelID    int64
	CanonicalAPIModelID string
	Capabilities        string
	Status              int
	Revision            int64
	CreatedAt           int64
	UpdatedAt           int64
}

// OfferingKeyHashFor 计算供给的业务键哈希（渠道 + 上游模型名）。
//
// 用哈希而不是把两列一起放进唯一索引，是为了让「墓碑」语义只依赖一个列：
// 软删除时把 AliveFlag 置为本行 ID 即可放行业务键。
func OfferingKeyHashFor(channelID int64, upstreamModelID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("apigx:offering:%d:%s", channelID, upstreamModelID)))
	return hex.EncodeToString(sum[:])
}

// CreateModelOffering 新增一条供给（已校验渠道归属）。
//
// 新供给一律从**草稿**开始：发布需要显式动作（提交审核 → 平台发布），
// 避免"加了模型就自动对外可见"。
func CreateModelOffering(db *gorm.DB, ownerUserID int64, offering *ModelOffering) error {
	if offering == nil || offering.ChannelID <= 0 {
		return fmt.Errorf("%w: channel_id is required", ErrInvalidArgument)
	}
	if _, err := GetChannel(db, ownerUserID, offering.ChannelID); err != nil {
		return err
	}
	if offering.UpstreamModelID == "" {
		return fmt.Errorf("%w: upstream_model_id is required", ErrInvalidArgument)
	}
	if offering.Capabilities == "" {
		encoded, err := common.Marshal(Capability{})
		if err != nil {
			return err
		}
		offering.Capabilities = string(encoded)
	}
	if offering.PublicID == "" {
		offering.PublicID = newPublicID()
	}
	offering.OfferingKeyHash = OfferingKeyHashFor(offering.ChannelID, offering.UpstreamModelID)
	offering.Status = OfferingStatusDraft
	offering.Revision = 1
	offering.AliveFlag = AliveFlagAlive
	touchTimestamps(true, &offering.CreatedAt, &offering.UpdatedAt)
	if err := db.Create(offering).Error; err != nil {
		return err
	}
	return nil
}

// GetModelOffering 按所有者读取供给。
func GetModelOffering(db *gorm.DB, ownerUserID, id int64) (*ModelOffering, error) {
	var offering ModelOffering
	if err := db.Where("id = ?", id).First(&offering).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// 归属经渠道传递（供给没有 owner 列）
	if _, err := GetChannel(db, ownerUserID, offering.ChannelID); err != nil {
		return nil, ErrNotFound
	}
	return &offering, nil
}

// ListModelOfferingsByChannel 列出某渠道下的全部存活供给（已校验渠道归属）。
func ListModelOfferingsByChannel(db *gorm.DB, ownerUserID, channelID int64) ([]ModelOffering, error) {
	if _, err := GetChannel(db, ownerUserID, channelID); err != nil {
		return nil, err
	}
	var offerings []ModelOffering
	err := db.Where("channel_id = ?", channelID).Order("id ASC").Find(&offerings).Error
	return offerings, err
}

// UpdateModelOfferingCapabilities 更新能力声明并递增修订号（能力是路由与展示的依据）。
func UpdateModelOfferingCapabilities(db *gorm.DB, ownerUserID, id int64, caps Capability) error {
	offering, err := GetModelOffering(db, ownerUserID, id)
	if err != nil {
		return err
	}
	encoded, err := common.Marshal(caps)
	if err != nil {
		return err
	}
	return db.Model(&ModelOffering{}).Where("id = ? AND alive_flag = ?", offering.ID, AliveFlagAlive).
		Updates(map[string]any{
			"capabilities": string(encoded),
			"revision":     offering.Revision + 1,
			"updated_at":   common.GetTimestamp(),
		}).Error
}

// SubmitModelOffering 把草稿（或已驳回）提交为待审核。
//
// 状态机刻意做窄：**已发布的供给不能靠提交动作改状态**（改价/改能力应当走修订流程），
// 否则一次误点就会让已对外可见的供给退回待审核、对使用者表现为"模型突然消失"。
func SubmitModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	if db == nil {
		return errors.New("apigxmodel: nil db")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		offering, err := GetModelOffering(tx, ownerUserID, id)
		if err != nil {
			return err
		}
		if offering.Status != OfferingStatusDraft && offering.Status != OfferingStatusRejected {
			return fmt.Errorf("%w: offering %d cannot move from status %d to %d", ErrInvalidArgument, id, offering.Status, OfferingStatusPending)
		}

		// A previously confirmed platform mapping is reusable. A sharer may have
		// proposed a different target, in which case an administrator must resolve
		// the conflict instead of silently overwriting that proposal.
		proposal, hasProposal, err := GetOfferingAliasProposal(tx, offering.ID)
		if err != nil {
			return err
		}
		canonical, resolved, err := ResolveCanonicalModel(tx, offering.UpstreamModelID)
		if err != nil {
			return err
		}
		if resolved && (!hasProposal || proposal.TargetCanonicalModelID == 0 || proposal.TargetCanonicalModelID == canonical.ID) {
			offering.CanonicalModelID = canonical.ID
			if !hasProposal {
				proposal, err = upsertOfferingAliasProposal(tx, offering, canonical.ID)
				if err != nil {
					return err
				}
			}
			if err := approveOfferingAliasProposal(tx, proposal.ID, canonical.ID, 0); err != nil {
				return err
			}
		} else {
			targetCanonicalID := int64(0)
			if hasProposal {
				targetCanonicalID = proposal.TargetCanonicalModelID
			}
			// Resubmitting a rejected identity must reopen its proposal. Keeping the
			// rejected alias unchanged would leave the offering pending forever while
			// it remains absent from the administrator's pending identity queue.
			if _, err := upsertOfferingAliasProposal(tx, offering, targetCanonicalID); err != nil {
				return err
			}
			offering.CanonicalModelID = 0
		}

		updated := tx.Model(&ModelOffering{}).
			Where("id = ? AND alive_flag = ? AND status IN ?", offering.ID, AliveFlagAlive,
				[]int{OfferingStatusDraft, OfferingStatusRejected}).
			Updates(map[string]any{
				"status": OfferingStatusPending, "canonical_model_id": offering.CanonicalModelID,
				"updated_at": common.GetTimestamp(),
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("%w: offering state changed", ErrInvalidArgument)
		}
		return nil
	})
}

// PublishModelOffering 平台审核通过（待审核 → 已发布）。
func PublishModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	return transitionOffering(db, ownerUserID, id, []int{OfferingStatusPending}, OfferingStatusPublished)
}

// RejectModelOffering 平台驳回（待审核 → 已驳回）。
func RejectModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	return transitionOffering(db, ownerUserID, id, []int{OfferingStatusPending}, OfferingStatusRejected)
}

// ListOfferingsForReview 返回平台审核队列。它是 reviewer-scoped 读取，因此刻意不接 owner 参数；
// API 层必须先验证管理员角色。分享者自己的列表仍走 ListModelOfferingsByChannel 的 owner 约束。
func ListOfferingsForReview(db *gorm.DB, status, offset, limit int) ([]OfferingReviewItem, int64, error) {
	if db == nil {
		return nil, 0, errors.New("apigxmodel: nil db")
	}
	if status != -1 && (status < OfferingStatusDraft || status > OfferingStatusRejected) {
		return nil, 0, fmt.Errorf("%w: invalid offering review status", ErrInvalidArgument)
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	offeringTable := ModelOffering{}.TableName()
	channelTable := Channel{}.TableName()
	groupTable := ShareGroup{}.TableName()
	canonicalTable := CanonicalModel{}.TableName()
	base := db.Table(offeringTable+" AS o").
		Joins("JOIN "+channelTable+" AS c ON c.id = o.channel_id AND c.deleted_at IS NULL AND c.alive_flag = ?", AliveFlagAlive).
		Joins("JOIN "+groupTable+" AS g ON g.id = c.share_group_id AND g.deleted_at IS NULL AND g.alive_flag = ?", AliveFlagAlive).
		Joins("LEFT JOIN "+canonicalTable+" AS cm ON cm.id = o.canonical_model_id").
		Where("o.deleted_at IS NULL AND o.alive_flag = ?", AliveFlagAlive)
	if status != -1 {
		base = base.Where("o.status = ?", status)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []OfferingReviewItem
	err := base.Select("o.id AS offering_id, o.public_id AS offering_public_id, " +
		"c.owner_user_id AS owner_user_id, g.id AS share_group_id, g.public_id AS share_group_public_id, " +
		"g.name AS share_group_name, g.default_visibility AS group_visibility, " +
		"c.id AS channel_id, c.public_id AS channel_public_id, c.name AS channel_name, " +
		"o.upstream_model_id AS upstream_model_id, o.canonical_model_id AS canonical_model_id, " +
		"cm.api_model_id AS canonical_api_model_id, o.capabilities AS capabilities, " +
		"o.status AS status, o.revision AS revision, o.created_at AS created_at, o.updated_at AS updated_at").
		Order("o.updated_at ASC, o.id ASC").Offset(offset).Limit(limit).Scan(&items).Error
	return items, total, err
}

// ReviewModelOffering 执行跨 owner 的平台审核，并把状态变化与脱敏审核上下文写入同一事务。
// 条件更新保证两个管理员同时审核同一条供给时最多一个成功；审计写入失败会回滚状态变化。
func ReviewModelOffering(db *gorm.DB, id int64, toStatus int, review OfferingReviewContext) (*OfferingReviewResult, error) {
	if db == nil {
		return nil, errors.New("apigxmodel: nil db")
	}
	if id <= 0 || review.ReviewerUserID <= 0 || review.ReviewerRole < common.RoleAdminUser {
		return nil, fmt.Errorf("%w: offering and reviewer are required", ErrInvalidArgument)
	}
	if toStatus != OfferingStatusPublished && toStatus != OfferingStatusRejected {
		return nil, fmt.Errorf("%w: unsupported review decision", ErrInvalidArgument)
	}
	reasonCode := strings.TrimSpace(review.ReasonCode)
	if !validOfferingReviewReasonCode(reasonCode) {
		return nil, fmt.Errorf("%w: review reason code is required", ErrInvalidArgument)
	}
	if len(review.RequestID) > 64 || (review.IPHash != "" && len(review.IPHash) != 64) || len([]rune(review.UserAgent)) > 255 {
		return nil, fmt.Errorf("%w: invalid review audit context", ErrInvalidArgument)
	}

	result := &OfferingReviewResult{}
	err := db.Transaction(func(tx *gorm.DB) error {
		var offering ModelOffering
		if err := tx.Where("id = ? AND alive_flag = ?", id, AliveFlagAlive).First(&offering).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		var channel Channel
		if err := tx.Select("id", "owner_user_id", "share_group_id").Where("id = ? AND alive_flag = ?", offering.ChannelID, AliveFlagAlive).First(&channel).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if offering.Status != OfferingStatusPending {
			return fmt.Errorf("%w: offering %d cannot move from status %d to %d", ErrInvalidArgument, id, offering.Status, toStatus)
		}
		if toStatus == OfferingStatusPublished && offering.CanonicalModelID <= 0 {
			return fmt.Errorf("%w: model identity must be confirmed before publishing", ErrInvalidArgument)
		}

		now := common.GetTimestamp()
		updates := map[string]any{"status": toStatus, "updated_at": now}
		if toStatus == OfferingStatusPublished {
			updates["verified_at"] = now
		}
		updated := tx.Model(&ModelOffering{}).
			Where("id = ? AND alive_flag = ? AND status = ?", offering.ID, AliveFlagAlive, OfferingStatusPending).
			Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("%w: offering was already reviewed", ErrInvalidArgument)
		}
		if toStatus == OfferingStatusPublished {
			var group ShareGroup
			if err := tx.Select("id", "publish_status").
				Where("id = ? AND alive_flag = ?", channel.ShareGroupID, AliveFlagAlive).
				First(&group).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNotFound
				}
				return err
			}
			switch group.PublishStatus {
			case ShareGroupPublishDraft, ShareGroupPublishPending:
				if err := tx.Model(&ShareGroup{}).Where("id = ?", group.ID).
					Update("publish_status", ShareGroupPublishPublished).Error; err != nil {
					return err
				}
			case ShareGroupPublishPublished:
				// Already active.
			default:
				return fmt.Errorf("%w: share group is suspended or retired", ErrInvalidArgument)
			}
		}

		action := "offering_reject"
		if toStatus == OfferingStatusPublished {
			action = "offering_publish"
		}
		summary, err := common.Marshal(map[string]any{
			"owner_user_id": channel.OwnerUserID,
			"from_status":   OfferingStatusPending,
			"to_status":     toStatus,
			"reason_code":   reasonCode,
		})
		if err != nil {
			return err
		}
		event := &AuditEvent{
			EventUID:       common.NewRequestId(),
			ActorUserID:    review.ReviewerUserID,
			ActorRole:      review.ReviewerRole,
			ActorApiKeyID:  review.ReviewerKeyID,
			Category:       "admin",
			Action:         action,
			TargetType:     "model_offering",
			TargetID:       offering.ID,
			TargetPublicID: offering.PublicID,
			BeforeDigest:   offeringReviewDigest(offering.ID, channel.OwnerUserID, OfferingStatusPending, offering.Revision),
			AfterDigest:    offeringReviewDigest(offering.ID, channel.OwnerUserID, toStatus, offering.Revision),
			ChangeSummary:  string(summary),
			RequestID:      review.RequestID,
			IpHash:         review.IPHash,
			UserAgent:      review.UserAgent,
			OccurredAt:     now,
			CreatedAt:      now,
		}
		if err := tx.Create(event).Error; err != nil {
			return err
		}

		result = &OfferingReviewResult{
			OfferingID:       offering.ID,
			OfferingPublicID: offering.PublicID,
			OwnerUserID:      channel.OwnerUserID,
			FromStatus:       OfferingStatusPending,
			ToStatus:         toStatus,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func offeringReviewDigest(offeringID, ownerUserID int64, status int, revision int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("offering:%d:owner:%d:status:%d:revision:%d", offeringID, ownerUserID, status, revision)))
	return hex.EncodeToString(sum[:])
}

func validOfferingReviewReasonCode(reason string) bool {
	if reason == "" || len(reason) > 64 {
		return false
	}
	for i := 0; i < len(reason); i++ {
		ch := reason[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func transitionOffering(db *gorm.DB, ownerUserID, id int64, from []int, to int) error {
	offering, err := GetModelOffering(db, ownerUserID, id)
	if err != nil {
		return err
	}
	allowed := false
	for _, candidate := range from {
		if offering.Status == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: offering %d cannot move from status %d to %d", ErrInvalidArgument, id, offering.Status, to)
	}
	if to == OfferingStatusPublished && offering.CanonicalModelID <= 0 {
		return fmt.Errorf("%w: model identity must be confirmed before publishing", ErrInvalidArgument)
	}
	now := common.GetTimestamp()
	updates := map[string]any{"status": to, "updated_at": now}
	if to == OfferingStatusPublished {
		updates["verified_at"] = now
	}
	return db.Model(&ModelOffering{}).Where("id = ? AND alive_flag = ?", offering.ID, AliveFlagAlive).
		Updates(updates).Error
}

// DeleteModelOffering 软删除供给（墓碑语义：同一模型可被重新添加）。
func DeleteModelOffering(db *gorm.DB, ownerUserID, id int64) error {
	offering, err := GetModelOffering(db, ownerUserID, id)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	res := db.Model(&ModelOffering{}).
		Where("id = ? AND alive_flag = ?", offering.ID, AliveFlagAlive).
		Updates(map[string]any{"deleted_at": softDeletedAt(now), "alive_flag": offering.ID, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CapabilityOf 解析供给的能力声明。
func (o *ModelOffering) CapabilityOf() Capability {
	var caps Capability
	if o == nil || o.Capabilities == "" {
		return caps
	}
	_ = common.UnmarshalJsonStr(o.Capabilities, &caps)
	return caps
}

// ImportModelOfferings 批量导入供给（一键获取上游模型列表后的落库动作）。
//
// 幂等：已存在（存活）的同名供给会被跳过而不是报错——上游模型列表反复拉取是常态，
// 报错会让分享者必须先手工比对。返回新建数量与被跳过的模型名，便于界面如实回显。
//
// 新导入的供给仍然是**草稿**：导入不等于发布（发布要显式提交审核）。
func ImportModelOfferings(db *gorm.DB, ownerUserID, channelID int64, upstreamModelIDs []string) (created int, skipped []string, err error) {
	if len(upstreamModelIDs) == 0 {
		return 0, nil, nil
	}
	if _, err := GetChannel(db, ownerUserID, channelID); err != nil {
		return 0, nil, err
	}

	existing := make(map[string]struct{})
	var alive []ModelOffering
	if err := db.Where("channel_id = ?", channelID).Find(&alive).Error; err != nil {
		return 0, nil, err
	}
	for i := range alive {
		existing[alive[i].UpstreamModelID] = struct{}{}
	}

	seen := make(map[string]struct{}, len(upstreamModelIDs))
	for _, modelID := range upstreamModelIDs {
		trimmed := strings.TrimSpace(modelID)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		if _, exists := existing[trimmed]; exists {
			skipped = append(skipped, trimmed)
			continue
		}
		offering := &ModelOffering{ChannelID: channelID, UpstreamModelID: trimmed}
		if err := CreateModelOffering(db, ownerUserID, offering); err != nil {
			return created, skipped, err
		}
		existing[trimmed] = struct{}{}
		created++
	}
	return created, skipped, nil
}

// FindOfferingByUpstreamModel 按「渠道 + 上游模型名」定位存活供给。
//
// 质量统计要按供给归档样本，而供给的身份就是这一对——用列表接口再筛会多一次全量查询。
func FindOfferingByUpstreamModel(db *gorm.DB, channelID int64, upstreamModelID string) (int64, bool, error) {
	if db == nil || channelID <= 0 || upstreamModelID == "" {
		return 0, false, nil
	}
	var offering ModelOffering
	err := db.Where("channel_id = ? AND upstream_model_id = ? AND status = ? AND alive_flag = ?",
		channelID, upstreamModelID, OfferingStatusPublished, AliveFlagAlive).Order("id ASC").First(&offering).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var canonical CanonicalModel
		canonicalErr := db.Where("api_model_id = ? AND status = ?", upstreamModelID, 1).First(&canonical).Error
		if canonicalErr == nil {
			err = db.Where("channel_id = ? AND canonical_model_id = ? AND status = ? AND alive_flag = ?",
				channelID, canonical.ID, OfferingStatusPublished, AliveFlagAlive).Order("id ASC").First(&offering).Error
		} else if !errors.Is(canonicalErr, gorm.ErrRecordNotFound) {
			return 0, false, canonicalErr
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return offering.ID, true, nil
}

// ListPublishedOfferingsByModel 列出某上游模型名下**已发布**的全部供给（跨分享者）。
//
// 用于可解释路由（T2.9）：候选集就是"这个模型当前所有已发布的供给"。
// 只返回已发布（草稿/待审核/已驳回不对外），与广场的可见性判据保持一致。
func ListPublishedOfferingsByModel(db *gorm.DB, upstreamModelID string) ([]ModelOffering, error) {
	if db == nil || upstreamModelID == "" {
		return nil, nil
	}
	var offerings []ModelOffering
	// 闸门（U-1）：供给"已发布"还不够 —— 它所属的**分享分组**也必须处于「已发布」。
	// 否则草稿/待审核/已暂停的分组会继续对外服务（这正是"新建渠道后直接就确定"的根因）。
	// 用 TableName() 取表名、只做普通列等值比较 ⇒ SQLite/MySQL/PostgreSQL 通用。
	channelTable := Channel{}.TableName()
	groupTable := ShareGroup{}.TableName()
	groupPublished := "channel_id IN (SELECT id FROM " + channelTable +
		" WHERE share_group_id IN (SELECT id FROM " + groupTable +
		" WHERE publish_status = ? AND deleted_at IS NULL) AND deleted_at IS NULL)"
	var canonical CanonicalModel
	canonicalErr := db.Where("api_model_id = ? AND status = ?", upstreamModelID, 1).First(&canonical).Error
	query := db.Where("status = ?", OfferingStatusPublished)
	if canonicalErr == nil {
		query = query.Where("upstream_model_id = ? OR canonical_model_id = ?", upstreamModelID, canonical.ID)
	} else if errors.Is(canonicalErr, gorm.ErrRecordNotFound) {
		query = query.Where("upstream_model_id = ?", upstreamModelID)
	} else {
		return nil, canonicalErr
	}
	err := query.
		Where(groupPublished, ShareGroupPublishPublished).
		Order("id ASC").Find(&offerings).Error
	if err != nil {
		return nil, err
	}
	if canonicalErr != nil {
		return offerings, nil
	}
	// A backing channel can contain multiple upstream aliases that were mapped
	// to one public model. It is still one selectable source, so keep one stable
	// representative per share channel for routing and explanations.
	unique := make([]ModelOffering, 0, len(offerings))
	seenChannels := make(map[int64]struct{}, len(offerings))
	for i := range offerings {
		if _, duplicate := seenChannels[offerings[i].ChannelID]; duplicate {
			continue
		}
		seenChannels[offerings[i].ChannelID] = struct{}{}
		unique = append(unique, offerings[i])
	}
	return unique, nil
}
