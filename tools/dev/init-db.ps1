# 初始化 APIGX 本地 PostgreSQL（幂等，可重复执行）
#
# 用法（在仓库根目录执行；本机实例为 PostgreSQL 18.4，二进制在 D:\PostgreSQL\bin）：
#   .\tools\dev\init-db.ps1 -AdminPassword '<postgres 超级用户密码>'
# 或先设置环境变量再运行，避免密码进入命令行历史：
#   $env:PGPASSWORD='<postgres 密码>'; .\tools\dev\init-db.ps1
#
# 脚本只会创建/更新应用角色与数据库，不修改超级用户配置。
# 认证方式为 scram-sha-256（见 D:\PostgreSQL\data\pg_hba.conf），因此必须提供密码。

[CmdletBinding()]
param(
  [string]$AdminPassword = $env:PGPASSWORD,
  [string]$AdminUser     = 'postgres',
  [string]$PgBin         = 'D:\PostgreSQL\bin',
  [string]$AppUser       = 'apigx',
  [string]$AppPassword   = 'apigx_dev',
  [string]$Database      = 'apigx_dev',
  [string]$HostName      = '127.0.0.1',
  [int]   $Port          = 5432
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($AdminPassword)) {
  Write-Error '缺少超级用户密码。请用 -AdminPassword 参数，或先设置 $env:PGPASSWORD。'
}

$psql = Join-Path $PgBin 'psql.exe'
if (-not (Test-Path $psql)) { Write-Error "找不到 psql：$psql" }

$sqlFile = Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'deploy\local\init-db.sql'
if (-not (Test-Path $sqlFile)) { Write-Error "找不到 SQL 文件：$sqlFile" }

Write-Host "[apigx] 初始化角色 $AppUser 与数据库 $Database（$HostName`:$Port）"

$env:PGPASSWORD = $AdminPassword
try {
  & $psql -w -h $HostName -p $Port -U $AdminUser -d postgres `
    -v ON_ERROR_STOP=1 `
    -v "app_user=$AppUser" `
    -v "app_password=$AppPassword" `
    -v "db_name=$Database" `
    -f $sqlFile
  if ($LASTEXITCODE -ne 0) { Write-Error "psql 执行失败，退出码 $LASTEXITCODE" }
}
finally {
  Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
}

Write-Host "[apigx] 用应用角色自检连接 ..."
$env:PGPASSWORD = $AppPassword
try {
  & $psql -w -h $HostName -p $Port -U $AppUser -d $Database -tAc 'select current_user, current_database(), version();'
  if ($LASTEXITCODE -ne 0) { Write-Error "应用角色连接失败，退出码 $LASTEXITCODE" }
}
finally {
  Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
}

Write-Host "[apigx] 完成。把连接串写入 .env（模板见 .env.example）："
Write-Host "        DATABASE_URL=postgres://${AppUser}:<密码>@${HostName}:${Port}/${Database}?sslmode=disable"
