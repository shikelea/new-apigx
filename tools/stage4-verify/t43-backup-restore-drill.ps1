# T4.3 备份恢复演练（本地真实环境，不动现场库）
# 步骤：取指纹 → pg_dump → 恢复到新库 → 在新库取指纹 → 逐项比对
# 纪律：比对失败就 exit 1（不许"看起来差不多"）

$ErrorActionPreference = 'Continue'  # 外部命令的 stderr 提示（如 dropdb --if-exists）不应中止脚本；真正的断言在比对那一步
$psql = 'D:\PostgreSQL\bin\psql.exe'
$dump = 'D:\PostgreSQL\bin\pg_dump.exe'
$createdb = 'D:\PostgreSQL\bin\createdb.exe'
$dropdb = 'D:\PostgreSQL\bin\dropdb.exe'
$env:PGPASSWORD = 'apigx_dev'
$H = '127.0.0.1'; $P = '5433'; $U = 'apigx'
$SRC = 'apigx_dev'
$DST = 'apigx_restore_drill'
$backupFile = 'D:\Github\apigx\.toolchain\_state\backup-t43.dump'

# 指纹查询：覆盖计划书要求的四类可恢复对象 —— 凭据 / 权限 / 价格版本 / 历史消耗
$fingerprintSql = @'
select 'channels' k, count(*)::text v from apigx_channels
union all select 'upstream_keys', count(*)::text from apigx_upstream_keys
union all select 'upstream_key_secret_hash', md5(coalesce(string_agg(secret_ref, '' order by id), '')) from apigx_upstream_keys
union all select 'upstream_key_fp_hash', md5(coalesce(string_agg(secret_fingerprint, '' order by id), '')) from apigx_upstream_keys
union all select 'grants', count(*)::text from apigx_grants
union all select 'invites', count(*)::text from apigx_invites
union all select 'price_rules', count(*)::text from apigx_price_rules
union all select 'price_rule_items', count(*)::text from apigx_price_rule_items
union all select 'attempts', count(*)::text from apigx_attempts
union all select 'users', count(*)::text from users
union all select 'base_channels', count(*)::text from channels
union all select 'base_channel_key_hash', md5(coalesce(string_agg("key", '' order by id), '')) from channels
order by 1;
'@
$fpFile = 'D:\Github\apigx\.toolchain\_state\t43-fp.sql'
[System.IO.File]::WriteAllText($fpFile, $fingerprintSql, (New-Object System.Text.UTF8Encoding($false)))

Write-Output '=== 1) 备份前：取现场库指纹 ==='
$before = & $psql -w -h $H -p $P -U $U -d $SRC -tAf $fpFile 2>&1
$before | ForEach-Object { '  ' + $_ }

Write-Output '=== 2) 备份（pg_dump -Fc）==='
if (Test-Path $backupFile) { Remove-Item $backupFile -Force }
& $dump -w -h $H -p $P -U $U -Fc -f $backupFile $SRC 2>&1 | Select-Object -First 3
"  备份文件: $backupFile  ($([math]::Round((Get-Item $backupFile).Length/1KB,1)) KB)"

Write-Output '=== 3) 恢复到新库（先删再建，避免污染）==='
& $dropdb -w -h $H -p $P -U $U --if-exists $DST 2>&1 | Select-Object -First 2
& $createdb -w -h $H -p $P -U $U $DST 2>&1 | Select-Object -First 2
& 'D:\PostgreSQL\bin\pg_restore.exe' -w -h $H -p $P -U $U -d $DST --no-owner --no-privileges $backupFile 2>&1 | Select-Object -First 5
"  恢复完成"

Write-Output '=== 4) 恢复后：在新库取指纹并逐项比对 ==='
$after = & $psql -w -h $H -p $P -U $U -d $DST -tAf $fpFile 2>&1
$after | ForEach-Object { '  ' + $_ }

$b = @{}; $a = @{}
foreach ($line in $before) { if ($line -match '^([^|]+)\|(.*)$') { $b[$Matches[1].Trim()] = $Matches[2].Trim() } }
foreach ($line in $after)  { if ($line -match '^([^|]+)\|(.*)$') { $a[$Matches[1].Trim()] = $Matches[2].Trim() } }

$fail = 0
if ($b.Keys.Count -eq 0 -or $a.Keys.Count -eq 0) { Write-Output '❌ 指纹解析出 0 项 —— 解析或查询有问题，不能算通过'; exit 1 }
Write-Output '=== 5) 比对结果 ==='
foreach ($k in ($b.Keys | Sort-Object)) {
  $ok = ($a.ContainsKey($k)) -and ($a[$k] -eq $b[$k])
  if (-not $ok) { $fail++ }
  '  {0,-28} {1,-36} {2}' -f $k, $b[$k], $(if ($ok) { '一致 ✓' } else { "不一致 ✗ (恢复后=$($a[$k]))" })
}
Remove-Item Env:\PGPASSWORD -ErrorAction SilentlyContinue
Remove-Item $fpFile -ErrorAction SilentlyContinue
if ($fail -gt 0) { Write-Output "❌ 有 $fail 项不一致"; exit 1 }
Write-Output "✅ 全部 $($b.Keys.Count) 项一致（凭据密文/指纹、权限、价格版本、历史消耗均可恢复）"
Write-Output "   恢复库保留在 $DST 供后续步骤使用"
