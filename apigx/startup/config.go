// Package startup validates APIGX-specific runtime invariants before the
// gateway starts background workers or accepts traffic.
package startup

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const secretKeyBytes = 32

// Validate rejects feature combinations that would silently weaken the shared
// resource platform. Generic New API deployments remain compatible unless an
// APIGX feature or APIGX_SHARED_MODE is explicitly enabled.
func Validate(db *gorm.DB, redisEnabled bool) error {
	shared := common.GetEnvOrDefaultBool("APIGX_SHARED_MODE", false)
	usageOnly := common.GetEnvOrDefaultBool("APIGX_USAGE_ONLY", false)
	sourceGuard := common.GetEnvOrDefaultBool("APIGX_SOURCE_GUARD_ENABLED", true)
	capacity := common.GetEnvOrDefaultBool("APIGX_CAPACITY_ENABLED", false)
	keyStats := common.GetEnvOrDefaultBool("APIGX_KEY_STATS_ENABLED", false)
	qualityStats := common.GetEnvOrDefaultBool("APIGX_QUALITY_STATS_ENABLED", false)
	breaker := common.GetEnvOrDefaultBool("APIGX_BREAKER_ENABLED", false)
	routeStrategy := common.GetEnvOrDefaultBool("APIGX_ROUTE_STRATEGY_ENABLED", false)
	tpmAdjust := common.GetEnvOrDefaultBool("APIGX_TPM_ADJUST_ENABLED", false)

	var problems []error
	if capacity && !redisEnabled {
		problems = append(problems, errors.New("APIGX_CAPACITY_ENABLED requires an available REDIS_CONN_STRING"))
	}
	if routeStrategy && !qualityStats {
		problems = append(problems, errors.New("APIGX_ROUTE_STRATEGY_ENABLED requires APIGX_QUALITY_STATS_ENABLED=true"))
	}
	if breaker && !qualityStats {
		problems = append(problems, errors.New("APIGX_BREAKER_ENABLED requires APIGX_QUALITY_STATS_ENABLED=true"))
	}
	if tpmAdjust && !capacity {
		problems = append(problems, errors.New("APIGX_TPM_ADJUST_ENABLED requires APIGX_CAPACITY_ENABLED=true"))
	}

	if shared {
		required := map[string]bool{
			"APIGX_USAGE_ONLY":             usageOnly,
			"APIGX_SOURCE_GUARD_ENABLED":   sourceGuard,
			"APIGX_CAPACITY_ENABLED":       capacity,
			"APIGX_KEY_STATS_ENABLED":      keyStats,
			"APIGX_QUALITY_STATS_ENABLED":  qualityStats,
			"APIGX_BREAKER_ENABLED":        breaker,
			"APIGX_ROUTE_STRATEGY_ENABLED": routeStrategy,
			"APIGX_TPM_ADJUST_ENABLED":     tpmAdjust,
		}
		for name, enabled := range required {
			if !enabled {
				problems = append(problems, fmt.Errorf("APIGX_SHARED_MODE requires %s=true", name))
			}
		}
		if !redisEnabled {
			problems = append(problems, errors.New("APIGX_SHARED_MODE requires an available REDIS_CONN_STRING"))
		}
		if err := validateSecretKey(os.Getenv("APIGX_SECRET_KEY")); err != nil {
			problems = append(problems, err)
		}
		if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
			if err := validateMySQLStorageEngine(db); err != nil {
				problems = append(problems, err)
			}
		}
	}

	return errors.Join(problems...)
}

func validateSecretKey(raw string) error {
	value := strings.TrimSpace(raw)
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != secretKeyBytes {
		return fmt.Errorf("APIGX_SHARED_MODE requires APIGX_SECRET_KEY as exactly %d bytes encoded by 64 hexadecimal characters", secretKeyBytes)
	}
	return nil
}

func validateMySQLStorageEngine(db *gorm.DB) error {
	if db == nil {
		return errors.New("APIGX_SHARED_MODE cannot verify MySQL default_storage_engine: database handle is nil")
	}
	var engine string
	if err := db.Raw("SELECT @@default_storage_engine").Scan(&engine).Error; err != nil {
		return fmt.Errorf("APIGX_SHARED_MODE cannot verify MySQL default_storage_engine: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(engine), "InnoDB") {
		return fmt.Errorf("APIGX_SHARED_MODE requires MySQL default_storage_engine=InnoDB, got %q", engine)
	}
	return nil
}
