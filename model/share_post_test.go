package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharePostValidationAndVisibilityDefaults(t *testing.T) {
	post := &SharePost{AuthorID: 7, Title: "  免费渠道  ", Content: "# hello", Visibility: ""}
	require.NoError(t, post.Validate())
	assert.Equal(t, "免费渠道", post.Title)
	assert.Equal(t, SharePostVisibilityPublic, post.Visibility)
	assert.EqualError(t, (&SharePost{AuthorID: 7, Title: "", Content: "body"}).Validate(), "share title must contain 1-160 characters")
	assert.Error(t, (&SharePost{AuthorID: 7, Title: "title", Content: "body", Visibility: "unknown"}).Validate())
}

func TestSharePostVoteCountersAreIdempotent(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&SharePost{}, &SharePostVote{}))
	DB.Exec("DELETE FROM share_post_votes")
	DB.Exec("DELETE FROM share_posts")
	post := &SharePost{AuthorID: 1, Title: "渠道", Content: "内容", Visibility: SharePostVisibilityPublic}
	require.NoError(t, CreateSharePost(post))
	_, err := SubmitOwnedSharePost(post.PublicID, post.AuthorID)
	require.NoError(t, err)
	_, err = ReviewSharePost(post.PublicID, true, "")
	require.NoError(t, err)
	_, err = VoteSharePost(post.PublicID, 2, SharePostVoteUsable)
	require.NoError(t, err)
	got, err := VoteSharePost(post.PublicID, 2, SharePostVoteUsable)
	require.NoError(t, err)
	assert.Equal(t, 1, got.UsableVotes)
	got, err = VoteSharePost(post.PublicID, 2, SharePostVoteInvalid)
	require.NoError(t, err)

	assert.Equal(t, 1, got.InvalidVotes)
	assert.Equal(t, 0, got.UsableVotes)
}
