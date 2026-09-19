package service

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/apigx/channelpool"
	apigxmodel "github.com/QuantumNous/new-api/apigx/model"
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type failOnceFunding struct {
	settles int
}

func (*failOnceFunding) Source() string         { return BillingSourceUsageOnly }
func (*failOnceFunding) PreConsume(_ int) error { return nil }
func (f *failOnceFunding) Settle(_ int) error {
	f.settles++
	if f.settles == 1 {
		return errors.New("first settlement failed")
	}
	return nil
}
func (*failOnceFunding) Refund() error { return nil }

func TestBillingSessionRecordsQualityOnceAcrossSettlementRetry(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "billing-quality.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, apigxmodel.Migrate(db))

	group := &apigxmodel.ShareGroup{OwnerUserID: 1001, Name: "billing-quality", PublicID: "billing-quality-group"}
	require.NoError(t, apigxmodel.CreateShareGroup(db, group))
	channel := &apigxmodel.Channel{
		OwnerUserID: 1001, ShareGroupID: group.ID, PublicID: "billing-quality-channel",
		Name: "billing-quality", BaseChannelID: 7002,
	}
	require.NoError(t, apigxmodel.CreateChannel(db, channel))
	offering := &apigxmodel.ModelOffering{ChannelID: channel.ID, UpstreamModelID: "billing-quality-model"}
	require.NoError(t, apigxmodel.CreateModelOffering(db, 1001, offering))

	previousDB := channelpool.CurrentDB
	channelpool.SetDBProvider(func() *gorm.DB { return db })
	t.Cleanup(func() { channelpool.SetDBProvider(previousDB) })
	t.Setenv("APIGX_QUALITY_STATS_ENABLED", "true")

	funding := &failOnceFunding{}
	relayInfo := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 7002},
		OriginModelName: "billing-quality-model",
		StartTime:       time.Now().Add(-time.Second),
		IsPlayground:    true,
	}
	session := &BillingSession{relayInfo: relayInfo, funding: funding}
	require.Error(t, session.Settle(1))
	require.NoError(t, session.Settle(1))
	require.NoError(t, session.Settle(1))

	view, err := apigxmodel.ResolveQualityView(db, offering.ID, apigxmodel.QualityWindowSixHour, common.GetTimestamp())
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.SampleCount)
	assert.EqualValues(t, 1, view.SuccessCount)
	assert.Equal(t, 2, funding.settles)
}
