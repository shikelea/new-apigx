# Verify (and optionally fix) the UTF-8 BOM on every .ps1 file in this repo.
#
# Why: this project's shell is Windows PowerShell 5.1, which decodes BOM-less
# UTF-8 files as ANSI/GBK. Chinese comments then swallow the following newline,
# so the next line of code ends up inside a comment and silently never runs.
# See docs/dev-environment.md section 3.
#
# Usage (from the repository root):
#   .\tools\dev\check-ps1-bom.ps1        # report only; exit 1 when a BOM is missing
#   .\tools\dev\check-ps1-bom.ps1 -Fix   # add the missing BOM in place
#
# Note: editing tools that rewrite a file drop the BOM, so re-run this after any
# tool-based edit of a .ps1 file.

[CmdletBinding()]
param(
  [switch]$Fix,
  [string]$Root = (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
)

$utf8Bom = New-Object System.Text.UTF8Encoding($true)
$bad = 0

Get-ChildItem -Path $Root -Recurse -Filter '*.ps1' -File -ErrorAction SilentlyContinue |
  Where-Object { $_.FullName -notmatch '\\\.toolchain\\' } |
  ForEach-Object {
    $bytes = [System.IO.File]::ReadAllBytes($_.FullName)
    $hasBom = $bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
    $rel = $_.FullName.Substring($Root.Length).TrimStart('\')

    if ($hasBom) {
      Write-Host ("  OK      " + $rel)
    }
    elseif ($Fix) {
      $text = Get-Content -Raw -Encoding UTF8 $_.FullName
      [System.IO.File]::WriteAllText($_.FullName, $text, $utf8Bom)
      Write-Host ("  FIXED   " + $rel)
    }
    else {
      $bad++
      Write-Host ("  NO BOM  " + $rel)
    }
  }

if ($bad -gt 0) {
  Write-Host "$bad file(s) missing a UTF-8 BOM. Re-run with -Fix."
  exit 1
}

Write-Host 'all .ps1 files have a UTF-8 BOM'
