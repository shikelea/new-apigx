# 代码风格规范 · APIGX

> 基线：Google 风格（Go 用 `gofmt` + Google Go Style；前端用项目既有 React/TS 约定）。
> 规则：只追加不删除已有内容；每 5 轮任务结束时整理一次（去重、分类、排序）。
> 说明：以下「项目约定」直接来自 `docs/PROJECT_PLAN.md` v1.0，属于必须遵守的设计约束，不是偏好。

## 项目约定（来自计划书）

1. **授权与调用一律用稳定 ID**，名称只作展示：分享分组可重名，Grant、请求归属、Key 轮询索引都不依赖名称或批量导入行号（§2、§3.5）。
2. **退役用软删除**，保留被请求/任务/价格/计量记录引用的稳定 ID；展示名、价格、owner 变化不回写历史快照（§7）。
3. **时间统一 UTC 存储**，日限额按 Asia/Shanghai 划分，保存配置版本与请求准入日，跨日结算回到原预留周期；统计界面标注日期范围与时区（§8.5）。
4. **秘密处理分级**：平台 API Key 只存不可恢复校验摘要、仅创建时展示一次；上游 Key 用可轮换加密存储，主密钥与数据库分离；日志、导出、错误响应、监控标签、任务结果都不得携带凭据（§10.3）。
5. **外部输入一律不可信**：上游模型名/描述/URL、外部 Markdown、回调内容；渲染前转义，出站地址与重定向做 SSRF 校验（阻断环回/内网/云元数据）（§8.1、§10.3）。
6. **金额与计量用定点小数或整数最小单位**；价格与汇率快照绑定 Request / Attempt / AsyncTask，历史估算不随汇率更新改写（§8.5）。
7. **一次请求只有一个权威路由决定**：转发器接收确定的渠道与凭据引用，禁止在原生 `GetNextEnabledKey` 中二次抽选（§6、§11 验收）。
8. **不假设跨库原子事务**：PostgreSQL 事务管持久预算与请求状态，Redis 管并发/速率预留；执行顺序为「记录意图与预留 → 取得 Redis 容量 → 复验权限 → 记录出站尝试 → 发送」，失败走幂等补偿与对账（§9.2）。
9. **新增共享领域表独立命名**，不把分享分组塞进 New API 现有用户分组字段；扩展点集中且逐条记录相对上游的改动，便于升级核对（§5.3）。
10. **平台扩展请求头出站前剥离**，上游不得收到平台 Key 或内部授权信息（§9.1）。
11. **限制维度必须显式表达**：「未配置 / 禁用 / 继承默认 / 明确不限」分别存储，空字段不得解释为无限容量（§9.2）。
12. **免费模式改的是结算路径而非数值**：保持鉴权、额度与计量，取消充值余额前置条件；不得通过把价格统一设零或给超大余额实现（§6）。

## 本机与工具链约定（2026-09-16 实测，违反会静默出错）

13. **`.ps1` 必须 UTF-8 带 BOM**：本工具的 shell 是 Windows PowerShell **5.1**（`$PSVersionTable.PSVersion` = 5.1.26100.9444），它把无 BOM 的 UTF-8 文件按 ANSI/GBK 读取。中文注释会吞掉换行符，导致**下一行代码被并进注释且不报任何错**（已实际踩中：`tools/dev/env.ps1` 中一行 `$env:APIGX_PROXY` 赋值被静默丢弃）。新增或改写 `.ps1` 后必须校验首三字节：

    ```powershell
    $b = [System.IO.File]::ReadAllBytes('tools\dev\env.ps1'); "{0:X2} {1:X2} {2:X2}" -f $b[0],$b[1],$b[2]   # 应为 EF BB BF
    ```

    **编辑工具重写 `.ps1` 会丢掉 BOM，每次改完都要重新补。** `.md` / `.sql` / `.json` 不由 PowerShell 解析，保持无 BOM 的 UTF-8。

    同一个原因还有一个**假故障**：PS 5.1 用 `Get-Content` 读无 BOM 的 UTF-8 文件时也会按 GBK 显示成乱码，容易被误判为「文件被写坏了」。判断文件真实内容用 `Get-Content -Encoding UTF8`，或直接用 read 工具；不要用裸 `Get-Content` 的显示结果下结论。仓库内已有守卫脚本：`.\tools\dev\check-ps1-bom.ps1`（报告）与 `-Fix`（就地补 BOM）。

