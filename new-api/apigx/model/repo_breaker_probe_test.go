package apigxmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreakerProbeStateMachinePersistsRecoveryAndAuthGate(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, Migrate(db))

	const (
		offeringID  = int64(501)
		baseChannel = int64(601)
		openedAt    = int64(1_700_000_000)
	)
	require.NoError(t, OpenBreakerProbe(db, offeringID, baseChannel, openedAt, 60))
	assert.False(t, HasDueBreakerProbes(db, openedAt+59))
	assert.True(t, HasDueBreakerProbes(db, openedAt+60))

	due, err := ListDueBreakerProbes(db, openedAt+60, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	state := due[0]
	assert.Equal(t, BreakerProbeStateOpen, state.State)
	assert.True(t, state.OwnsChannelDisable)

	ready, err := RecordBreakerProbeResult(db, state.ID, openedAt+60, true, false, "")
	require.NoError(t, err)
	assert.False(t, ready)
	ready, err = RecordBreakerProbeResult(db, state.ID, openedAt+75, true, false, "")
	require.NoError(t, err)
	assert.True(t, ready)

	var recovering BreakerProbeState
	require.NoError(t, db.First(&recovering, state.ID).Error)
	assert.Equal(t, BreakerProbeStateRecovering, recovering.State)
	assert.Equal(t, alignToMinute(openedAt+75)+60, recovering.RecoveryBaselineTs)
	mayRestore, err := BreakerChannelMayRestore(db, baseChannel)
	require.NoError(t, err)
	assert.True(t, mayRestore)
	require.NoError(t, FinalizeBreakerRecovery(db, state.ID, openedAt+76))

	baseline, err := BreakerRecoveryBaseline(db, offeringID)
	require.NoError(t, err)
	assert.Equal(t, recovering.RecoveryBaselineTs, baseline)

	// A later trip reopens the same durable row. An auth failure stops scheduled
	// probes until the owner changes the channel credential pool.
	require.NoError(t, OpenBreakerProbe(db, offeringID, baseChannel, openedAt+120, 60))
	ready, err = RecordBreakerProbeResult(db, state.ID, openedAt+180, false, true, "http_401")
	require.NoError(t, err)
	assert.False(t, ready)
	assert.False(t, HasDueBreakerProbes(db, openedAt+10_000))

	group := &ShareGroup{OwnerUserID: 1, Name: "probe", PublicID: "probe-group"}
	require.NoError(t, CreateShareGroup(db, group))
	channel := &Channel{OwnerUserID: 1, ShareGroupID: group.ID, Name: "probe", BaseChannelID: baseChannel}
	require.NoError(t, CreateChannel(db, channel))
	require.NoError(t, RearmAuthBlockedBreakerProbesForChannel(db, channel.ID, openedAt+181))
	assert.True(t, HasDueBreakerProbes(db, openedAt+181))
}

func TestBreakerProbeModalityRequiresConfirmedCatalogIdentity(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, Migrate(db))
	owner := int64(771)
	group := &ShareGroup{OwnerUserID: owner, Name: "probe-modality"}
	require.NoError(t, CreateShareGroup(db, group))
	channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "probe-modality", BaseChannelID: 77_101}
	require.NoError(t, CreateChannel(db, channel))
	canonical := &CanonicalModel{APIModelID: "opaque-image", ModalityType: ModalityImageGeneration, Status: 1}
	require.NoError(t, db.Create(canonical).Error)
	offering := &ModelOffering{
		ChannelID: channel.ID, CanonicalModelID: canonical.ID, UpstreamModelID: "vendor-opaque-id",
	}
	require.NoError(t, CreateModelOffering(db, owner, offering))

	modality, err := BreakerProbeModality(db, offering.ID)
	require.NoError(t, err)
	assert.Equal(t, ModalityImageGeneration, modality)
	supported, err := CanActivelyProbeOffering(db, offering.ID)
	require.NoError(t, err)
	assert.True(t, supported)

	require.NoError(t, db.Model(&ModelOffering{}).Where("id = ?", offering.ID).
		Update("canonical_model_id", 0).Error)
	modality, err = BreakerProbeModality(db, offering.ID)
	require.NoError(t, err)
	assert.Zero(t, modality)
	supported, err = CanActivelyProbeOffering(db, offering.ID)
	require.NoError(t, err)
	assert.False(t, supported)

	// This is the real UI workflow: only a submitted offering enters identity
	// review, then the administrator confirms its mapping.
	require.NoError(t, SubmitModelOffering(db, owner, offering.ID))
	var alias ModelAlias
	require.NoError(t, db.Where("normalized_model_id = ?", NormalizeModelIDForAlias(offering.UpstreamModelID)).
		First(&alias).Error)
	chatCanonical := &CanonicalModel{APIModelID: "vendor-chat", ModalityType: ModalityChat, Status: 1}
	require.NoError(t, db.Create(chatCanonical).Error)
	_, err = ConfirmAliasCandidate(db, alias.ID, chatCanonical.ID, owner)
	require.NoError(t, err)
	modality, err = BreakerProbeModality(db, offering.ID)
	require.NoError(t, err)
	assert.Equal(t, ModalityChat, modality)
	supported, err = CanActivelyProbeOffering(db, offering.ID)
	require.NoError(t, err)
	assert.True(t, supported)
}

func TestResolveQualityViewSinceExcludesPreRecoveryFailures(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, Migrate(db))
	const offeringID = int64(701)
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: 1_700_000_000, Outcome: QualityOutcomeUpstream,
	}))
	require.NoError(t, RecordQualitySample(db, QualitySample{
		OfferingID: offeringID, BucketTs: 1_700_000_120, Outcome: QualityOutcomeSuccess,
	}))

	view, err := ResolveQualityViewSince(db, offeringID, QualityWindowSixHour, 1_700_000_180, 1_700_000_060)
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.SampleCount)
	assert.EqualValues(t, 1, view.SuccessCount)
	assert.Zero(t, view.UpstreamFailures)
}
