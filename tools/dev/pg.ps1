# APIGX 本地 PostgreSQL 实例管理（工作区内的独立实例，不碰系统服务）
#
#   .\tools\dev\pg.ps1 start     # 启动（127.0.0.1:5433）
#   .\tools\dev\pg.ps1 status    # 查看状态
#   .\tools\dev\pg.ps1 psql      # 用应用角色连到 apigx_dev
#   .\tools\dev\pg.ps1 log       # 查看最后 40 行日志
#   .\tools\dev\pg.ps1 stop      # 停止
#
# 为什么不用系统服务实例：本机已有的 postgresql-x64-18 服务监听 5432，采用
# scram-sha-256，需要用户提供超级用户密码；而本脚本用同一套 PG 18.4 二进制在
# 仓库内 initdb 出一个独立实例，认证与数据目录都由项目自己掌握，不需要系统密码，
# 也不改动任何系统配置。
#
# 已知限制（都在下方实现里绕开或已说明）：
#
# 1) Windows 下 postgres 拒绝以管理员令牌运行，pg_ctl 在受限令牌环境里会报
#      pg_ctl: could not create restricted token: error code 87
#    因此 start 直接用 postgres.exe 拉起进程（不经 pg_ctl）。
#
# 2) DSH 沙箱会在每条命令结束时回收整个进程树，Start-Process 起来的服务随之退出
#    （日志表现为「数据库系统中断…数据库系统关闭」）。所以：
#      - 在 DSH 内部：请把数据库作为受管后台作业托管，不要指望本脚本的 start 能常驻；
#      - 在你自己的普通终端里：`.\tools\dev\pg.ps1 start` 可长期常驻运行。
#
# 3) 沙箱内 pg_ctl stop 发送信号会被拒（Operation not permitted），
#    stop 已回退到按 postmaster.pid 结束进程，仍失败时给出提示而不是假装成功。

[CmdletBinding()]
param(
  [ValidateSet('start', 'stop', 'status', 'psql', 'log')]
  [string]$Action = 'status',
  [int]$Port = 5433,
  [string]$PgBin = 'D:\PostgreSQL\bin',
  [string]$AppUser = 'apigx',
  [string]$Database = 'apigx_dev'
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$dataDir = Join-Path $repoRoot '.toolchain\pgdata'
$outLog = Join-Path $dataDir 'server.out.log'
$errLog = Join-Path $dataDir 'server.err.log'
$pidFile = Join-Path $dataDir 'postmaster.pid'

$postgres = Join-Path $PgBin 'postgres.exe'
$pgCtl = Join-Path $PgBin 'pg_ctl.exe'
$psql = Join-Path $PgBin 'psql.exe'

if (-not (Test-Path $postgres)) { throw "找不到 $postgres" }
if (-not (Test-Path $dataDir)) {
  throw "找不到数据目录 $dataDir（初始化：$PgBin\initdb.exe -D `"$dataDir`" -U $AppUser --auth-host=scram-sha-256 --pwfile=<文件>）"
}

function Test-PortUp {
  param([int]$P)
  $client = New-Object System.Net.Sockets.TcpClient
  try {
    $client.Connect('127.0.0.1', $P)
    return $true
  } catch {
    return $false
  } finally {
    $client.Dispose()
  }
}

switch ($Action) {

  'start' {
    if (Test-PortUp -P $Port) { Write-Host "[apigx] 已在运行：127.0.0.1:$Port"; break }
    Write-Host "[apigx] 启动本地 PostgreSQL：$dataDir -> 127.0.0.1:$Port"
    Start-Process -FilePath $postgres `
      -ArgumentList @('-D', $dataDir, '-p', "$Port", '-c', 'listen_addresses=127.0.0.1') `
      -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog | Out-Null

    $up = $false
    foreach ($i in 1..30) {
      Start-Sleep -Milliseconds 500
      if (Test-PortUp -P $Port) { $up = $true; break }
    }
    if ($up) { Write-Host "[apigx] 已启动（等待 $([math]::Round($i * 0.5, 1))s）" }
    else { Write-Warning "端口未在 15 秒内监听，请查看日志：$errLog" }
  }

  'stop' {
    if (-not (Test-PortUp -P $Port)) { Write-Host "[apigx] 未在运行"; break }
    Write-Host "[apigx] 停止本地 PostgreSQL"

    # 注意：原生命令往 stderr 写内容时，在 $ErrorActionPreference='Stop' 下会被
    # 提升为终止错误并中断脚本，这里临时放宽，保证后面的回退逻辑一定执行。
    $prevEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & $pgCtl -D $dataDir -m fast stop 2>&1 | Select-Object -Last 3
    $ErrorActionPreference = $prevEap

    if (Test-PortUp -P $Port) {
      Write-Warning 'pg_ctl 未能停止（沙箱内可能不允许发送信号），回退到按 postmaster.pid 结束进程'
      if (Test-Path $pidFile) {
        $pid0 = (Get-Content $pidFile -TotalCount 1).Trim()
        $ErrorActionPreference = 'Continue'
        Stop-Process -Id ([int]$pid0) -Force -ErrorAction SilentlyContinue
        $ErrorActionPreference = $prevEap
        Start-Sleep -Seconds 2
      }
    }
    if (Test-PortUp -P $Port) {
      Write-Warning "仍在监听 $Port：当前沙箱可能不允许结束该进程，请用任务管理器结束 postgres.exe，或让本实例保持运行。"
    } else {
      Write-Host '[apigx] 已停止'
    }
  }

  'status' {
    $up = Test-PortUp -P $Port
    Write-Host "[apigx] 数据目录：$dataDir"
    Write-Host "[apigx] 127.0.0.1:$Port -> $(if ($up) { '运行中' } else { '未运行' })"
    if ($up) {
      $env:PGPASSWORD = 'apigx_dev'
      try {
        & $psql -w -h 127.0.0.1 -p $Port -U $AppUser -d $Database -tAc "select current_user || ' @ ' || current_database() || ' | ' || split_part(version(),' ',2);"
      } finally {
        Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
      }
    }
  }

  'psql' {
    $env:PGPASSWORD = 'apigx_dev'
    try {
      & $psql -w -h 127.0.0.1 -p $Port -U $AppUser -d $Database
    } finally {
      Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
    }
  }

  'log' {
    if (Test-Path $errLog) { Get-Content $errLog -Tail 40 -Encoding UTF8 } else { Write-Host '（暂无日志）' }
    if (Test-Path $outLog) { Get-Content $outLog -Tail 10 -Encoding UTF8 }
  }
}
