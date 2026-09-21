# 阶段 1 / T1.3：PostgreSQL 迁移矩阵驱动
#
#   .\tools\stage1-verify\migrate-pg-matrix.ps1
#
# 本脚本覆盖 PostgreSQL 的两种真实情形（SQLite 由 Go 单测覆盖；MySQL 由
# APIGX_TEST_MYSQL_DSN 驱动同一个 Go 矩阵测试）：
#   情形 A「全新库」        ：建一个空库，迁移必须一次建成全部 apigx_ 表，且连跑两次幂等
#   情形 B「从底座旧库升级」：直接指向底座已迁移过的库（有底座原生表），
#                            验证我们的表是**新增**的、底座表原样保留，且连跑两次幂等
#
# 注意：本脚本本身不运行 MySQL；MySQL 结果必须单独执行并独立记录。

[CmdletBinding()]
param(
  [string]$PgBin    = 'D:\PostgreSQL\bin',
  [int]   $Port     = 5433,
  [string]$DbUser   = 'apigx',
  [string]$DbPass   = 'apigx_dev',
  [string]$FreshDb  = 'apigx_matrix_fresh',
  [string]$BaseDb   = 'apigx_dev',
  [string]$RepoRoot = (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
)

$ErrorActionPreference = 'Stop'
$ExpectedApigxTableCount = 29
$psql = Join-Path $PgBin 'psql.exe'
if (-not (Test-Path $psql)) { throw "找不到 psql：$psql" }

# 自带环境加载：脚本可能被任意 shell 调用，不能假定调用者已经 dot-source 过 env.ps1
$envFile = Join-Path $RepoRoot 'tools\dev\env.ps1'
if (Test-Path $envFile) { . $envFile | Out-Null }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  throw '找不到 go：请确认 tools/dev/env.ps1 存在且可加载（它会把仓库内便携 Go 加进 PATH）'
}

$env:PGPASSWORD = $DbPass

function Invoke-Sql {
  param([string]$Database, [string]$Sql)
  # 原生命令把 NOTICE 写到 stderr（例如 drop database if exists 的「不存在,跳过」），
  # 在 ErrorActionPreference='Stop' 下会被提升为终止错误，因此这里临时放宽，
  # 只以退出码判断成败，并过滤掉 NOTICE 噪音。
  $prev = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  $out = & $psql -w -h 127.0.0.1 -p $Port -U $DbUser -d $Database -tAc $Sql 2>&1
  $code = $LASTEXITCODE
  $ErrorActionPreference = $prev
  if ($code -ne 0) { throw "psql 失败（$Database）：$out" }
  $clean = $out | Where-Object { "$_" -notmatch '^(注意|NOTICE|psql:)' }
  return ($clean | Out-String).Trim()
}

function Get-ApigxTableCount { param([string]$Database)
  return [int](Invoke-Sql -Database $Database -Sql "select count(*) from information_schema.tables where table_schema='public' and substr(table_name,1,6)='apigx_'")
}
function Get-AllTableCount { param([string]$Database)
  return [int](Invoke-Sql -Database $Database -Sql "select count(*) from information_schema.tables where table_schema='public'")
}

function Invoke-MigrationTest {
  param([string]$Database, [string]$Label)
  Write-Host "[apigx] $Label -> 库 $Database"
  $env:APIGX_TEST_PG_DSN = "postgres://${DbUser}:${DbPass}@127.0.0.1:${Port}/${Database}?sslmode=disable"
  Push-Location (Join-Path $RepoRoot 'new-api')
  try {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $out = & go test ./apigx/model/ -run 'TestMigrateMatrixOnConfiguredDialects/postgres' -v 2>&1
    $code = $LASTEXITCODE
    $ErrorActionPreference = $prev
  } finally {
    Pop-Location
    Remove-Item Env:\APIGX_TEST_PG_DSN -ErrorAction SilentlyContinue
  }
  $out | Select-String -Pattern '^(=== RUN|--- PASS|--- FAIL|--- SKIP|PASS|FAIL|ok)' | ForEach-Object { '    ' + $_.Line }
  if ($code -ne 0) { throw "迁移测试失败（$Label）：exit=$code" }
  Write-Host "    结果：PASS"
}

Write-Host '=== 情形 A：全新库 ==='
Invoke-Sql -Database 'postgres' -Sql "drop database if exists $FreshDb" | Out-Null
Invoke-Sql -Database 'postgres' -Sql "create database $FreshDb owner $DbUser" | Out-Null
$freshBefore = Get-AllTableCount -Database $FreshDb
Write-Host "[apigx] 迁移前：库内共 $freshBefore 张表"
Invoke-MigrationTest -Database $FreshDb -Label '迁移（含连续两次，验幂等）'
$freshAfter = Get-AllTableCount -Database $FreshDb
$freshApigx = Get-ApigxTableCount -Database $FreshDb
Write-Host "[apigx] 迁移后：apigx_ 表 $freshApigx 张，库内共 $freshAfter 张"

Write-Host ''
Write-Host '=== 情形 B：从底座旧库升级 ==='
$baseAllBefore = Get-AllTableCount -Database $BaseDb
$baseApigxBefore = Get-ApigxTableCount -Database $BaseDb
Write-Host "[apigx] 迁移前：库内共 $baseAllBefore 张表（其中 apigx_ 表 $baseApigxBefore 张）"
Invoke-MigrationTest -Database $BaseDb -Label '迁移（含连续两次，验幂等）'
$baseAllAfter = Get-AllTableCount -Database $BaseDb
$baseApigxAfter = Get-ApigxTableCount -Database $BaseDb
Write-Host "[apigx] 迁移后：apigx_ 表 $baseApigxAfter 张，库内共 $baseAllAfter 张"
$baseNative = $baseAllAfter - $baseApigxAfter
Write-Host "[apigx] 底座原生表仍有 $baseNative 张（升级不得删改底座表）"

Write-Host ''
Write-Host '=== 结果 ==='
$ok = $true
if ($freshApigx -ne $ExpectedApigxTableCount) { Write-Warning "全新库只建出 $freshApigx 张 apigx_ 表（期望 $ExpectedApigxTableCount）"; $ok = $false }
if ($baseApigxAfter -ne $ExpectedApigxTableCount) { Write-Warning "升级库只建出 $baseApigxAfter 张 apigx_ 表（期望 $ExpectedApigxTableCount）"; $ok = $false }
if ($baseNative -lt 30) { Write-Warning "底座原生表只剩 $baseNative 张，疑似被本次迁移影响"; $ok = $false }
if ($ok) { Write-Host "  PostgreSQL 矩阵通过：全新库与升级库均建成 $ExpectedApigxTableCount 张表，且底座表未被影响" -ForegroundColor Green }
Write-Host ''
Write-Host '注意：本脚本只覆盖 PostgreSQL；MySQL 必须另设 APIGX_TEST_MYSQL_DSN 运行对应子测试。' -ForegroundColor Yellow
