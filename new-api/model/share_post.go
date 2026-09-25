package model

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SharePostStatusDraft          = "draft"
	ShareCenterRequireLoginOption = "ShareCenterRequireLogin"
	SharePostStatusPending        = "pending"
	SharePostStatusPublished      = "published"
	SharePostStatusRejected       = "rejected"
	SharePostStatusArchived       = "archived"

	SharePostVisibilityPublic  = "public"
	SharePostVisibilityLogin   = "login"
	SharePostVisibilityPrivate = "private"

	SharePostVoteUsable  = 1
	SharePostVoteInvalid = -1
)

// SharePost is the text-only community sharing entity. It deliberately has no
// relation to APIGX channels, keys, offerings, routing or billing.
type SharePost struct {
	ID           uint           `json:"id" gorm:"primaryKey"`
	PublicID     string         `json:"public_id" gorm:"size:40;uniqueIndex;not null"`
	AuthorID     int            `json:"author_id" gorm:"index;not null"`
	AuthorName   string         `json:"author_name" gorm:"column:author_name;->"`
	Title        string         `json:"title" gorm:"size:160;not null"`
	Summary      string         `json:"summary" gorm:"size:500"`
	Content      string         `json:"content" gorm:"type:text;not null"`
	Visibility   string         `json:"visibility" gorm:"size:16;index;not null;default:public"`
	Status       string         `json:"status" gorm:"size:16;index;not null;default:draft"`
	RejectReason string         `json:"reject_reason,omitempty" gorm:"size:500"`
	UsableVotes  int            `json:"usable_votes" gorm:"not null;default:0"`
	InvalidVotes int            `json:"invalid_votes" gorm:"not null;default:0"`
	PublishedAt  int64          `json:"published_at" gorm:"bigint;index"`
	CreatedAt    int64          `json:"created_at" gorm:"bigint;index"`
	UpdatedAt    int64          `json:"updated_at" gorm:"bigint;index"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

func (SharePost) TableName() string { return "share_posts" }

type SharePostVote struct {
	ID        uint  `json:"id" gorm:"primaryKey"`
	PostID    uint  `json:"post_id" gorm:"uniqueIndex:idx_share_post_vote;not null"`
	UserID    int   `json:"user_id" gorm:"uniqueIndex:idx_share_post_vote;not null"`
	Value     int   `json:"value" gorm:"not null"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (SharePostVote) TableName() string { return "share_post_votes" }

func normalizeSharePostVisibility(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return SharePostVisibilityPublic
	}
	return normalized
}

func validateSharePostInput(title, summary, content, visibility string) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(title)); n < 1 || n > 160 {
		return fmt.Errorf("share title must contain 1-160 characters")
	}
	if utf8.RuneCountInString(summary) > 500 {
		return fmt.Errorf("share summary must contain at most 500 characters")
	}
	if utf8.RuneCountInString(content) < 1 || utf8.RuneCountInString(content) > 100000 {
		return fmt.Errorf("share content must contain 1-100000 characters")
	}
	if visibility != SharePostVisibilityPublic && visibility != SharePostVisibilityLogin && visibility != SharePostVisibilityPrivate {
		return fmt.Errorf("invalid share visibility")
	}
	return nil
}

func (post *SharePost) Validate() error {
	post.Title = strings.TrimSpace(post.Title)
	post.Summary = strings.TrimSpace(post.Summary)
	post.Visibility = normalizeSharePostVisibility(post.Visibility)
	return validateSharePostInput(post.Title, post.Summary, post.Content, post.Visibility)
}

func CreateSharePost(post *SharePost) error {
	if post == nil || post.AuthorID <= 0 {
		return errors.New("invalid share author")
	}
	if err := post.Validate(); err != nil {
		return err
	}
	now := common.GetTimestamp()
	if post.PublicID == "" {
		post.PublicID = common.GetUUID()
	}
	post.Status = SharePostStatusDraft
	post.CreatedAt = now
	post.UpdatedAt = now
	return DB.Create(post).Error
}

func GetSharePostByPublicID(publicID string) (*SharePost, error) {
	return findSharePostByPublicID(DB, publicID)
}

