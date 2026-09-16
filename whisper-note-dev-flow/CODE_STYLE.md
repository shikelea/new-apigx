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
