package apigxmodel

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 三库兼容矩阵（底座 AGENTS.md 硬要求：SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6
// 必须**各自在真实实例上**验证；单库通过不算通过）。
//
// 由环境变量驱动，未配置的方言会**明确跳过并打印原因**，不会静默略过：
//
//	APIGX_TEST_PG_DSN    postgres://user:pass@host:port/db?sslmode=disable
//	APIGX_TEST_MYSQL_DSN user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=True&loc=UTC
//
// 每种方言都做两件事：
//  1. 在**全新库**上迁移，并断言建出 ExpectedTableCount 张 apigx_ 表；
//  2. **连续迁移两次**，断言第二次无错且表集合不变（幂等）。
//
// 「从底座旧库升级」的场景由外部脚本驱动：同一个测试直接指向底座已迁移过的库即可
// （此时 apigx_ 表是新增的，底座表原样保留）。
func TestMigrateMatrixOnConfiguredDialects(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		db := newSQLiteDB(t)
		require.NoError(t, Migrate(db))
		assert.Len(t, apigxTableNames(t, db), ExpectedTableCount)
		require.NoError(t, Migrate(db), "第二次迁移必须幂等")
		assert.Len(t, apigxTableNames(t, db), ExpectedTableCount)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("APIGX_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("未设置 APIGX_TEST_PG_DSN：PostgreSQL 矩阵未执行（不允许当作通过）")
		}
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err, "连接 PostgreSQL 失败")
		assertPostgresMatrix(t, db)
	})

	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("APIGX_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("未设置 APIGX_TEST_MYSQL_DSN：MySQL 矩阵未执行（不允许当作通过）")
		}
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
		require.NoError(t, err, "连接 MySQL 失败")
		require.NoError(t, Migrate(db))
		require.NoError(t, Migrate(db), "第二次迁移必须幂等")
		// MySQL 与 PostgreSQL 的表清单都用 information_schema 查
		assert.Len(t, informationSchemaApigxTables(t, db, "table_name"), ExpectedTableCount)

		// 存储引擎必须是 InnoDB。这不是洁癖：
		//  - 预留、额度原子扣减、幂等记录都依赖**事务与行锁**，MyISAM 没有事务，这些保证会静默失效；
		//  - MyISAM 的索引长度上限是 1000 字节（InnoDB DYNAMIC 是 3072），底座原生表
		//    （如 passkey_credentials.credential_id varchar(512) 上的唯一索引）会直接建不出来。
		// 本机首次接入时就踩到了：phpStudy 的 my.ini 里 default-storage-engine=MyIsam，
		// 结果 27 张表全部建成 MyISAM、底座自身 FATAL 起不来。此断言用于防止再次静默通过。
		var engine string
		row := db.Raw("SELECT engine FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
			TablePrefix+"user_profiles").Row()
		require.NoError(t, row.Scan(&engine), "读取 MySQL 存储引擎失败")
		assert.Equal(t, "InnoDB", engine,
			"APIGX 的表必须使用 InnoDB：MyISAM 无事务（预留/原子扣减会失效）且索引上限仅 1000 字节")
	})
}

func assertPostgresMatrix(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, Migrate(db))
	assert.Len(t, informationSchemaApigxTables(t, db, "table_name"), ExpectedTableCount)
	require.NoError(t, Migrate(db), "第二次迁移必须幂等")
	assert.Len(t, informationSchemaApigxTables(t, db, "table_name"), ExpectedTableCount)
}

// informationSchemaApigxTables 通过 information_schema 统计 apigx_ 表，
// 同时适用于 PostgreSQL 与 MySQL（SQLite 用 sqlite_master，见 apigxTableNames）。
func informationSchemaApigxTables(t *testing.T, db *gorm.DB, column string) []string {
	t.Helper()
	// 按方言选择「当前库/schema」表达式：PostgreSQL 是 current_schema()，MySQL 是 DATABASE()。
	// 不用「先试一个再回退」的写法——那会在日志里留下一条假的错误，掩盖真正的问题。
	schemaExpr := "current_schema()"
	if db.Dialector.Name() == "mysql" {
		schemaExpr = "DATABASE()"
	}
	var names []string
	query := "SELECT " + column + " FROM information_schema.tables WHERE table_schema = " + schemaExpr +
		" AND substr(" + column + ", 1, 6) = ?"
	require.NoError(t, db.Raw(query, TablePrefix).Scan(&names).Error)
	return names
}

// 说明：sqlite 驱动在此文件被引用是为了保证 matrix 测试在只跑本文件时也能编译；
// 真正的 SQLite 用例在 migrate_test.go。
var _ = sqlite.Open