func getSharePostQuery(db *gorm.DB) *gorm.DB {
	return db.Model(&SharePost{}).
		Select("share_posts.*, COALESCE(NULLIF(users.display_name, ''), users.username) AS author_name").
		Joins("LEFT JOIN users ON users.id = share_posts.author_id")
}

func findSharePostByPublicID(db *gorm.DB, publicID string) (*SharePost, error) {
	var post SharePost
	if err := getSharePostQuery(db).Where("share_posts.public_id = ?", publicID).First(&post).Error; err != nil {
		return nil, err
	}
	return &post, nil
}

func sharePostVisibleCondition(query *gorm.DB, viewerID int, requireLogin bool) *gorm.DB {
	query = query.Where("share_posts.status = ?", SharePostStatusPublished)
	if viewerID == 0 {
		if requireLogin {
			return query.Where("1 = 0")
		}
		return query.Where("share_posts.visibility = ?", SharePostVisibilityPublic)
	}
	return query.Where("share_posts.visibility IN (?, ?) OR (share_posts.visibility = ? AND share_posts.author_id = ?)",
		SharePostVisibilityPublic, SharePostVisibilityLogin, SharePostVisibilityPrivate, viewerID)
}

func ListPublishedSharePosts(viewerID int, requireLogin bool, keyword string, offset, limit int) ([]*SharePost, int64, error) {
	query := sharePostVisibleCondition(getSharePostQuery(DB), viewerID, requireLogin)
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("share_posts.title LIKE ? OR share_posts.summary LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var posts []*SharePost
	if err := query.Order("share_posts.published_at DESC, share_posts.id DESC").Offset(offset).Limit(limit).Find(&posts).Error; err != nil {
		return nil, 0, err
	}
	return posts, total, nil
}

func ListOwnedSharePosts(authorID int, offset, limit int) ([]*SharePost, int64, error) {
	query := getSharePostQuery(DB).Where("share_posts.author_id = ?", authorID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var posts []*SharePost
	if err := query.Order("share_posts.updated_at DESC, share_posts.id DESC").Offset(offset).Limit(limit).Find(&posts).Error; err != nil {
		return nil, 0, err
	}
	return posts, total, nil
}

func ListPendingSharePosts(offset, limit int) ([]*SharePost, int64, error) {
	query := getSharePostQuery(DB).Where("share_posts.status = ?", SharePostStatusPending)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var posts []*SharePost
	if err := query.Order("share_posts.updated_at ASC, share_posts.id ASC").Offset(offset).Limit(limit).Find(&posts).Error; err != nil {
		return nil, 0, err
	}
	return posts, total, nil
}

func GetVisibleSharePost(publicID string, viewerID int, requireLogin bool) (*SharePost, error) {
	query := sharePostVisibleCondition(getSharePostQuery(DB), viewerID, requireLogin)
	var post SharePost
	if err := query.Where("share_posts.public_id = ?", publicID).First(&post).Error; err != nil {
		return nil, err
	}
	return &post, nil
}

func GetOwnedSharePost(publicID string, authorID int) (*SharePost, error) {
	var post SharePost
	if err := getSharePostQuery(DB).Where("share_posts.public_id = ? AND share_posts.author_id = ?", publicID, authorID).First(&post).Error; err != nil {
		return nil, err
	}
	return &post, nil
}

func UpdateOwnedSharePost(post *SharePost, authorID int) error {
	if post == nil || post.AuthorID != authorID {
		return gorm.ErrRecordNotFound
	}
	if post.Status != SharePostStatusDraft && post.Status != SharePostStatusRejected {
		return errors.New("only draft or rejected share can be edited")
	}
	if err := post.Validate(); err != nil {
		return err
	}
	post.UpdatedAt = common.GetTimestamp()
	err := DB.Model(&SharePost{}).Where("id = ? AND author_id = ? AND status IN (?, ?)", post.ID, authorID, SharePostStatusDraft, SharePostStatusRejected).
		Select("title", "summary", "content", "visibility", "status", "reject_reason", "updated_at").Updates(map[string]any{
		"title": post.Title, "summary": post.Summary, "content": post.Content, "visibility": post.Visibility,
		"status": SharePostStatusDraft, "reject_reason": "", "updated_at": post.UpdatedAt,
	}).Error
	if err == nil {
		post.Status = SharePostStatusDraft
		post.RejectReason = ""
	}
	return err
}

func SubmitOwnedSharePost(publicID string, authorID int) (*SharePost, error) {
	var post SharePost
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := getSharePostQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("share_posts.public_id = ? AND share_posts.author_id = ?", publicID, authorID).First(&post).Error; err != nil {
			return err
		}
		if post.Status != SharePostStatusDraft && post.Status != SharePostStatusRejected {
			return errors.New("only draft or rejected share can be submitted")
		}
		if err := tx.Model(&SharePost{}).Where("id = ? AND status IN (?, ?)", post.ID, SharePostStatusDraft, SharePostStatusRejected).Updates(map[string]any{
			"status": SharePostStatusPending, "reject_reason": "", "updated_at": common.GetTimestamp(),
		}).Error; err != nil {
			return err
		}
		post.Status = SharePostStatusPending
		post.RejectReason = ""
		return nil
	})
	return &post, err
}

