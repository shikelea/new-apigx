# 报告底座克隆相对固定提交的全部改动
#
# 用途：把 new-api/ 相对 deploy/base.lock.json 中锁定提交的差异列出来，
# 用于日常核对与上游升级前的检查。任何改动都应在
# docs/stage0/upstream-diff.md 台账中登记，本脚本负责给出「实际差异」。
#
# 用法（仓库根目录）：
#   .\tools\dev\upstream-diff.ps1
#   .\tools\dev\upstream-diff.ps1 -BasePath new-api

[CmdletBinding()]
param(
  [string]$BasePath = 'new-api',
  [string]$LockFile = 'deploy\base.lock.json'
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$lockPath = Join-Path $repoRoot $LockFile
$base = Join-Path $repoRoot $BasePath

if (-not (Test-Path $lockPath)) { throw "找不到锁定文件：$lockPath" }
if (-not (Test-Path (Join-Path $base '.git'))) {
  throw "找不到底座克隆：$base（获取步骤见 docs/stage0/base-lock.md 第 3 节）"
}

$lock = Get-Content -Raw -Encoding UTF8 $lockPath | ConvertFrom-Json
$pinned = $lock.commit
$head = (git -C $base rev-parse HEAD).Trim()

Write-Host "[apigx] 底座固定提交 : $pinned"
Write-Host "[apigx] 当前 HEAD     : $head"
if ($head -ne $pinned) {
  Write-Warning '当前 HEAD 与锁定提交不一致：底座可能被切换过分支，或存在本地提交。差异已按固定提交计算，请核对是否预期。'
}

Write-Host ''
Write-Host '--- 改动概览：相对固定提交（含工作区改动） ---'
$stat = git -C $base diff --stat $pinned
if ($stat) { $stat } else { Write-Host '（无）' }

Write-Host ''
Write-Host '--- 已跟踪文件的改动（状态 + 路径） ---'
$changed = @(git -C $base diff --name-status $pinned)
if ($changed.Count -gt 0 -and $changed[0]) {
  $changed | ForEach-Object { Write-Host ("  " + $_) }
} else {
  Write-Host '  （无）'
}

Write-Host ''
Write-Host '--- 未跟踪的新文件 ---'
$untracked = @(git -C $base ls-files --others --exclude-standard)
if ($untracked.Count -gt 0 -and $untracked[0]) {
  $untracked | ForEach-Object { Write-Host ("  ?? " + $_) }
} else {
  Write-Host '  （无）'
}

$trackedCount = @($changed | Where-Object { $_ }).Count
$untrackedCount = @($untracked | Where-Object { $_ }).Count

Write-Host ''
if ($trackedCount -eq 0 -and $untrackedCount -eq 0) {
  Write-Host "[apigx] 结论：底座与固定提交完全一致（0 处改动）。"
} else {
  Write-Host "[apigx] 结论：修改上游文件 $trackedCount 个，新增未跟踪文件 $untrackedCount 个。"
  Write-Host '[apigx] 请确认每一项都已在 docs/stage0/upstream-diff.md 登记；未登记的差异视为流程违规。'
}
