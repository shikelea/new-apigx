package apigxmodel

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newRelayToolsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "relay_tools.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, Migrate(db))
	return db
}

func TestListRelaySharesPublicOnlyExcludesDraftsAndSuspended(t *testing.T) {
	db := newRelayToolsTestDB(t)
	published := &RelayShare{OwnerUserID: 1, Title: "published", BaseURL: "https://published.example", Kind: RelayKindCharity}
	draft := &RelayShare{PublicID: "draft-relay", OwnerUserID: 1, Title: "draft", BaseURL: "https://draft.example", Kind: RelayKindCharity, Status: RelayStatusDraft, AliveFlag: AliveFlagAlive}
	suspended := &RelayShare{PublicID: "suspended-relay", OwnerUserID: 1, Title: "suspended", BaseURL: "https://suspended.example", Kind: RelayKindCommercial, Status: RelayStatusSuspended, AliveFlag: AliveFlagAlive}
	require.NoError(t, CreateRelayShare(db, published))
	require.NoError(t, db.Create(draft).Error)
	require.NoError(t, db.Create(suspended).Error)

	items, err := ListRelayShares(db, 0, false, 100)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "published", items[0].Title)
}

func TestCreateRelaySharePublishesOnlySupportedKinds(t *testing.T) {
	db := newRelayToolsTestDB(t)
	share := &RelayShare{OwnerUserID: 2, Title: "invalid", BaseURL: "https://invalid.example", Kind: "private"}
	require.Error(t, CreateRelayShare(db, share))
}

func TestNextCheckinRunAtUsesConfiguredLocalTime(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	current := time.Date(2026, 9, 26, 8, 30, 0, 0, loc).Unix()
	next, err := NextCheckinRunAt(current, "Asia/Shanghai", 9*60)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 26, 9, 0, 0, 0, loc).Unix(), next)

	current = time.Date(2026, 9, 26, 9, 1, 0, 0, loc).Unix()
	next, err = NextCheckinRunAt(current, "Asia/Shanghai", 9*60)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 27, 9, 0, 0, 0, loc).Unix(), next)
}
