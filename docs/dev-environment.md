# APIGX 开发环境说明

记录本机（Windows 开发机）跑通 APIGX 开发所需的实际环境、已知坑与验证证据。
对应任务：`TASK_LOG.md` 的 T0.1。建立日期：2026-09-16。

## 1. 当前状态

| 组件 | 状态 | 位置 / 版本 | 验证证据 |
|---|---|---|---|
| Git | ✅ | 2.53.0.windows.1 | 仓库已 `git init -b main` |
| Node.js | ✅ | v24.14.0（`C:\Program Files\nodejs`） | 用于出网下载与前端 |
| Go | ✅ | 1.27.1 windows/amd64 | `go version` → `go1.27.1 windows/amd64`；`go get` 成功拉取 pgx v5.11.0、go-redis v9.22.0 |
| Redis | ✅ | 5.0.14.1（Windows 便携版） | `redis-cli ping` → `PONG`（127.0.0.1:6379） |
| PostgreSQL | ✅ | 工作区内独立实例：18.4，数据目录 `.toolchain/pgdata`，监听 127.0.0.1:**5433**，角色 `apigx`，库 `apigx_dev` | `psql -h 127.0.0.1 -p 5433 -U apigx -d apigx_dev` → `apigx @ apigx_dev \| PG 18.4` |
| Docker / Podman | ❌ 未安装 | — | 因此 PostgreSQL/Redis 均使用本机原生安装，不用容器 |
| WSL | ⚠️ 不可用 | `wsl -l -v` → `E_ACCESSDENIED` | 不依赖 WSL |
| 出网 | ✅（经绕行） | 见第 2 节 | `goproxy.cn` HTTP 200；GitHub API HTTP 200；`git ls-remote` 成功 |

## 2. 坑一：本机沙箱内 HTTPS 全部失败（Schannel）

**现象**：在 DSH 沙箱进程里，curl / PowerShell / .NET 的 HTTPS 一律失败：

```
curl: (35) schannel: AcquireCredentialsHandle failed: SEC_E_NO_CREDENTIALS (0x8009030e)
```
PowerShell 侧表现为 `基础连接已经关闭: 接收时发生错误`。

**已排除的原因**：不是 DNS（`github.com` 正常解析）、不是断网（`ping 8.8.8.8` 通、经代理 `CONNECT` 返回 `HTTP/1.1 200 Connection established`）。
**实际原因**：Windows Schannel 在受限令牌下无法取得凭证。**TCP 与代理隧道本身是好的**，坏的是本进程的 TLS 栈。

**可用通道**：

| 通道 | 用法 |
|---|---|
| Node（自带 OpenSSL） | `node tools/net/fetch.mjs <url> [outFile] [--direct]`；默认经 `APIGX_PROXY`（`http://127.0.0.1:7897`）CONNECT 隧道访问境外站点，`--direct` 直连国内镜像 |
| Git（自带 OpenSSL） | 仓库级已配置 `http.https://github.com/.proxy = http://127.0.0.1:7897`，`git clone/fetch/ls-remote` 可直接用 |
| Go（自带 crypto/tls） | 走 `GOPROXY=https://goproxy.cn,direct`，不需要代理 |

国内镜像（Node 直连可通）：`golang.google.cn`、`mirrors.aliyun.com/golang`、`goproxy.cn`、`registry.npmmirror.com`。
注意 `mirrors.aliyun.com/goproxy` 实测超时，Go 代理只用 `goproxy.cn`。

**不要**再尝试用 curl/Invoke-WebRequest 下载任何东西——会稳定失败，不要把它当成网络不通。

## 3. 坑二：本工具的 shell 是 Windows PowerShell 5.1，`.ps1` 必须有 BOM

`$PSVersionTable.PSVersion` = **5.1.26100.9444**（不是 pwsh 7）。PS 5.1 把**无 BOM 的 UTF-8 文件按 ANSI/GBK 读取**，中文注释会变成乱码，更严重的是多字节序列会吞掉换行符，导致**下一行代码被并进注释里静默不执行**。

真实事故：`tools/dev/env.ps1` 里 `$env:APIGX_PROXY = '...'` 紧跟在一行中文注释之后，结果整行被注释吞掉，变量一直是空的，且没有任何报错。

**约定**：本仓库所有 `.ps1` 一律以 UTF-8 **带 BOM** 保存（`EF BB BF`）。新增或改写后用下面的方式校验：

```powershell
$b = [System.IO.File]::ReadAllBytes('tools\dev\env.ps1'); "{0:X2} {1:X2} {2:X2}" -f $b[0],$b[1],$b[2]   # 应为 EF BB BF
```

`.md`、`.sql`、`.json` 不受影响（不由 PowerShell 解析），保持无 BOM 的 UTF-8 即可。

## 4. 目录布局与启动方式

