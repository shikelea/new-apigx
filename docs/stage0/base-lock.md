# 底座锁定与许可义务

阶段 0 · T0.2 产出。对象：New API（`https://github.com/QuantumNous/new-api`）。
本文件是「用哪个版本、怎么复现、依赖有哪些、法律义务是什么、我们的改动怎么追踪」的权威记录。

## 1. 锁定记录

| 项 | 值 |
|---|---|
| 上游仓库 | `https://github.com/QuantumNous/new-api` |
| 固定提交 | `69a50029819a26c53e6babd276d49cfe2f8880ad`（短号 `69a5002`） |
| 提交标题 | `feat(channel): per-route pass-through for advanced custom channels (#7386)` |
| 提交时间 | `2026-09-15T14:00:05+08:00` |
| 本地路径 | `new-api/`（被 `.gitignore` 排除，不入库） |
| 工作区状态 | detached HEAD，未做任何本地改动（差异基线为 0） |
| 受跟踪文件 | 2468 |
| 代码体量 | Go：1004 文件 / 约 20.8 万行；TS+TSX：1289 文件 / 约 24.0 万行 |
| 许可证 | AGPLv3（`LICENSE` 544 行全文）；另有 `NOTICE`、`THIRD-PARTY-LICENSES.md` |
| 机器可读记录 | `deploy/base.lock.json` |

计划书 §5.1 要求「阶段 0 固定开发提交，部署前锁定经过验证的稳定版本及镜像摘要」。上表是开发期固定提交；**部署版本的锁定（含镜像摘要）留到阶段 4**，不要把移动的 `main` 当生产版本。

## 2. 完整性指纹（SHA256，均为固定提交处的原始文件）

| 文件 | 字节 | SHA256 |
|---|---|---|
| `go.mod` | 7579 | `6015aeb99b863f2ec2285954dc89b92140077017dfa33563cde2e027a9aee051` |
| `go.sum` | 314796 | `2420a4a300026a1dde1d962f23ed34fa7c22cf1c34a4a596106a37c8ac439055` |
| `web/package.json` | 4080 | `f0de4e55f527141134b02bd0c056e601e4d5f7e1653eb46caa1e157c78b81251` |
| `web/bun.lock` | 349515 | `a2fadebda73a3f757c989b1d1fc2e95b18c800ebeb58bf8be3933a4966857cbd` |
| `LICENSE` | 35184 | `6f1e622c82a380075843bb084a7ec3b1f1d12a4a02526d75e78b0924a860aa75` |
| `NOTICE` | 2084 | `903b9ca441be6912459551f39b70338ee7a410eba6cce097043cf006622bd6bc` |
| `THIRD-PARTY-LICENSES.md` | 24893 | `8c819d10b444f9510cd5cd8633a9f119150b40dbbb0f2102b8ce03a22d58cf1e` |

注意：`VERSION` 文件在固定提交处是 **空文件**（0 字节）。判断版本一律以 commit 为准，不要依赖 `VERSION`。

## 3. 复现步骤

本机沙箱内 HTTPS 的 Schannel 不可用（见 `docs/dev-environment.md`），因此 Git 必须走本地代理：

```powershell
cd D:\Github\apigx
git init new-api -b apigx-base
git -C new-api config http.https://github.com/.proxy http://127.0.0.1:7897
git -C new-api remote add origin https://github.com/QuantumNous/new-api
git -C new-api fetch --depth 1 origin 69a50029819a26c53e6babd276d49cfe2f8880ad
git -C new-api checkout --detach FETCH_HEAD
git -C new-api rev-parse HEAD   # 应输出完整 SHA
```

依赖获取：

```powershell
. .\tools\dev\env.ps1           # 挂上仓库内 Go 工具链，GOPROXY=goproxy.cn
cd new-api ; go mod download ; cd ..     # 后端依赖
cd new-api\web ; bun install ; cd ..\..  # 前端依赖（Bun 本机尚未安装，见 §4.3）
```

## 4. 依赖清单

权威清单是固定提交处的 `go.mod` / `go.sum` / `web/bun.lock`（指纹见 §2）。下面是**按用途归类**的要点，便于判断哪些能复用、哪些与我们的目标冲突。

### 4.1 后端（Go）

语言版本：`go 1.25.1`（本机工具链 1.27.1 编译，语言版本指令更高不匹配风险低，但升级前应核对）。

