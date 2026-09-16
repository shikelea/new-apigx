package apigxmodel

import "gorm.io/gorm"

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
	return db.AutoMigrate(AllModels()...)
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

// ExpectedTableCount 是当前冻结设计的表数量（schema-draft §5.1–§5.27，共 27 张）。
//
// 这是一个**契约常量**：新增表时必须同时调整它，并让 migrate_test.go 的断言继续成立；
// 这样「漏加迁移」会立刻变成测试失败，而不是静默少建一张表。
const ExpectedTableCount = 27
