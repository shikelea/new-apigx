package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	"github.com/QuantumNous/new-api/apigx/relayhook"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDistributeVideoRetrieveSkipsNewSourceSelection(t *testing.T) {
	previousDB := channelpool.CurrentDB()
	previousGuard := relayhook.IsSourceGuardEnabled()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	channelpool.SetDBProvider(func() *gorm.DB { return database })
	relayhook.SetSourceGuardEnabled(true)
	t.Setenv("APIGX_SHARED_MODE", "true")
	t.Cleanup(func() {
		channelpool.SetDBProvider(func() *gorm.DB { return previousDB })
		relayhook.SetSourceGuardEnabled(previousGuard)
	})

	reached := false
	router := gin.New()
	router.GET(
		"/v1/videos/:video_id",
		func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUserId, 7)
			common.SetContextKey(c, constant.ContextKeyTokenId, 11)
			c.Next()
		},
		Distribute(),
		func(c *gin.Context) {
			reached = true
			c.Status(http.StatusNoContent)
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task_public", nil)
	router.ServeHTTP(recorder, request)

	assert.True(t, reached)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}