| 用途 | 关键依赖 |
|---|---|
| Web 框架 | `gin-gonic/gin v1.9.1`、`gin-contrib/{cors,gzip,static,sse}` |
| ORM 与数据库 | `gorm.io/gorm v1.25.12`、`gorm.io/driver/{postgres,mysql,clickhouse}`、`jackc/pgx/v5 v5.9.2`、`go-sql-driver/mysql`、`glebarez/sqlite`（纯 Go，无 CGO） |
| 缓存 | `go-redis/redis/v8 v8.11.5`（注意是 **v8**，不是 v9）、`alicebob/miniredis/v2`（测试用） |
| 认证与授权 | `casbin/casbin/v2`、`coreos/go-oidc/v3`、`golang-jwt/jwt/v5`、`go-webauthn/webauthn`、`pquerna/otp`（TOTP）、`Azure/go-ntlmssp` |
| 金额与计费 | `shopspring/decimal v1.4.0`、`expr-lang/expr v1.17.8`（计费表达式）、`tiktoken-go/tokenizer`（token 计数） |
| 厂商 SDK | `aws/aws-sdk-go-v2` + `service/bedrockruntime`、`openai/openai-go v1.12.0`、`waffo-com/waffo-go`、`waffo-com/waffo-pancake-sdk-go` |
| 支付（本项目不用） | `stripe/stripe-go/v81`、`Calcium-Ion/go-epay` —— 计划书明确不做充值/交易，属**可裁剪候选**，但裁剪前须确认底座计费链路无隐式依赖 |
| 媒体处理 | `abema/go-mp4`、`yapingcat/gomedia`、`go-audio/{aiff,wav,audio,riff}`、`mewkiz/flac`、`jfreymuth/oggvorbis`、`tcolgate/mp3`、`golang.org/x/image` |
| 插件运行时 | `grafana/sobek`（JS 引擎，`plugins/tasks/` 用） |
| 可观测性 | `grafana/pyroscope-go`、`prometheus/*`（间接） |
| 工具库 | `samber/lo`、`samber/hot`、`tidwall/{gjson,sjson}`、`google/uuid`、`pkg/errors`、`jinzhu/copier`、`thanhpk/randstr`、`shirou/gopsutil` |
| 测试 | `stretchr/testify v1.11.1` |

模块装配：`replace github.com/QuantumNous/new-api/relaykit => ./relaykit`，即 `relaykit/` 是**独立 Go 模块**，必须能单独 `GOWORK=off go build ./...`。

### 4.2 前端（`web/`）

包管理器是 **Bun**（`bun.lock`），不是 npm/pnpm。构建用 Rsbuild 2，类型检查用 `tsgo`（`@typescript/native-preview`），lint/format 用 oxlint/oxfmt，测试用 vitest 4。

| 用途 | 关键依赖 |
|---|---|
| 框架 | `react 19.2`、`react-dom 19.2`、`type: module` |
| 路由/数据/状态 | `@tanstack/react-router`、`@tanstack/react-query`、`@tanstack/react-table`、`@tanstack/react-virtual`、`zustand 5` |
| UI 与样式 | `@base-ui/react`、`@hugeicons/react`、`tailwindcss 4`、`class-variance-authority`、`clsx`、`tailwind-merge`、`motion`、`sonner`、`vaul`、`cmdk` |
| 表单与校验 | `react-hook-form`、`@hookform/resolvers`、`zod 4` |
| i18n | `i18next 26`、`react-i18next`、`i18next-browser-languagedetector` |
| 图表/富文本 | `@visactor/react-vchart`、`recharts`、`shiki`、`marked`、`katex`、`@codemirror/*` |
| 安全相关 | `dompurify 3.4.13`（被 `overrides` 锁定）、`node-forge` |
| `overrides` 安全锁 | `brace-expansion 2.1.1`、`dompurify 3.4.13`、`fast-uri 3.1.2`、`hono 4.12.22`、`ip-address 10.2.0`、`js-cookie 3.0.7`、`mermaid 11.15.0`、`minimist 1.2.8`、`postcss 8.5.15`、`qs 6.15.2`、`uuid 14.0.0` |

### 4.3 本机工具链缺口（前端）

`bun`、`tsgo`、`oxlint` **本机均未安装**。后端（Go 1.x + goproxy.cn）已可用；前端要动到 `web/` 之前必须先补 Bun，否则无法 typecheck/lint/build，也无法按底座规范提交前端改动。已登记为不稳定项。

