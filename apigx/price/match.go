package price

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"gorm.io/gorm"
)

const (
	MatchStatusMatched                  = "matched"
	MatchStatusUnchanged                = "unchanged"
	MatchStatusNotFound                 = "not_found"
	MatchStatusManualOverride           = "manual_override"
	MatchStatusExpressionRequiresManual = "expression_requires_manual"

	MatchKindExact          = "exact"
	MatchKindCanonicalAlias = "canonical_alias"
)

var ErrPriceCandidateChanged = errors.New("price: candidate changed; preview again")

// MatchDimension is one immutable price item shown in the confirmation preview.
type MatchDimension struct {
	Dimension         int    `json:"dimension"`
	UnitScale         int    `json:"unit_scale"`
	UnitPriceMicro    int64  `json:"unit_price_micro"`
	Currency          string `json:"currency"`
	UsdUnitPriceMicro int64  `json:"usd_unit_price_micro"`
}

// MatchCandidate is derived entirely from New API's active local pricing. The
// client confirms CandidateToken and never sends prices back to the server.
type MatchCandidate struct {
	OfferingID        int64            `json:"offering_id"`
	UpstreamModelID   string           `json:"upstream_model_id"`
	NormalizedModelID string           `json:"normalized_model_id"`
	MatchedModelID    string           `json:"matched_model_id,omitempty"`
	MatchKind         string           `json:"match_kind,omitempty"`
	Status            string           `json:"status"`
	Reason            string           `json:"reason,omitempty"`
	Confirmable       bool             `json:"confirmable"`
	CandidateToken    string           `json:"candidate_token,omitempty"`
	Source            int              `json:"source,omitempty"`
	SourceRef         string           `json:"source_ref,omitempty"`
	PricingMode       int              `json:"pricing_mode,omitempty"`
	Dimensions        []MatchDimension `json:"dimensions,omitempty"`
	ExpressionHash    string           `json:"expression_hash,omitempty"`
	ExpressionVersion int              `json:"expression_version,omitempty"`
	Expression        string           `json:"expression,omitempty"`
	CurrentVersion    int64            `json:"current_version,omitempty"`
	CurrentSource     int              `json:"current_source,omitempty"`
	CurrentLocked     bool             `json:"current_locked,omitempty"`

	reading Reading
}

type MatchSelection struct {
	OfferingID     int64
	CandidateToken string
}