13b. **不要在 PowerShell 里写含非 ASCII 内容的文件**：`Set-Content` / `Add-Content` / `Out-File` / here-string 都会走 PS 5.1 的 ANSI 编码路径，结果不只是乱码——**多字节序列会吞掉换行，把两行合并**，产生的 Go/JS 源文件直接语法错误（已实际踩中一次：一份 77KB 的 Go 文件被压成 304 行、668 个乱码字符，`go build` 报 `expected 'package', found ID`）。

    - 写 `.go` / `.mjs` / `.md` 等含中文的文件：一律用 **write / edit 文件工具**。
    - 用 PowerShell 只做读取、编译、跑测试、拼 ASCII 命令。
    - 需要校验文件是否真的坏了：用 read 工具，或按字节统计中文与可疑乱码字符的数量，**不要只看 `Get-Content` 的显示**。
14. **需要 HTTPS 下载时不要用 curl / Invoke-WebRequest / .NET WebClient**：沙箱进程内 Schannel 报 `schannel: AcquireCredentialsHandle failed: SEC_E_NO_CREDENTIALS (0x8009030e)`，必然失败（TCP 与代理隧道本身是好的，别误判为断网）。统一走：下载用 `node tools/net/fetch.mjs <url> [outFile] [--direct]`；Git 用仓库级 `http.https://github.com/.proxy`；Go 用 `GOPROXY=https://goproxy.cn,direct`。
15. **不要跨命令依赖环境变量**：每次 `pwsh` 调用都是全新进程，开工先 `. .\tools\dev\env.ps1`，它把 `.toolchain/go`、`.toolchain/redis` 挂进 PATH 并设置 Go 缓存与镜像。
16. **开发期密钥不进仓库**：`.env` 已在 `.gitignore`；数据库密码只作为参数或 `PGPASSWORD` 传给 `tools/dev/init-db.ps1`，不写进脚本、文档、日志或提交信息。`.toolchain/` 属于本机缓存，不入库。

## 底座（new-api）强制规范 — 在 `new-api/` 下写代码时这些优先

完整条款见 `new-api/AGENTS.md`（27KB，**改代码前必读**）、`new-api/web/AGENTS.md`、`new-api/CLAUDE.md`。这里是不可违反的硬约束摘要：

17. **数据库三兼容**：所有数据库代码必须同时兼容 SQLite、MySQL ≥ 5.7.8、PostgreSQL ≥ 9.6。单库测试、mock、只跑一种方言都不算验证；schema/迁移改动要在全新库与旧版本升级库上各跑一次，并验证启动迁移的幂等性。
18. **JSON 走包装函数**：根模块内禁止直接 `encoding/json` 做 marshal/unmarshal，统一用 `common.Marshal/Unmarshal/UnmarshalJsonStr/DecodeJson`；`relaykit/` 内用 `kitutil.*`。
19. **`relaykit/` 保持独立可构建**：不得 import 根模块包，改动后必须 `cd relaykit && GOWORK=off go build ./...`。
20. **计费安全不变量**：配额/计费不得因溢出或未校验输入产生负数；用户可控的倍数（图片张数、视频秒数、分辨率档位等）必须先有上界校验；配额换算只用 `common/quota_math.go` 的 `QuotaFromFloat/QuotaRound/QuotaFromDecimal` 及其 `*Checked` 变体，禁止裸 `int(...)` 强转；饱和事件必须通过 `attachQuotaSaturation` 留痕。
21. **认证安全按 OWASP ASVS 执行**：涉及注册、登录、找回、邮箱验证、MFA、WebAuthn、OAuth、会话、API 凭据的改动必须对照 ASVS 与 Cheat Sheet，服务端强制，前端校验不能替代；审计事件不得包含任何凭据。
22. **受保护标识只增不改**：`New API` / `QuantumNous` 相关的品牌、署名、模块路径、镜像名、footer/about 等不得修改或删除；APIGX 自身标识只做新增。
23. **不要往底座文档目录写文件**：禁止在 `new-api/docs/` 下新增文档，禁止在 `new-api/plugins/**` 下生成 README 等文档；我们的设计文档一律放本仓库 `docs/`。
24. **前端必须复用与 i18n**：改 `web/` 前先读 `web/AGENTS.md` 并检索 `web/src/components/` 与对应 feature，优先复用业务封装而不是重新拼装；面向用户文案必须走 `t()`；测试放模块专属 `__tests__/`；包管理用 Bun。

