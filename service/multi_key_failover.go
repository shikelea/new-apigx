package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const MaxMultiKeyAttemptsPerRequest = 10

const multiKeyAttemptsContextKey = "apigx_multi_key_attempts"

type MultiKeyFailoverDecision int

const (
	MultiKeyFailoverNotApplicable MultiKeyFailoverDecision = iota
	MultiKeyFailoverRetrySameChannel
	MultiKeyFailoverChannelExhausted
)

type attemptedChannelKeys struct {
	stableIDs map[string]struct{}
	indexes   map[int]struct{}
}

type requestMultiKeyAttempts struct {
	channels map[int]*attemptedChannelKeys
}

func multiKeyAttempts(c *gin.Context) *requestMultiKeyAttempts {
	if c == nil {
		return &requestMultiKeyAttempts{channels: make(map[int]*attemptedChannelKeys)}
	}
	if existing, ok := c.Get(multiKeyAttemptsContextKey); ok {
		if attempts, valid := existing.(*requestMultiKeyAttempts); valid && attempts != nil {
			return attempts
		}
	}
	attempts := &requestMultiKeyAttempts{channels: make(map[int]*attemptedChannelKeys)}
	c.Set(multiKeyAttemptsContextKey, attempts)
	return attempts
}

func attemptedKeysForChannel(c *gin.Context, channelID int) *attemptedChannelKeys {
	attempts := multiKeyAttempts(c)
	keys := attempts.channels[channelID]
	if keys == nil {
		keys = &attemptedChannelKeys{
			stableIDs: make(map[string]struct{}),
			indexes:   make(map[int]struct{}),
		}
		attempts.channels[channelID] = keys
	}
	return keys
}

// MultiKeyExclusionsForChannel returns a defensive copy because selection may
// outlive the caller's immediate lookup while the request records more failures.
func MultiKeyExclusionsForChannel(c *gin.Context, channelID int) model.MultiKeyExclusions {
	if c == nil || channelID <= 0 {
		return model.MultiKeyExclusions{}
	}
	attempts := multiKeyAttempts(c).channels[channelID]
	if attempts == nil {
		return model.MultiKeyExclusions{}
	}
	excluded := model.MultiKeyExclusions{
		StableIDs: make(map[string]struct{}, len(attempts.stableIDs)),
		Indexes:   make(map[int]struct{}, len(attempts.indexes)),
	}
	for stableID := range attempts.stableIDs {
		excluded.StableIDs[stableID] = struct{}{}
	}
	for index := range attempts.indexes {
		excluded.Indexes[index] = struct{}{}
	}
	return excluded
}

// RegisterMultiKeyFailure records the credential used by the failed upstream
// attempt and decides whether this request may try another key in the same
// channel. It never changes persistent key health; the base channel error path
// remains responsible for auto-disable and APIGX statistics.
func RegisterMultiKeyFailure(c *gin.Context, channel *model.Channel) MultiKeyFailoverDecision {
	if c == nil || channel == nil || channel.Id <= 0 || !channel.ChannelInfo.IsMultiKey {
		return MultiKeyFailoverNotApplicable
	}
	index := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	if index < 0 || index >= len(channel.GetKeys()) {
		return MultiKeyFailoverNotApplicable
	}
	attempted := attemptedKeysForChannel(c, channel.Id)
	stableID := common.GetContextKeyString(c, constant.ContextKeyChannelMultiKeyStableID)
	if stableID != "" {
		attempted.stableIDs[stableID] = struct{}{}
	} else if stableID, ok := channel.ChannelInfo.MultiKeyStableIDAt(index); ok {
		attempted.stableIDs[stableID] = struct{}{}
	} else {
		attempted.indexes[index] = struct{}{}
	}
	if len(attempted.stableIDs)+len(attempted.indexes) >= MaxMultiKeyAttemptsPerRequest {
		return MultiKeyFailoverChannelExhausted
	}
	if channel.HasEnabledKeyExcluding(MultiKeyExclusionsForChannel(c, channel.Id)) {
		return MultiKeyFailoverRetrySameChannel
	}
	return MultiKeyFailoverChannelExhausted
}
