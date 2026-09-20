package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func failoverTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

func TestRegisterMultiKeyFailureExcludesEveryFailedCredential(t *testing.T) {
	c := failoverTestContext()
	channel := &model.Channel{
		Id:  41,
		Key: "key-a\nkey-b\nkey-c",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:        true,
			MultiKeyMode:      constant.MultiKeyModeRandom,
			MultiKeyStableIDs: []string{"stable-a", "stable-b", "stable-c"},
		},
	}

	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 0)
	assert.Equal(t, MultiKeyFailoverRetrySameChannel, RegisterMultiKeyFailure(c, channel))
	assert.Contains(t, MultiKeyExclusionsForChannel(c, channel.Id).StableIDs, "stable-a")

	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 1)
	assert.Equal(t, MultiKeyFailoverRetrySameChannel, RegisterMultiKeyFailure(c, channel))
	assert.Contains(t, MultiKeyExclusionsForChannel(c, channel.Id).StableIDs, "stable-b")

	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 2)
	assert.Equal(t, MultiKeyFailoverChannelExhausted, RegisterMultiKeyFailure(c, channel))
	assert.Len(t, MultiKeyExclusionsForChannel(c, channel.Id).StableIDs, 3)
}

func TestRegisterMultiKeyFailureStopsAtHardAttemptLimit(t *testing.T) {
	c := failoverTestContext()
	keys := make([]string, 11)
	stableIDs := make([]string, 11)
	for index := range keys {
		keys[index] = string(rune('a' + index))
		stableIDs[index] = "stable-" + keys[index]
	}
	channel := &model.Channel{
		Id:  42,
		Key: keys[0] + "\n" + keys[1] + "\n" + keys[2] + "\n" + keys[3] + "\n" + keys[4] + "\n" + keys[5] + "\n" + keys[6] + "\n" + keys[7] + "\n" + keys[8] + "\n" + keys[9] + "\n" + keys[10],
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:        true,
			MultiKeyMode:      constant.MultiKeyModePolling,
			MultiKeyStableIDs: stableIDs,
		},
	}

	for index := 0; index < MaxMultiKeyAttemptsPerRequest-1; index++ {
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
		assert.Equal(t, MultiKeyFailoverRetrySameChannel, RegisterMultiKeyFailure(c, channel))
	}
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, MaxMultiKeyAttemptsPerRequest-1)
	assert.Equal(t, MultiKeyFailoverChannelExhausted, RegisterMultiKeyFailure(c, channel))
	assert.Len(t, MultiKeyExclusionsForChannel(c, channel.Id).StableIDs, MaxMultiKeyAttemptsPerRequest)
}

func TestRegisterMultiKeyFailureUsesSelectedStableIdentityAfterReorder(t *testing.T) {
	c := failoverTestContext()
	channel := &model.Channel{
		Id:  43,
		Key: "key-b\nkey-a",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:        true,
			MultiKeyMode:      constant.MultiKeyModePolling,
			MultiKeyStableIDs: []string{"stable-b", "stable-a"},
		},
	}

	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 0)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyStableID, "stable-a")
	assert.Equal(t, MultiKeyFailoverRetrySameChannel, RegisterMultiKeyFailure(c, channel))
	excluded := MultiKeyExclusionsForChannel(c, channel.Id)
	assert.Contains(t, excluded.StableIDs, "stable-a")
	assert.NotContains(t, excluded.StableIDs, "stable-b")
}
