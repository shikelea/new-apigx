// Package apigxmodel 承载 APIGX 新增的领域表（表名一律 apigx_ 前缀）。
//
// 包名用 apigxmodel 而不是 model，是为了与底座的 model 包区分，避免导入歧义：
//
//	import apigxmodel "github.com/QuantumNous/new-api/apigx/model"
//
// # 硬约束（违反即视为缺陷）
//
// 这些规则来自 `docs/stage0/schema-draft.md` §3 与底座 `AGENTS.md`，全部是**三库可移植**的要求：
//
//  1. 三库同时兼容：SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6。单库通过不算通过。
//  2. 绝不手写 `AUTO_INCREMENT` / `SERIAL`：主键交给 GORM 按方言生成。
//  3. 布尔字段**不写** `default:true`：MySQL 与 PostgreSQL 对布尔默认值的归一化不同，
//     `AutoMigrate` 会在每次启动反复下发 `ALTER TABLE`。
//  4. 时间列一律 UTC epoch 秒（int64），与底座 `common.GetTimestamp()` 一致（计划书 §8.5）。
//  5. 金额一律 int64 最小单位（micro-USD，1 unit = 1e-6 USD），不用 decimal：
//     避免各驱动 `Scanner`/`Valuer` 差异；换算走底座 `common/quota_math.go` 收口。
//  6. JSON 一律 TEXT 列 + `common.Marshal` / `common.Unmarshal`；**禁止**直接 `encoding/json`。
//     写入前必须做字节数上界校验（MySQL text 上限 65535 字节）。
//  7. 列名不得使用保留字（`group`、`key`、`order`、`user`、`desc`、`usage`）：
//     从命名上绕开，就没有方言引号问题。
//  8. 每一条索引都**显式命名**且长度 ≤ 40 字符（MySQL 标识符上限 64）。
//  9. 软删除用「`deleted_at` + `alive_flag` 墓碑列」：
//     存活行 `alive_flag = 0`，删除时置为本行 ID（唯一值），
//     「存活期间唯一」写成 `uniqueIndex(..., alive_flag)`。
//     不使用部分唯一索引（`WHERE deleted_at IS NULL`）——MySQL 不支持。
//  10. 快照列只存值，不加外键语义、不加级联；展示名 / 价格 / owner 变化不得回写历史。
//
// # 字段类型别名（schema-draft §3.2）
//
// Go 没有「带 tag 的类型别名」，因此别名只是**书写规约**，写字段时必须按下表选用 tag：
//
//	别名         Go 类型          GORM tag
//	ID          int64           `gorm:"primaryKey"`                     // 自增交给 GORM
//	FK          int64           `gorm:"bigint;not null;default:0"`      // 逻辑外键，不建库级约束
//	BIGINT      int64           `gorm:"bigint;not null;default:0"`
//	INT         int             `gorm:"type:int;not null;default:0"`
//	ENUM        int             `gorm:"type:int;not null"`              // 无 default，由 Go 侧常量赋值
//	BOOL        bool            `gorm:"not null"`                       // 无 default
//	MICRO       int64           `gorm:"bigint;not null;default:0"`      // micro-USD / micro 倍率
//	TS          int64           `gorm:"bigint;not null;default:0"`      // UTC epoch 秒，0=未设置
//	STR(n)      string          `gorm:"size:n;not null;default:''"`     // n ≤ 191
//	STRN(n)     string          `gorm:"size:n"`                         // 可空
//	TEXT        string          `gorm:"type:text"`
//	HASH64      string          `gorm:"type:char(64)"`                  // SHA-256 hex，不含 '-'
//	CUR3        string          `gorm:"type:char(3);not null"`          // ISO 4217
//	SOFTDEL     gorm.DeletedAt  `gorm:"index"`
package apigxmodel

// TablePrefix 是所有 APIGX 表的统一前缀，用于「新增文件集中、命名空间不相交」的约束。
const TablePrefix = "apigx_"

// StatusCapacitySaturated 是 APIGX 写入**底座多 Key 状态表**的标记值，表示"该 Key 容量已满"。
//
// 取值刻意落在底座 `common.ChannelStatus*`（1/2/3…）之外：
// 底座只要看到**非 Enabled** 就会跳过该 Key，而用一个属于自己的值，
// 我们的清理逻辑就能只回收自己的标记、不误删底座自己的禁用原因
// （例如鉴权失败自动禁用——那不该被我们顺手恢复）。
const StatusCapacitySaturated = 90

// 四态限制：计划书 §9.2 要求「未配置 / 禁用 / 继承默认 / 明确不限」分别存储，
// 空字段不得被解释成无限容量（CODE_STYLE.md 第 11 条）。
//
// 所有容量维度都以「模式列 + 值列」成对出现，校验落在 Go 侧（数据库层不写 CHECK，
// 因为三库对 CHECK 的支持与执行时机差异较大，且底座未使用 CHECK）。
const (
	// LimitModeUnset 未配置（未表态）；值列必须为 0，且不得解释为不限。
	LimitModeUnset int = 0
	// LimitModeDisabled 禁用：该资源不能用于此维度；值列必须为 0。
	LimitModeDisabled int = 1
	// LimitModeInherit 继承上级默认；值列必须为 0，解析时向上查找。
	LimitModeInherit int = 2
	// LimitModeUnlimited 明确不限；值列必须为 0。
	LimitModeUnlimited int = 3
	// LimitModeValue 明确值；值列必须 > 0。
	LimitModeValue int = 4
)

// AliveFlagAlive 是「存活」标识的取值（软删除墓碑约定，见包注释第 9 条）。
//
// 存活行取 0；被软删除时置为本行 ID（保证同一业务键下墓碑值互不相同），
// 这样 `uniqueIndex(业务键..., alive_flag)` 既能阻止存活行重复，又不会让墓碑互相冲突。
const AliveFlagAlive int64 = 0