---

## 25. 二开第一原则：动手前先搜底座（最高优先级）

**这是本项目最容易犯、代价最大的错误**：底座（New API）已经实现了大量通用能力，我们在 `apigx/` 下重写一份，等于制造第二套系统——两套状态会互相竞争，行为不一致，且维护成本翻倍。

### 动手前的强制动作

写任何「通用能力」代码之前，**必须先 grep 底座**（关键词 + 看 `model/`、`service/`、`controller/`、`middleware/`）：

| 你要做的事 | 先在底座搜 | 已确认底座已有（禁止重写） |
|---|---|---|
| 多 Key 选择 / 轮询 / 随机 | `MultiKey`、`GetNextEnabledKey` | ✅ `model/channel.go` 的 `GetNextEnabledKey`、`constant/multi_key_mode.go` |
| 逐 Key 健康 / 禁用 / 冷却 | `MultiKeyStatusList`、`UpdateChannelStatus` | ✅ `model/channel.go` 的 `handlerMultiKeyUpdate` |
| 批量导入 Key | `IsMultiKey`、`MultiKeySize` | ✅ `controller/channel.go` 渠道新增/编辑路径（内联，不可直接作为库复用） |
| 渠道选择 / 分组 / 优先级 | `Distribute`、`CacheGetRandomSatisfiedChannel` | ✅ `middleware/distributor.go` |
| 限流 | `rate-limit`、`RateLimit` | ✅ 用户级窗口限流；**渠道/Key 级并发上限确实没有**（阶段 0 已实测确认，故 `apigx/capacity` 是必要新增） |
| 计费 / 预扣 / 结算 | `BillingSession`、`FundingSource` | ✅ `service/billing_session.go`、`funding_source.go`（我们的 `usage_only` 是**新增资金来源**，符合 §6 要求，不是重写） |
| 重试 / 失败转移 | `Retry`、`ShouldRetry` | ✅ `controller/relay.go`、`service/` 相关 |
| 凭据加密 | `encrypt`、`crypto` | ❌ 底座渠道 Key 是**明文**；计划书 §10.3 要求加密 → `apigx/secretbox` 是必要新增 |

### 判定标准（三条同时成立才允许自己写）

1. 底座**完全没有**该能力（写出 grep 证据，记进文档）；
2. 或者底座有但**形态不可复用**（例如逻辑内联在控制器里、或绑定了管理员权限模型），且差异是**语义性**的而非风格性的；
3. 或者是计划书明确要求的**有意偏离**（如加密存储、免费计量模式），且已在台账/技术债中登记。

否则：**改底座**（登记 `docs/stage0/upstream-diff.md` 台账 + 跑三库矩阵），或**直接调用底座**。

### 一个已发生的反面案例（务必记住）

T1.5 初期我按计划书「轮询/随机/加权/剩余容量」写了一整套 `SelectUpstreamKey`。核查后发现：底座不但已有轮询与随机，而且已在 `SetupContextForSelectedChannel` 里完成了**唯一的选路决定**（计划书 §9.2 要求「一个权威决定点」）。我那套代码不只是重复，还会**与底座的轮询游标互相竞争**。已删除。