## 5. 许可与署名义务（AGPLv3 及 §7 附加条款）

底座 `NOTICE` 在 AGPLv3 之外给出了**额外法律要求**，必须逐条落实：

| # | 义务 | 落实方式 | 状态 |
|---|---|---|---|
| 1 | 保留 AGPLv3 全文与版权声明 | 保留 `new-api/LICENSE`，不改动；修改版对外提供时附带完整许可 | ✅ 保留（尚未分发） |
| 2 | 保留 `NOTICE` 文件 | 不改动、不删除；Docker 镜像、独立二进制、前端产物、Electron 安装包分发时**必须随附** | ✅ 保留（尚未分发） |
| 3 | 保留署名文案（AGPLv3 §7(b)） | 在界面的 about / legal / footer / 署名位置保留：`Frontend design and development by New API contributors.` | ⬜ 阶段 1 前端改版时落实 |
| 4 | 保留指向原项目的可见链接 | 在显著位置保留 `https://github.com/QuantumNous/new-api` 链接 | ⬜ 同上 |
| 5 | 标注修改（AGPLv3 §7(c)） | 修改版必须标注改动、不得歪曲来源；由 `docs/stage0/upstream-diff.md` 台账 + 本文件承担 | ⬜ 机制已建立（§8） |
| 6 | 通过网络提供服务时提供源码获取方式 | 上线前在 about/legal 提供修改版对应源码的获取入口 | ⬜ 阶段 4 |
| 7 | 第三方许可与 Apache-2.0 NOTICE | 保留 `THIRD-PARTY-LICENSES.md`；`NOTICE` 中 AWS SDK / smithy-go / otp 的 Apache-2.0 声明必须保留 | ✅ 保留 |
| 8 | Electron / Chromium 声明 | 若分发桌面版，需随附 Electron 与 Chromium 的第三方许可声明 | ⬜ 仅在我们真做桌面版时才需要 |

**受保护标识（底座项目治理条款，硬约束）**：不得修改、删除或替换任何与 `New API` / `QuantumNous` 有关的引用、品牌、元数据与署名，包括 README、许可头、版权声明、包元数据、HTML title、meta 标签、footer、about 页、Go module path、包名与 import 路径、Docker 镜像名、CI 配置、注释与文档。我们做品牌改造时**只加不改**：新增 APIGX 自身标识，但保留全部上游标识。

## 6. 底座自带的强制规范（约束我们的新增代码）

底座仓库自带 `new-api/AGENTS.md`（27KB）、`new-api/web/AGENTS.md`、`new-api/CLAUDE.md`。**在 `new-api/` 下写代码时它们优先于通用风格偏好。** 与阶段 0 设计直接相关的硬约束：

| 领域 | 约束 | 对我们的影响 |
|---|---|---|
| 数据库 | 所有数据库代码必须同时兼容 **SQLite、MySQL ≥ 5.7.8、PostgreSQL ≥ 9.6**；单库测试、mock、只跑一种方言都不算验证 | 与计划书「优先 PostgreSQL」有张力，见 §7-1 |
| JSON | 根模块所有 marshal/unmarshal 必须走 `common.Marshal/Unmarshal/...`，禁止直接 `encoding/json` | 新增序列化代码必须照做 |
| 模块边界 | `relaykit/` 必须保持可独立构建，禁止 import 根模块包；改动后须 `cd relaykit && GOWORK=off go build ./...` | 协议转换相关改动走 relaykit |
| 计费不变量 | 配额计算不得因溢出/未校验输入产生负数；倍数输入必须有上界；配额换算统一用 `common/quota_math.go` 的 `QuotaFromFloat/QuotaRound/QuotaFromDecimal` 及 `*Checked` 变体；饱和事件必须可审计 | 直接约束我们的 `usage_only` 计量实现 |
| 认证安全 | 涉及认证流程的改动**必须**符合 OWASP ASVS 与 Cheat Sheet 要求，服务端强制，前端校验不能替代；审计事件不得含凭据 | 约束注册邀请、邮箱验证、平台 Key 等设计 |
| 测试 | 后端用 testify（`require` 建场、`assert` 断言）；不为小改动散落测试文件；禁止 smoke/sleep/随机输入的伪测试 | 约束 T0.8/T0.9 的验收脚本落位 |
| 插件 | 动 JS 任务插件前必须读 `docs/plugin-api/v1.md` | 阶段 3 异步任务可能涉及 |
| 文档 | **不得**在 `new-api/docs/` 下新增文件（除非用户明确要求）；不得在 `plugins/` 下生成文档 | 我们的设计文档一律放本仓库 `docs/`，不要写进底座 |
| 前端 | 包管理用 Bun；新增 UI 前必须检索并复用 `web/src/components/`，禁止重复实现；文案必须走 i18n `t()`；测试放模块专属 `__tests__/` | 分享广场与控制台必须沿用同一套组件与 i18n 体系 |