func ReviewSharePost(publicID string, publish bool, rejectReason string) (*SharePost, error) {
	var post SharePost
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := getSharePostQuery(tx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("share_posts.public_id = ?", publicID).First(&post).Error; err != nil {
			return err
		}
		if post.Status != SharePostStatusPending {
			return errors.New("share is not pending review")
		}
		updates := map[string]any{"updated_at": common.GetTimestamp()}
		if publish {
			updates["status"] = SharePostStatusPublished
			updates["reject_reason"] = ""
			updates["published_at"] = common.GetTimestamp()
			post.Status = SharePostStatusPublished
			post.RejectReason = ""
		} else {
			if strings.TrimSpace(rejectReason) == "" {
				return errors.New("reject reason is required")
			}
			updates["status"] = SharePostStatusRejected
			updates["reject_reason"] = strings.TrimSpace(rejectReason)
			updates["published_at"] = 0
			post.Status = SharePostStatusRejected
			post.RejectReason = strings.TrimSpace(rejectReason)
		}
		if err := tx.Model(&SharePost{}).Where("id = ? AND status = ?", post.ID, SharePostStatusPending).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
	return &post, err
}

func ArchiveOwnedSharePost(publicID string, authorID int) error {
	result := DB.Model(&SharePost{}).Where("public_id = ? AND author_id = ? AND status IN (?, ?, ?)", publicID, authorID, SharePostStatusDraft, SharePostStatusRejected, SharePostStatusPublished).
		Updates(map[string]any{"status": SharePostStatusArchived, "updated_at": common.GetTimestamp()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func VoteSharePost(publicID string, userID int, value int) (*SharePost, error) {
	if userID <= 0 || (value != SharePostVoteUsable && value != SharePostVoteInvalid) {
		return nil, errors.New("invalid share vote")
	}
	var post SharePost
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ? AND status = ?", publicID, SharePostStatusPublished).First(&post).Error; err != nil {
			return err
		}
		if post.AuthorID == userID {
			return errors.New("author cannot vote on own share")
		}
		var vote SharePostVote
		err := tx.Where("post_id = ? AND user_id = ?", post.ID, userID).First(&vote).Error
		changed := false
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&SharePostVote{PostID: post.ID, UserID: userID, Value: value, CreatedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp()}).Error; err != nil {
				return err
			}
			changed = true
		} else if err != nil {
			return err
		} else if vote.Value != value {
			oldValue := vote.Value
			if err := tx.Model(&vote).Updates(map[string]any{"value": value, "updated_at": common.GetTimestamp()}).Error; err != nil {
				return err
			}
			changed = true
			if oldValue == SharePostVoteUsable {
				post.UsableVotes--
			} else {
				post.InvalidVotes--
			}
		}
		if changed {
			if value == SharePostVoteUsable {
				post.UsableVotes++
			} else {
				post.InvalidVotes++
			}
		}
		result := tx.Model(&SharePost{}).Where("id = ?", post.ID).Updates(map[string]any{
			"usable_votes": post.UsableVotes, "invalid_votes": post.InvalidVotes,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("share vote counter update affected no rows")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return GetSharePostByPublicID(publicID)
}
