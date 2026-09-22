package apigxmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlazaKeepsOneSourcePerChannelAndCanonicalModel(t *testing.T) {
	db := newRepoDB(t)
	const owner int64 = 8401
	group := &ShareGroup{
		OwnerUserID: owner, Name: "plaza-dedupe",
		DefaultVisibility: ShareGroupVisibilityPublic, PublishStatus: ShareGroupPublishPublished,
	}
	require.NoError(t, CreateShareGroup(db, group))
	channel := &Channel{OwnerUserID: owner, ShareGroupID: group.ID, Name: "plaza-dedupe", BaseChannelID: 84_001}
	require.NoError(t, CreateChannel(db, channel))
	canonical := &CanonicalModel{APIModelID: "standard-dedupe", ModalityType: ModalityChat, Status: 1}
	require.NoError(t, db.Create(canonical).Error)

	first := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: "vendor-a"}
	second := &ModelOffering{ChannelID: channel.ID, UpstreamModelID: "vendor-b"}
	require.NoError(t, CreateModelOffering(db, owner, first))
	require.NoError(t, CreateModelOffering(db, owner, second))
	require.NoError(t, db.Model(&ModelOffering{}).Where("id IN ?", []int64{first.ID, second.ID}).
		Updates(map[string]any{"canonical_model_id": canonical.ID, "status": OfferingStatusPublished}).Error)

	items, total, err := ListPlazaItems(db, owner, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, first.ID, items[0].OfferingID)
	assert.Equal(t, canonical.APIModelID, items[0].CanonicalAPIModelID)

	require.NoError(t, DeleteModelOffering(db, owner, first.ID))
	items, total, err = ListPlazaItems(db, owner, 20, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, second.ID, items[0].OfferingID)
}
