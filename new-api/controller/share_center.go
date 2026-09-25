package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func shareCenterLoginRequired() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[model.ShareCenterRequireLoginOption] == "true"
}

func shareCenterPage(c *gin.Context) (int, int) {
	page := common.GetPageQuery(c)
	return page.GetStartIdx(), page.GetPageSize()
}

func ListShareCenterPosts(c *gin.Context) {
	start, size := shareCenterPage(c)
	posts, total, err := model.ListPublishedSharePosts(c.GetInt("id"), shareCenterLoginRequired(), c.Query("keyword"), start, size)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page := common.GetPageQuery(c)
	page.SetTotal(int(total))
	page.SetItems(posts)
	common.ApiSuccess(c, page)
}

func GetShareCenterPost(c *gin.Context) {
	post, err := model.GetVisibleSharePost(c.Param("publicID"), c.GetInt("id"), shareCenterLoginRequired())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "share not found"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, post)
}

// Visibility remains accepted for old clients, but platform sharing is public by policy.
type sharePostInput struct {
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Content    string `json:"content"`
	Visibility string `json:"visibility"`
}

func CreateShareCenterPost(c *gin.Context) {
	var input sharePostInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	post := &model.SharePost{AuthorID: c.GetInt("id"), Title: input.Title, Summary: input.Summary, Content: input.Content, Visibility: model.SharePostVisibilityPublic}
	if err := model.CreateSharePost(post); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "share_center.post.create", map[string]any{"public_id": post.PublicID})
	common.ApiSuccess(c, post)
}

func UpdateShareCenterPost(c *gin.Context) {
	post, err := model.GetOwnedSharePost(c.Param("publicID"), c.GetInt("id"))
	if err != nil {
		shareCenterNotFound(c, err)
		return
	}
	var input sharePostInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	post.Title, post.Summary, post.Content = input.Title, input.Summary, input.Content
	if err := model.UpdateOwnedSharePost(post, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, post)
}

func SubmitShareCenterPost(c *gin.Context) {
	post, err := model.SubmitOwnedSharePost(c.Param("publicID"), c.GetInt("id"))
	if err != nil {
		shareCenterNotFound(c, err)
		return
	}
	recordManageAudit(c, "share_center.post.submit", map[string]any{"public_id": post.PublicID})
	common.ApiSuccess(c, post)
}

func ArchiveShareCenterPost(c *gin.Context) {
	if err := model.ArchiveOwnedSharePost(c.Param("publicID"), c.GetInt("id")); err != nil {
		shareCenterNotFound(c, err)
		return
	}
	recordManageAudit(c, "share_center.post.archive", map[string]any{"public_id": c.Param("publicID")})
	common.ApiSuccess(c, nil)
}

func ListMyShareCenterPosts(c *gin.Context) {
	start, size := shareCenterPage(c)
	posts, total, err := model.ListOwnedSharePosts(c.GetInt("id"), start, size)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page := common.GetPageQuery(c)
	page.SetTotal(int(total))
	page.SetItems(posts)
	common.ApiSuccess(c, page)
}

func VoteShareCenterPost(c *gin.Context) {
	var input struct {
		Value int `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	post, err := model.VoteSharePost(c.Param("publicID"), c.GetInt("id"), input.Value)
	if err != nil {
		shareCenterNotFound(c, err)
		return
	}
	common.ApiSuccess(c, post)
}

func ListShareCenterReviews(c *gin.Context) {
	start, size := shareCenterPage(c)
	posts, total, err := model.ListPendingSharePosts(start, size)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page := common.GetPageQuery(c)
	page.SetTotal(int(total))
	page.SetItems(posts)
	common.ApiSuccess(c, page)
}

func ApproveShareCenterPost(c *gin.Context) {
	post, err := reviewShareCenterPost(c, true)
	if err != nil {
		shareCenterNotFound(c, err)
		return
	}
	recordManageAudit(c, "share_center.post.approve", map[string]any{"public_id": post.PublicID, "author_id": post.AuthorID})
	common.ApiSuccess(c, post)
}

func RejectShareCenterPost(c *gin.Context) {
	post, err := reviewShareCenterPost(c, false)
	if err != nil {
		shareCenterNotFound(c, err)
		return
	}
	recordManageAudit(c, "share_center.post.reject", map[string]any{"public_id": post.PublicID, "author_id": post.AuthorID})
	common.ApiSuccess(c, post)
}

func reviewShareCenterPost(c *gin.Context, publish bool) (*model.SharePost, error) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil && !publish {
		return nil, err
	}
	return model.ReviewSharePost(c.Param("publicID"), publish, strings.TrimSpace(input.Reason))
}

func GetShareCenterConfig(c *gin.Context) {
	common.ApiSuccess(c, gin.H{"require_login": shareCenterLoginRequired()})
}

func UpdateShareCenterConfig(c *gin.Context) {
	var input struct {
		RequireLogin bool `json:"require_login"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption(model.ShareCenterRequireLoginOption, strconv.FormatBool(input.RequireLogin)); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "share_center.config.update", map[string]any{"require_login": input.RequireLogin})
	common.ApiSuccess(c, gin.H{"require_login": input.RequireLogin})
}

func shareCenterNotFound(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "share not found"})
		return
	}
	common.ApiError(c, err)
}