**教训**：计划书里写着「要做 X」不等于「要自己实现 X」。先问「底座怎么做 X」，再决定是调用、扩展还是新增。
## 附：阶段 2 期间积累的工程教训（血换来的，别重走）

这些不是风格偏好，每一条都对应一次真实的返工或误判。

26. **"静默失效"是这套底座里最高频的缺陷形态**：代码看起来在跑、不报错、不告警，但**什么也没发生**。本阶段至少遇到六种，务必逐条防：
    - **钩子挂在死路径上**：`PostConsumeQuota` 是"没有计费会话时的回退路径"，正常请求根本不经过它——挂在上面表现为"统计一行都没有、日志也没有任何提示"。
    - **脚本句柄复用**：go-redis 的 `Script.Run` 执行的是**它自己绑定的脚本**；复用 `window` 句柄去跑调整脚本，会让"调整"实际执行成"自增"（返回值恒 0、计数纹丝不动）。
    - **门控只看了一个来源**：`if cfg.ChannelRPMLimit > 0` 导致"只配渠道策略、不配环境变量"时整段被跳过。
    - **提前返回排在解析之前**："所有维度都未配置就返回"排在了 RPM 解析之前 → 只配 RPM 时形同虚设。
    - **精确匹配把长窗口变成空数据**：按 `window_kind` 精确匹配，而写入只产生分钟桶 → 6 小时窗口**永远查不到样本**。
    - **同一件事两处口径**：`route-explain` 不应用熔断判定而 `route-apply` 应用 → 解释里"它在候选里"、实际请求却不会用它。
    **对策**：任何"应该发生某事"的改动，都要有**一个能证明它发生了的观测**（计数变化、日志、接口输出），而不只是"测试通过"。

27. **索引空间是这类改动的主要风险面**：按权重展开多 Key 列表时，槽位下标**不能再用来索引原始 Key 列表**——一旦有凭据解不开被跳过，两套下标错位，状态就会映射到**另一把 Key** 上。凡是"改投影/改列表"的改动，先问一句：**谁还在用旧的下标？**

28. **验证之前先确认"你在验证哪个二进制"**：本轮曾让整轮 E2E 打在**旧网关**上（新二进制因端口占用启动失败），而新接口在旧二进制上不存在（404 被脚本读成"0 个候选"）。另外 `Get-NetTCPConnection` 在本环境**会漏报监听者**，用 `netstat -ano` 才可靠。

29. **每个端到端脚本都有自己的"网关开关前置条件"**：缺一个开关就会表现为"功能失效"，极易误判成回归。跑验收前先按对照表把开关打开（见 TASK_LOG 的阶段 2 验收汇总）。

30. **测试设计的三条**：
    - **确定性单测优于并发 E2E**——当"失败原因不可分辨"时（被拒可能来自准入、也可能来自 Key 选择），单测才是能说明问题的那个。
    - **假阳性断言比没有断言更危险**：曾写"响应不含字符 `1`"作为泄露扫描，而 `owner_user_id=8701` 本身就含 `1`——看着严格、实则毫无意义。要断言**结构**（`"channel_id"` 这类键名）与**有区分度的长值**。
    - **不要猜字段名与常量名**：本阶段猜错过 `types.IsSkipRetry`、`UpstreamKeyStatusActive`、日志字段 `auth`、超时错误码——每次都是查证后才对。

31. **三库差异会咬人**：`apigx_quality_buckets.id` 在 PostgreSQL 里曾建成 `character` 且无默认值，**SQLite 的动态类型容忍了它**，因此单测全绿、只有真实 PG 暴露；而 **`AutoMigrate` 不修复已存在列的类型**。凡涉及 schema/列类型的改动，必须在**真实三库**上验证（且不要只比"表数量"）。

32. **写 JSON 列必须"加载 → 改字段 → Update"**：直接 `Updates(map{"channel_info": ChannelInfo{...}})` 不会持久化 map 字段（读回全是 0）。测试里踩到过一次，浪费了一轮排查。