package startup

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var apigxConfigKeys = []string{
	"APIGX_SHARED_MODE", "APIGX_USAGE_ONLY", "APIGX_SOURCE_GUARD_ENABLED",
	"APIGX_CAPACITY_ENABLED", "APIGX_KEY_STATS_ENABLED", "APIGX_QUALITY_STATS_ENABLED",
	"APIGX_BREAKER_ENABLED", "APIGX_ROUTE_STRATEGY_ENABLED", "APIGX_TPM_ADJUST_ENABLED",
	"APIGX_SECRET_KEY",
}

func resetConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range apigxConfigKeys {
		t.Setenv(key, "")
	}
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() { common.SetDatabaseTypes(previousMain, previousLog) })
}

func enableSharedProfile(t *testing.T) {
	t.Helper()
	for _, key := range apigxConfigKeys[:9] {
		t.Setenv(key, "true")
	}
	t.Setenv("APIGX_SECRET_KEY", strings.Repeat("ab", secretKeyBytes))
}

func TestValidateKeepsGenericNewAPIDefaultsCompatible(t *testing.T) {
	resetConfigEnv(t)
	require.NoError(t, Validate(nil, false))
}

func TestValidateAcceptsCompleteSharedProfile(t *testing.T) {
	resetConfigEnv(t)
	enableSharedProfile(t)
	require.NoError(t, Validate(nil, true))
}

func TestValidateRejectsIncompleteSharedProfile(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("APIGX_SHARED_MODE", "true")
	err := Validate(nil, false)
	require.Error(t, err)
	message := err.Error()
	assert.Contains(t, message, "APIGX_USAGE_ONLY=true")
	assert.Contains(t, message, "REDIS_CONN_STRING")
	assert.Contains(t, message, "APIGX_SECRET_KEY")
}

func TestValidateRejectsFeatureDependencyContradictions(t *testing.T) {
	resetConfigEnv(t)
	t.Setenv("APIGX_CAPACITY_ENABLED", "true")
	t.Setenv("APIGX_ROUTE_STRATEGY_ENABLED", "true")
	t.Setenv("APIGX_BREAKER_ENABLED", "true")
	t.Setenv("APIGX_TPM_ADJUST_ENABLED", "true")
	err := Validate(nil, false)
	require.Error(t, err)
	message := err.Error()
	assert.Contains(t, message, "REDIS_CONN_STRING")
	assert.Contains(t, message, "APIGX_QUALITY_STATS_ENABLED=true")
}