## 7. 与计划书的冲突与待决策项（提前暴露技术债）

这些是**现在就暴露、不要等到写代码才发现**的分歧点：

1. **数据库范围冲突**：计划书 §6 写「数据库优先 PostgreSQL」，底座 AGENTS.md 要求同时兼容 SQLite/MySQL/PostgreSQL。
   → **已决定（用户 2026-09-16）：按底座规矩走三库兼容。** 新增共享领域表一律用三库可移植的 GORM 类型与写法，PostgreSQL 作为生产首选而非唯一支持；不得悄悄破坏底座的兼容承诺。
2. **免费计量的落点**：底座已有完整的预扣费/结算/配额钳制链路。计划书要求 `usage_only` 模式。
   → 建议：在既有计费会话上做模式分支（保持鉴权、额度、计量，去掉充值余额前置），**不要**另起一套计量；否则会同时违反「不另起第二套系统」和「计费安全不变量」。
3. **容量控制位置**：底座的 `GetNextEnabledKey` 与选择互斥锁**不是**并发上限（计划书 §9.2 [N4j] 已指出）。
   → 新增贯穿请求生命周期的容量控制时，必须与底座的配额钳制/审计机制共存，不能绕过 `quota_math` 的饱和处理。
4. **依赖裁剪**：`stripe-go`、`go-epay` 属支付栈，计划书不做交易。
   → 裁剪优先级低、风险不确定（可能被计费链路隐式引用），登记为技术债候选，不在阶段 0 动。
5. **体量决定策略**：Go 20.8 万行 / 前端 24 万行，不可能通读。
   → 只能靠「扩展点集中 + 差异台账 + 定向检索」控制可维护性；禁止 fork 后到处散改，这是本项目最大的技术债风险来源。
6. **工具链差异**：底座前端要求 Bun + tsgo + oxlint，本机都没有；Go 语言版本指令 1.25.1 而本机 1.27.1。
   → 阶段 1 前端动工前必须补齐 Bun，否则无法遵守底座前端规范。

## 8. 差异追踪机制

相对上游的每一处改动都必须可查、可复现、可回滚。

- **权威台账**：`docs/stage0/upstream-diff.md`。任何改动上游已有文件的行为，都必须先登记再改。
- **报告脚本**：`tools/dev/upstream-diff.ps1`，把底座克隆相对固定提交的全部改动列出（含新增未跟踪文件），用于日常核对与升级前检查。
- **优先顺序**：新增文件 > 扩展上游文件的扩展点 > 修改上游既有逻辑。第三类是最高技术债，必须在台账中写明原因与上游升级时的冲突风险。
- **扩展点集中**：我们的新增代码集中到约定目录，不散落在上游目录里；具体目录在 T0.3 的复用/修改清单中确定。
- **升级检查清单**：升级上游版本时依次核对——台账每条改动是否仍适用、扩展点是否被上游同名文件覆盖、迁移是否与上游 AutoMigrate 冲突、`relaykit` 是否仍可独立构建、前端是否仍符合复用与 i18n 规范。

## 9. 未验证 / 已知限制

- 本节全部是**源码与文档层面的核查**：固定提交已获取、指纹已记录、许可文件已读取。**没有**编译过底座、没有跑过它的测试、没有部署过、没有做过任何性能或安全验证。
- 「依赖清单」是 `go.mod` / `package.json` 的归类摘要，不等于完整的传递依赖图；完整图需在真正构建时由 `go list -m all` / `bun pm ls` 生成，并应作为构建产物记录。
- 计划书 §5 提到的复用点（`fetch_models`、`ratio_sync`、`perf_metric`、`GetNextEnabledKey` 等）在本文件中尚未逐一定位到具体文件与函数，属于 T0.3 的工作范围。
