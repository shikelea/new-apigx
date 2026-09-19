package apigxmodel

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// apigxTableNames 从 SQLite 里取出所有 apigx_ 前缀的表名。
// 用 substr 而不是 LIKE：LIKE 里 '_' 是通配符，会把 apigxX… 也匹配进来。
func apigxTableNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var names []string
	err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND substr(name, 1, 6) = ?", TablePrefix).Scan(&names).Error
	require.NoError(t, err)
	return names
}

func newSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 用文件库而不是内存库：内存库每条连接一份，无法验证「连续两次迁移」的幂等性。
	dsn := filepath.Join(t.TempDir(), "apigx_migrate_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	// Windows 上必须显式关闭底层连接，否则 TempDir 清理会因文件被占用而失败。
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// TestMigrateCreatesAllDomainTables 守住「设计里有多少张表，迁移就必须建多少张」。
// 漏加一张表会让本用例失败，而不是让问题在运行时才暴露。
func TestMigrateCreatesAllDomainTables(t *testing.T) {
	db := newSQLiteDB(t)

	require.NoError(t, Migrate(db))

	names := apigxTableNames(t, db)
	assert.Len(t, names, ExpectedTableCount, "建表数量与冻结设计不一致：%v", names)
	assert.Len(t, AllModels(), ExpectedTableCount, "AllModels 与 ExpectedTableCount 不一致")
}

// TestMigrateIsIdempotentOnSQLite 验证连续两次迁移不报错、且不改变表集合。
// 依据底座 AGENTS.md：schema/迁移改动必须验证幂等性（要求连续启动两次）。
func TestMigrateIsIdempotentOnSQLite(t *testing.T) {
	db := newSQLiteDB(t)

	require.NoError(t, Migrate(db))
	first := apigxTableNames(t, db)

	require.NoError(t, Migrate(db), "第二次迁移必须无错（幂等）")
	second := apigxTableNames(t, db)

	assert.ElementsMatch(t, first, second, "两次迁移后的表集合必须一致")
	assert.Len(t, second, ExpectedTableCount)
}

// TestMigrateAddsUsageCapabilityToExistingRows reproduces the real upgrade
// path: Stage C already has immutable usage rows, while Stage D adds their
// capability classification. Existing evidence must be preserved and safely
// classified as unknown instead of making startup fail on a NOT NULL column.
func TestMigrateAddsUsageCapabilityToExistingRows(t *testing.T) {
	db := newSQLiteDB(t)
	require.NoError(t, Migrate(db))
	require.NoError(t, db.Create(&UsageEvent{
		EventUID:   "legacy-usage-before-capability",
		Currency:   "USD",
		OccurredAt: 1_800_400_000,
		CreatedAt:  1_800_400_000,
	}).Error)
	require.NoError(t, db.Migrator().DropColumn(&UsageEvent{}, "Capability"))
	require.False(t, db.Migrator().HasColumn(&UsageEvent{}, "Capability"))

	require.NoError(t, Migrate(db))
	require.True(t, db.Migrator().HasColumn(&UsageEvent{}, "Capability"))
	var event UsageEvent
	require.NoError(t, db.Where("event_uid = ?", "legacy-usage-before-capability").First(&event).Error)
	assert.Equal(t, CapabilityUnknown, event.Capability)
}

// TestMigrateRejectsNilDB 保证误用（例如迁移前 DB 未初始化）会明确报错。
func TestMigrateRejectsNilDB(t *testing.T) {
	require.Error(t, Migrate(nil))
}

// TestModelFieldTypesArePortable 是本项目的「三库可移植」守门测试。
//
// schema-draft §3.2 把字段类型收敛到极小的集合（int64 / int / string / bool / gorm.DeletedAt），
// 正是为了让 SQLite / MySQL / PostgreSQL 生成等价列类型。一旦有人在模型里引入
// time.Time、float64、decimal、指针类型或自定义类型，本用例立刻失败——
// 这类字段在不同驱动上的 Scanner/Valuer 行为会分叉（schema-draft §3.2 已说明为何金额用 int64 micro 单位）。
func TestModelFieldTypesArePortable(t *testing.T) {
	deletedAtType := reflect.TypeOf(gorm.DeletedAt{})
	allowed := map[reflect.Kind]bool{
		reflect.Int64:  true, // ID / FK / BIGINT / MICRO / TS
		reflect.Int:    true, // INT / ENUM
		reflect.String: true, // STR / TEXT / HASH64 / CUR3
		reflect.Bool:   true, // BOOL
	}

	for _, m := range AllModels() {
		modelType := reflect.TypeOf(m).Elem()
		for i := 0; i < modelType.NumField(); i++ {
			field := modelType.Field(i)
			if field.Type == deletedAtType {
				continue // 软删除列是唯一例外，底座同样使用
			}
			assert.True(t, allowed[field.Type.Kind()],
				"%s.%s 的类型是 %s，不在可移植集合内（只允许 int64/int/string/bool/gorm.DeletedAt）",
				modelType.Name(), field.Name, field.Type)
		}
	}
}