// PreviewChannelMatches reads New API's active local pricing for every
// offering in one owned channel. It never writes price rows.
func PreviewChannelMatches(db *gorm.DB, ownerUserID, channelID int64) ([]MatchCandidate, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db is required", apigxmodel.ErrInvalidArgument)
	}
	offerings, err := apigxmodel.ListModelOfferingsByChannel(db, ownerUserID, channelID)
	if err != nil {
		return nil, err
	}
	candidates := make([]MatchCandidate, 0, len(offerings))
	for i := range offerings {
		candidate, buildErr := buildMatchCandidate(db, &offerings[i])
		if buildErr != nil {
			return nil, buildErr
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// ConfirmChannelMatches re-reads every selected local candidate inside the
// write transaction, verifies its opaque token, and appends price versions.
func ConfirmChannelMatches(db *gorm.DB, ownerUserID, channelID int64, selections []MatchSelection) (SyncReport, error) {
	report := SyncReport{Notes: map[string]string{}}
	if db == nil || len(selections) == 0 {
		return report, fmt.Errorf("%w: at least one selection is required", apigxmodel.ErrInvalidArgument)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		preview, previewErr := PreviewChannelMatches(tx, ownerUserID, channelID)
		if previewErr != nil {
			return previewErr
		}
		byOffering := make(map[int64]MatchCandidate, len(preview))
		for i := range preview {
			byOffering[preview[i].OfferingID] = preview[i]
		}
		seen := make(map[int64]struct{}, len(selections))
		readings := make([]Reading, 0, len(selections))
		for _, selection := range selections {
			if selection.OfferingID <= 0 || strings.TrimSpace(selection.CandidateToken) == "" {
				return fmt.Errorf("%w: offering_id and candidate_token are required", apigxmodel.ErrInvalidArgument)
			}
			if _, duplicate := seen[selection.OfferingID]; duplicate {
				return fmt.Errorf("%w: duplicate offering selection", apigxmodel.ErrInvalidArgument)
			}
			seen[selection.OfferingID] = struct{}{}
			candidate, ok := byOffering[selection.OfferingID]
			if !ok || !candidate.Confirmable || candidate.CandidateToken != selection.CandidateToken {
				return ErrPriceCandidateChanged
			}
			readings = append(readings, candidate.reading)
		}
		var syncErr error
		report, syncErr = SyncChannelPrices(tx, ownerUserID, channelID, readings)
		return syncErr
	})
	return report, err
}

func buildMatchCandidate(db *gorm.DB, offering *apigxmodel.ModelOffering) (MatchCandidate, error) {
	modelID := strings.TrimSpace(offering.UpstreamModelID)
	candidate := MatchCandidate{
		OfferingID:        offering.ID,
		UpstreamModelID:   modelID,
		NormalizedModelID: apigxmodel.NormalizeModelIDForAlias(modelID),
		Status:            MatchStatusNotFound,
		Reason:            "no_local_price",
	}

	current, currentItems, currentErr := apigxmodel.GetEffectivePriceRule(db, apigxmodel.PriceScopeOffering, offering.ID, 0)
	if currentErr == nil {
		candidate.CurrentVersion = current.Version
		candidate.CurrentSource = current.Source
		candidate.CurrentLocked = current.Locked || current.ManualOverride
		if candidate.CurrentLocked {
			candidate.Status = MatchStatusManualOverride
			candidate.Reason = "manual_price_active"
			return candidate, nil
		}
	} else if !errors.Is(currentErr, apigxmodel.ErrNoPriceRule) {
		return MatchCandidate{}, currentErr
	}

	matchedModelID := modelID
	matchKind := MatchKindExact
	reading, expr, found, expression := readLocalCandidate(matchedModelID)
	if !found {
		canonical, resolved, resolveErr := apigxmodel.ResolveCanonicalModel(db, modelID)
		if resolveErr != nil {
			return MatchCandidate{}, resolveErr
		}
		if resolved && canonical.APIModelID != "" {
			matchedModelID = canonical.APIModelID
			matchKind = MatchKindCanonicalAlias
			reading, expr, found, expression = readLocalCandidate(matchedModelID)
		}
	}
	if !found {
		return candidate, nil
	}

	candidate.MatchedModelID = matchedModelID
	candidate.MatchKind = matchKind
	if expression {
		candidate.Status = MatchStatusExpressionRequiresManual
		candidate.Reason = "dynamic_expression_requires_manual"
		candidate.Source = apigxmodel.PriceRuleSourceBuiltinPreset
		candidate.SourceRef = SourceRefFor("new-api/billing_expr", matchedModelID)
		candidate.Expression = expr
		candidate.ExpressionHash = billingexpr.ExprHashString(expr)
		candidate.ExpressionVersion = billingexpr.ExprVersion(expr)
		return candidate, nil
	}

	reading.UpstreamModelID = modelID
	items, mapErr := readingItems(reading)
	if mapErr != nil {
		candidate.Reason = "invalid_local_price"
		return candidate, nil
	}
	if len(items) == 0 {
		return candidate, nil
	}
	candidate.Source = reading.Source
	candidate.SourceRef = reading.SourceRef
	candidate.PricingMode = apigxmodel.PriceModePerDimension
	if reading.PerRequestUSD > 0 {
		candidate.PricingMode = apigxmodel.PriceModePerRequest
	}
	for i := range items {
		candidate.Dimensions = append(candidate.Dimensions, MatchDimension{
			Dimension: items[i].Dimension, UnitScale: items[i].UnitScale,
			UnitPriceMicro: items[i].UnitPriceMicro, Currency: items[i].Currency,
			UsdUnitPriceMicro: items[i].UnitPriceMicro,
		})
	}
	candidate.Status = MatchStatusMatched
	candidate.Confirmable = true
	candidate.reading = reading
	candidate.CandidateToken = matchCandidateToken(candidate, reading)
	if currentErr == nil && samePriceItems(current, currentItems, reading, items) {
		candidate.Status = MatchStatusUnchanged
		candidate.Reason = "current_version_matches"
		candidate.Confirmable = false
	}
	return candidate, nil
}

func readLocalCandidate(modelID string) (reading Reading, expr string, found, expression bool) {
	if value, ok := ReadModelPriceFromBillingExpr(modelID); ok {
		return Reading{}, value, true, true
	}
	value, ok := ReadModelPriceFromBase(modelID)
	return value, "", ok, false
}

func readingItems(reading Reading) ([]apigxmodel.PriceRuleItem, error) {
	if reading.PerRequestUSD > 0 {
		return perRequestItems(reading.PerRequestUSD)
	}
	return ItemsFromUSDPerMillion(reading.InputUSDPerMillion, reading.OutputUSDPerMillion,
		reading.CacheReadUSDPerMillion, reading.CacheWriteUSDPerMillion)
}

func samePriceItems(rule *apigxmodel.PriceRule, current []apigxmodel.PriceRuleItem, reading Reading, proposed []apigxmodel.PriceRuleItem) bool {
	if rule == nil || rule.Source != reading.Source || rule.SourceRef != reading.SourceRef || len(current) != len(proposed) {
		return false
	}
	byDimension := make(map[int]apigxmodel.PriceRuleItem, len(current))
	for i := range current {
		byDimension[current[i].Dimension] = current[i]
	}
	for i := range proposed {
		existing, ok := byDimension[proposed[i].Dimension]
		if !ok || existing.UnitScale != proposed[i].UnitScale || existing.UnitPriceMicro != proposed[i].UnitPriceMicro || existing.Currency != proposed[i].Currency {
			return false
		}
	}
	return true
}

func matchCandidateToken(candidate MatchCandidate, reading Reading) string {
	parts := []string{
		strconv.FormatInt(candidate.OfferingID, 10), candidate.UpstreamModelID,
		candidate.MatchedModelID, candidate.MatchKind, candidate.SourceRef,
		candidate.ExpressionHash,
		strconv.FormatInt(candidate.CurrentVersion, 10), strconv.Itoa(candidate.CurrentSource),
		strconv.FormatBool(candidate.CurrentLocked),
		strconv.FormatFloat(reading.InputUSDPerMillion, 'g', -1, 64),
		strconv.FormatFloat(reading.OutputUSDPerMillion, 'g', -1, 64),
		strconv.FormatFloat(reading.CacheReadUSDPerMillion, 'g', -1, 64),
		strconv.FormatFloat(reading.CacheWriteUSDPerMillion, 'g', -1, 64),
		strconv.FormatFloat(reading.PerRequestUSD, 'g', -1, 64),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