```
.toolchain/                    # 便携工具链，已在 .gitignore 中，不入库
├── go/                        # Go 1.27.1（go1.27.1.windows-amd64.zip，SHA256 已核对）
├── redis/                     # Redis-x64-5.0.14.1（含 redis-server.exe / redis-cli.exe）
│   └── data/                  # Redis 工作目录（本地开发关闭持久化）
├── bun/bun-windows-x64/       # Bun 1.4.2（前端包管理器，底座前端规范要求）
├── pgdata/                    # 本地 PostgreSQL 数据目录（独立实例，initdb 生成）
├── gopath/ gocache/ gotmp/    # Go 缓存，全部留在仓库内，避免污染用户目录
├── bun-home/ bun-cache/ npm-cache/  # Bun 与 npm 的安装目录与缓存（工作区外会被沙箱拒绝）
└── _download/ _meta/          # 安装包与下载清单（可随时删除）
tools/dev/env.ps1              # 环境引导，每个新 shell 都要 dot-source
tools/dev/pg.ps1               # 本地 PostgreSQL 启停/状态/连接（start|stop|status|psql|log）
tools/net/fetch.mjs            # 出网下载助手（见第 2 节）
deploy/local/redis.conf        # 本地 Redis 配置（127.0.0.1:6379，关闭持久化，maxmemory 256mb）
deploy/local/init-db.sql       # PostgreSQL 角色/库初始化（幂等）
tools/dev/init-db.ps1          # 调用上面的 SQL，创建 apigx 角色与 apigx_dev 库
.env.example                   # 环境变量模板（复制为 .env，.env 不入库）
```

**每次开工（每个新 shell 都是全新进程）**：

```powershell
. .\tools\dev\env.ps1
```

**启动 Redis**（前台运行，日志直接可见）：

```powershell
. .\tools\dev\env.ps1
redis-server.exe deploy\local\redis.conf
```

## 5. PostgreSQL：工作区内的独立实例

**不使用系统服务实例。** 本机原有的 `postgresql-x64-18` 服务监听 5432、认证为 `scram-sha-256`，需要超级用户密码；本方案改用同一套 PG 18.4 二进制，在仓库内 `initdb` 出一个**完全由项目自己掌握**的实例，不需要任何系统密码，也不改动系统配置。

| 项 | 值 |
|---|---|
| 数据目录 | `.toolchain/pgdata` |
| 监听 | `127.0.0.1:5433`（与系统实例的 5432 不冲突） |
| 超级用户 / 角色 | `apigx`（本实例的超级用户，即应用角色） |
| 数据库 | `apigx_dev` |
| 密码 | `apigx_dev`（本机开发默认值，仅监听回环；**不入库**，`.env` 已被 gitignore） |
| 管理脚本 | `tools/dev/pg.ps1 start\|stop\|status\|psql\|log` |

常用操作：

```powershell
.\tools\dev\pg.ps1 status     # 端口 + 版本 + 当前角色/库
.\tools\dev\pg.ps1 psql       # 交互式连接 apigx_dev
.\tools\dev\pg.ps1 start      # 在你自己的普通终端里启动（进程常驻）
.\tools\dev\pg.ps1 stop
```

连接串（写入 `.env`）：

```
DATABASE_URL=postgres://apigx:apigx_dev@127.0.0.1:5433/apigx_dev?sslmode=disable
```

### 两个 Windows／沙箱限制（都已在脚本里绕开或说明）

1. **`pg_ctl` 无法启动**：Windows 下 postgres 拒绝以管理员令牌运行，`pg_ctl` 会尝试创建受限令牌并失败——`pg_ctl: could not create restricted token: error code 87`。因此启动改为直接拉起 `postgres.exe`。
2. **DSH 沙箱会回收整个进程树**：`Start-Process` 起来的进程在本条命令结束时随之被杀（日志表现为「数据库系统中断…数据库系统关闭」）。所以在 DSH 内部必须**用受管后台作业**托管数据库；在你自己终端里运行 `pg.ps1 start` 则不受此限制、可长期常驻。同理，沙箱内 `pg_ctl stop` 发信号会被拒（`Operation not permitted`），脚本已回退到按 `postmaster.pid` 结束进程。

### 备选：改用系统服务实例

如果希望统一用 5432 上那个服务实例（例如为了和别人的环境一致），需要用户提供超级用户密码，然后执行 `tools/dev/init-db.ps1` 创建 `apigx` 角色与库；该脚本幂等，且不会修改超级用户配置。

## 6. 本地开发用的底座选项（可回滚）

底座的 relay 链路上挂了 `SystemPerformanceCheck` 中间件（`middleware/performance.go:64-67`），磁盘使用率超过阈值时直接返回 `503 system_disk_overloaded`。本机 D: 盘实测占用 **98.9%**（`dir D:\` 显示仅剩约 5.3 GB），默认阈值 95% 必然触发，因此本地联调时把阈值放宽：

```powershell
# 需要先登录拿 dashboard access_token，见 tools/stage0-verify/wire-base.mjs 第 4 步
PUT /api/option/   { "key": "performance_setting.monitor_disk_threshold", "value": "99" }
```

这是**本地开发的环境处置，不是功能改动**：它只改数据库里的运营配置，不动代码，随时可改回 95。生产环境应按真实容量规划处理，不要沿用这一条。

**磁盘告警**：D: 仅剩约 5.3 GB。仓库自身占用约 2.3 GB（`gocache` 734MB、`new-api/web/node_modules` 1.0GB、`go` 236MB、`pgdata` 67MB、`dist` 56MB）。已清理可再下载的安装包（`.toolchain/_download`，释放 125MB）。如需进一步腾空间，可执行 `go clean -cache`（释放约 734MB，代价是重新编译变慢）。

## 7. 未验证 / 已知限制

- 上面列出的证据都是**环境层面**的（工具链、连通性、服务可达）。APIGX 自身尚无一行业务代码，阶段 0 的功能性验收（归属隔离、定向渠道/Key 不被二次选择、免费计量、容量准入、对象隔离）尚未开始。
- Redis 5.0.14.1 是 Windows 非官方移植版，仅用于本地开发；生产按计划书使用 Linux 正式版本，语义相关（原子性、Lua、过期）在阶段 0 的真实验证中仍需复核。
- 本机无 Docker，因此「一键起环境」不可用；本地环境靠上面的脚本手工起。
