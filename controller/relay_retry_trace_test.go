package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddUsedChannelRecordsChannelAndOneBasedKeyAttempts(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 0)
	addUsedChannel(c, 32)

	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 5)
	addUsedChannel(c, 32)

	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
	addUsedChannel(c, 31)

	assert.Equal(t, []string{"32", "32", "31"}, c.GetStringSlice("use_channel"))
	raw, exists := c.Get(retryAttemptsContextKey)
	require.True(t, exists)
	attempts, ok := raw.([]relayRetryAttempt)
	require.True(t, ok)
	assert.Equal(t, []relayRetryAttempt{
		{ChannelID: 32, KeyNumber: 1},
		{ChannelID: 32, KeyNumber: 6},
		{ChannelID: 31},
	}, attempts)
}
