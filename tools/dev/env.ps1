# APIGX 开发环境引导（dot-source 使用）
#
#   . .\tools\dev\env.ps1
#
# 作用：把仓库内的便携工具链（Go、Redis）挂到当前会话的 PATH，并设置国内镜像。
# 每个 pwsh 调用都是全新进程，因此每次开工前都需要 dot-source 一次。
#
# 重要背景（2026-09-16 实测）：本机在 DSH 沙箱进程内 Windows Schannel 无法取得凭证，
# curl / PowerShell / .NET 的 HTTPS 一律报
#   schannel: AcquireCredentialsHandle failed: SEC_E_NO_CREDENTIALS (0x8009030e)
# 因此需要用 HTTPS 下载时，统一改用 Node（自带 OpenSSL）：
#   node tools/net/fetch.mjs <url> [outFile] [--direct]
# Git 自带 OpenSSL，经本地代理也可正常访问 GitHub。

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)

$env:GOROOT = Join-Path $repoRoot '.toolchain\go'
$env:GOPATH = Join-Path $repoRoot '.toolchain\gopath'
$env:GOMODCACHE = Join-Path $env:GOPATH 'pkg\mod'
$env:GOCACHE = Join-Path $repoRoot '.toolchain\gocache'
$env:GOTMPDIR = Join-Path $repoRoot '.toolchain\gotmp'

# 国内 Go 模块代理；境外源仍需本地代理时用 APIGX_PROXY 手动下载
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'sum.golang.google.cn'
$env:GONOSUMCHECK = ''

$env:PATH = "$(Join-Path $env:GOROOT 'bin');$(Join-Path $repoRoot '.toolchain\redis');$(Join-Path $repoRoot '.toolchain\bun\bun-windows-x64');$env:PATH"

# 本地 HTTP 代理（Clash 等）。仅 Git 与 node tools/net/fetch.mjs 使用；
# 沙箱内的 curl/PowerShell 即使走它也会因 Schannel 失败。
$env:APIGX_PROXY = 'http://127.0.0.1:7897'

# Bun：底座前端规范要求的包管理器（bun.lock 已随底座锁定）。
# 安装目录与缓存都下沉到仓库内，避免沙箱拒绝写用户目录。
$env:BUN_INSTALL = Join-Path $repoRoot '.toolchain\bun-home'
$env:BUN_INSTALL_CACHE_DIR = Join-Path $repoRoot '.toolchain\bun-cache'

# npm 的全局缓存/prefix 默认在工作区外，会被沙箱拒绝（EPERM），统一下沉到仓库内
$env:npm_config_cache = Join-Path $repoRoot '.toolchain\npm-cache'

foreach ($d in @($env:GOPATH, $env:GOMODCACHE, $env:GOCACHE, $env:GOTMPDIR, $env:BUN_INSTALL, $env:BUN_INSTALL_CACHE_DIR, $env:npm_config_cache)) {
  if (-not (Test-Path $d)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }
}

Write-Host "[apigx] GOROOT=$env:GOROOT"
Write-Host "[apigx] GOPROXY=$env:GOPROXY  APIGX_PROXY=$env:APIGX_PROXY"
Write-Host "[apigx] bun=$((Get-Command bun -ErrorAction SilentlyContinue).Source)"
