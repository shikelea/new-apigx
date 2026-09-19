package apigxmodel

import "gorm.io/gorm"

// defaultDB 是迁移成功后捕获的数据库句柄。
//
// 与底座 `model.DB` 同样的做法：进程内只有一个主库句柄，由启动路径注入一次。
// 这样 APIGX 的接口层不需要再往上游 router/ 里加 `model` 包的 import，
// 上游接线只剩下「挂一组路由」这几行。
var defaultDB *gorm.DB

// DefaultDB 返回迁移时捕获的主库句柄；可能为 nil（尚未迁移），调用方必须判空。
func DefaultDB() *gorm.DB { return defaultDB }

// Migrate 创建或升级 APIGX 的全部领域表。
//
// 与底座 AutoMigrate 的关系（依据 `docs/stage0/schema-draft.md` §2.3）：
//
//   - **只增不改**：只创建/补充我们自己的 apigx_* 表，不触碰底座的任何表；
//   - **幂等**：可重复执行，连续启动两次不产生额外的结构差异；
//   - **三库同源**：不使用任何方言特性，SQLite / MySQL / PostgreSQL 走同一条代码路径；
//   - **不建库级外键**：表间关系由 `FK` 列在应用层维护（schema-draft §6.1），
//     因此没有创建顺序要求，也不需要级联。
//
// 调用点见 `docs/stage0/upstream-diff.md` 的 D-003（底座 model.migrateDB 之后）。
func Migrate(db *gorm.DB) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	// MySQL 服务端默认引擎可能是 MyISAM —— 本机 phpStudy 的 my.ini 就是 default-storage-engine=MyIsam。
	// 这里**显式要求 InnoDB**，因为计划书的核心保证都依赖事务与行锁：
	//   - 预留、额度原子扣减、幂等记录：MyISAM 没有事务，这些保证会**静默失效**；
	//   - MyISAM 索引上限 1000 字节（InnoDB DYNAMIC 为 3072），底座原生表会直接建不出来。
	// 只影响我们自己创建的 apigx_* 表，不改变底座表的行为（也不触碰底座的任何表）。
	migrateDB := db
	if db.Dialector != nil && db.Dialector.Name() == "mysql" {
		migrateDB = db.Set("gorm:table_options", "ENGINE=InnoDB")
	}
	if err := migrateDB.AutoMigrate(AllModels()...); err != nil {
		return err
	}
	// 迁移成功后才捕获句柄：确保接口层拿到的一定是可用的库
	defaultDB = db
	return nil
}

// AllModels 返回全部 APIGX 领域表模型。
//
// 顺序与契约要求无关（不建库级外键）；但**新增表必须同时加到这里**，
// 否则该表不会随启动创建。表数量由 `migrate_test.go` 守住。
func AllModels() []any {
	return []any{
		// §5.1–§5.13 身份、分享与授权（models_identity.go）
		&UserProfile{},
		&APIKey{},
		&ShareGroup{},
		&Channel{},
		&UpstreamAccount{},
		&UpstreamKey{},
		&UpstreamKeyCredential{},
		&KeyModelCapability{},
		&CanonicalModel{},
		&ModelAlias{},
		&ModelOffering{},
		&AccessPolicy{},
		&Grant{},
		&Invite{},
		// §5.14–§5.27 计价、计量、异步与运维（models_metering.go）
		&FxRate{},
		&PriceRule{},
		&PriceRuleItem{},
		&CapacityPolicy{},
		&Reservation{},
		&Request{},
		&Attempt{},
		&UsageEvent{},
		&QualityBucket{},
		&AsyncTask{},
		&UpstreamObjectRef{},
		&AuditEvent{},
		&IdempotencyRecord{},
		&PlatformSetting{},
	}
}

// TableNamePrefix 供迁移与验收脚本筛选 APIGX 自己的表（避免与底座表混淆）。
const TableNamePrefix = TablePrefix

// ExpectedTableCount 是当前冻结设计的表数量（原 27 张 + 凭据历史表）。
//
// 这是一个**契约常量**：新增表时必须同时调整它，并让 migrate_test.go 的断言继续成立；
// 这样「漏加迁移」会立刻变成测试失败，而不是静默少建一张表。
const ExpectedTableCount = 28
