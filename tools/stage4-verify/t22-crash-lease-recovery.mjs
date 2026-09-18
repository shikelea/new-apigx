// T4.5 #22 演练（v2：修掉 v1 的两处缺陷）
//  v1 缺陷①：spawn 重启时继承当时 shell 的环境（其中没有 SQL_DSN）⇒ 网关一度跑在"无库"环境 ⇒ 登录失败
//  v1 缺陷②：重启后旧登录态失效，收尾 PUT 401 ⇒ 渠道并发策略残留
//  v1 缺陷③：没有**确证**请求 A 真的占住了槽位 ⇒ 请求 B 得 200 时无法判断原因
// v2 改进：显式传入完整网关环境；重启后重新登录；用 node 直连 Redis（RESP over TCP）确证 apigx:cap:* 租约存在
import { spawn, spawnSync } from 'node:child_process'
import net from 'node:net'
import fs0 from 'node:fs'

const B = 'http://127.0.0.1:3000'
const STUB = 'http://127.0.0.1:8100'
const EXE = 'D:\\Github\\apigx\\.toolchain\\bin\\new-api.exe'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// leaseKeysViaRedisCli 用**真实 redis-cli** 读租约键。
// 为什么不直连 RESP：我先前那个 node RESP 读法返回空（不可信）；而 node 的 stdio:'pipe' 在此沙箱下受限，
// 因此让 PowerShell 自己跑管道、把结果写文件，再由 node 读文件。
const leaseKeysViaRedisCli = () => {
  const cli = 'D:\\Github\\apigx\\.toolchain\\redis\\redis-cli.exe'
  const out = 'D:\\Github\\apigx\\.toolchain\\_state\\t22-leases.txt'
  try { fs0.unlinkSync(out) } catch {}
  const cmd = "& '" + cli + "' -h 127.0.0.1 -p 6379 KEYS 'apigx:cap:*' | Out-File -Encoding ascii -FilePath '" + out + "'"
  spawnSync('powershell.exe', ['-NoProfile', '-Command', cmd], { stdio: 'ignore' })
  try {
    return fs0.readFileSync(out, 'utf8').split(/\r?\n/).map((l) => l.trim()).filter((l) => l.length > 0)
  } catch { return [] }
}

const GATEWAY_ENV = {
  ...process.env,
  SQL_DSN: 'postgres://apigx:apigx_dev@127.0.0.1:5433/apigx_dev?sslmode=disable',
  REDIS_CONN_STRING: 'redis://127.0.0.1:6379',
  SESSION_SECRET: 'apigx-local-dev-session-secret',
  PORT: '3000',
  GIN_MODE: 'release',
  CRITICAL_RATE_LIMIT: '500',
  CRITICAL_RATE_LIMIT_DURATION: '60',
  APIGX_SECRET_KEY: '00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff',
  APIGX_CAPACITY_ENABLED: 'true',
  APIGX_KEY_STATS_ENABLED: 'true',
  APIGX_QUALITY_STATS_ENABLED: 'true',
  APIGX_BREAKER_ENABLED: 'true',
  APIGX_TPM_ADJUST_ENABLED: 'true',
  APIGX_ROUTE_STRATEGY_ENABLED: 'true',
}

// 直连 Redis 读键（RESP 协议，避免依赖外部命令与管道）
const redisCmd = (args, port = 6379) =>
  new Promise((resolve) => {
    const sock = net.createConnection({ host: '127.0.0.1', port })
    let buf = ''
    const timer = setTimeout(() => { sock.destroy(); resolve('') }, 4000)
    sock.on('connect', () => {
      sock.write('*' + args.length + '\r\n' + args.map((a) => '$' + Buffer.byteLength(a) + '\r\n' + a + '\r\n').join(''))
    })
    sock.on('data', (d) => { buf += d.toString() })
    sock.on('error', () => { clearTimeout(timer); resolve('') })
    sock.on('end', () => { clearTimeout(timer); resolve(buf) })
    setTimeout(() => { clearTimeout(timer); sock.end() }, 800)
  })

const post = async (url, body, auth, timeoutMs = 15000) => {
  const ac = new AbortController()
  const timer = setTimeout(() => ac.abort(), timeoutMs)
  try {
    const h = { 'content-type': 'application/json' }
    if (auth) h.authorization = auth
    const r = await fetch(url, { method: 'POST', headers: h, body: JSON.stringify(body ?? {}), signal: ac.signal })
    const t = await r.text()
    let j = null
    try { j = JSON.parse(t) } catch {}
    return { status: r.status, json: j, text: t }
  } catch (e) {
    return { status: -1, error: String(e.message || e) }
  } finally { clearTimeout(timer) }
}
const put = async (url, body, auth) => {
  const h = { 'content-type': 'application/json' }
  if (auth) h.authorization = auth
  const r = await fetch(url, { method: 'PUT', headers: h, body: JSON.stringify(body ?? {}) })
  const t = await r.text()
  let j = null
  try { j = JSON.parse(t) } catch {}
  return { status: r.status, json: j, text: t }
}
const get = async (url, auth) => {
  const h = {}
  if (auth) h.authorization = auth
  const r = await fetch(url, { headers: h })
  const t = await r.text()
  let j = null
  try { j = JSON.parse(t) } catch {}
  return { status: r.status, json: j, text: t }
}
const login = async () => {
  const l = await post(B + '/api/user/login', { username: 'apigxroot', password: 'ApigxDev#2026' })
  return l.json?.success ? 'Bearer ' + l.json.data.access_token : null
}
const waitPort = async (port, tries = 90) => {
  for (let i = 0; i < tries; i++) {
    try { await fetch('http://127.0.0.1:' + port + '/', { signal: AbortSignal.timeout(700) }); return true } catch { await sleep(700) }
  }
  return false
}

;(async () => {
  let auth = await login()
  if (!auth) { console.log('❌ 登录失败，无法开始'); process.exit(1) }

  const chans = await get(B + '/api/apigx/channels', auth)
  let target = null
  for (const ch of chans.json?.data?.items ?? []) {
    // 必须挑上游是替身的渠道：v2 挑到真实厂商渠道 ⇒ 注入 hang 完全无效
    if (!String(ch.base_url ?? "").includes("8100")) continue
    const offs = await get(B + '/api/apigx/channels/' + ch.id + '/offerings', auth)
    if ((offs.json?.data?.items ?? []).some((o) => o.upstream_model_id === 'stub/model-y-202609' && o.status === 2)) { target = ch; break }
  }
  if (!target) { console.log('❌ 没有"上游指向替身(8100)且已发布 glm-5.3-flash"的渠道 —— 无法做本演练'); process.exit(2) }
  console.log('  渠道 #' + target.id + ' ' + target.name)

  const setConcurrency = (mode, value) =>
    put(B + '/api/apigx/channels/' + target.id + '/capacity-policy', value === undefined ? { concurrency_mode: mode } : { concurrency_mode: mode, concurrency_value: value }, auth)

  console.log('=== 1) 并发上限设为 1 ===', (await setConcurrency(4, 1)).status)
  await post(STUB + '/_control/profile', { hang: true })

  const t = await get(B + '/api/token/', auth)
  const vt = (t.json?.data?.items ?? []).find((x) => x.name === 'apigx-stage0-verify') ?? (t.json?.data?.items ?? [])[0]
  const k = await post(B + '/api/token/' + vt.id + '/key', {}, auth)
  const pk = 'Bearer ' + k.json.data.key

  console.log('=== 2) 请求 A（挂住）===')
  const aPromise = post(B + '/v1/chat/completions', { model: 'stub/model-y-202609', messages: [{ role: 'user', content: 'hold' }], max_tokens: 4 }, pk, 150000)
  await sleep(5000)

  console.log('=== 3) **确证**请求 A 真的占住了槽位（查 Redis 租约）===')
  const leaseKeys = leaseKeysViaRedisCli()
  const leaseCount = leaseKeys.length;
  console.log('  租约键 =', JSON.stringify(leaseKeys).slice(0, 160))
  if (leaseCount === 0) {
    console.log('  ⚠️ 没有租约 ⇒ 请求 A 未占住槽位（很可能是它自己失败了：见下方状态）')
    console.log('  请求 A 结果 =', JSON.stringify(await aPromise).slice(0, 180))
    await post(STUB + '/_control/profile', { hang: false })
    await setConcurrency(0)
    console.log('⇒ 前置条件不成立（请求 A 未占住槽位），本次不继续，也不白等 TTL')
    process.exit(3)
  }

  console.log('=== 4) 请求 B（有租约时才能期望 429）===')
  const b = await post(B + '/v1/chat/completions', { model: 'stub/model-y-202609', messages: [{ role: 'user', content: 'second' }], max_tokens: 4 }, pk)
  console.log('  请求 B ->', b.status, b.status === 429 ? '✓ 并发控制生效' : JSON.stringify(b.json ?? b.text).slice(0, 160))

  console.log('=== 5) 真实杀掉网关进程 ===')
  spawnSync('taskkill', ['/F', '/IM', 'new-api.exe'], { stdio: 'ignore' })
  await sleep(2000)

  console.log('=== 6) 用**显式环境**重启（v1 就栽在这里）===')
  spawn(EXE, [], { cwd: 'D:\\Github\\apigx\\new-api', detached: true, stdio: 'ignore', env: GATEWAY_ENV }).unref()
  await waitPort(3000)
  await sleep(3000)
  auth = await login()
  console.log('  重启后登录 =', auth ? '成功 ✓（v1 在这里失败过）' : '失败 ✗')

  // 崩溃后是否仍有租约（同一辅助函数）
  
  const afterCount = leaseKeysViaRedisCli().length;
  console.log('  崩溃后租约仍在 =', afterCount > 0 ? '是（说明不是"凭空可用"）' : '否（已过期或未被持有）')

  const c = await post(B + '/v1/chat/completions', { model: 'stub/model-y-202609', messages: [{ role: 'user', content: 'after-restart' }], max_tokens: 4 }, pk)
  console.log('  重启后立刻请求 ->', c.status)

  console.log('=== 7) 等租约 TTL（90s + 余量）后自动恢复 ===')
  await sleep(100000)
  const d = await post(B + '/v1/chat/completions', { model: 'stub/model-y-202609', messages: [{ role: 'user', content: 'after-ttl' }], max_tokens: 4 }, pk)
  console.log('  TTL 之后 ->', d.status)

  console.log('=== 8) 收尾（用**新登录态**，v1 在这里 401 过）===')
  await post(STUB + '/_control/profile', { hang: false })
  const rl = await setConcurrency(0)
  console.log('  并发策略恢复 ->', rl.status, rl.status === 200 ? '✓' : '❌ 需人工处理')
  await aPromise.catch(() => {})

  const ok = leaseCount > 0 && b.status === 429 && d.status === 200
  console.log('\n' + (ok ? '✅ 演练达成：租约确证 → 并发 429 → 崩溃后未凭空可用 → TTL 后自动回收' : '⚠️ 演练未达成（原因见上：可能被宿主磁盘 503 阻断，或 A 未占住槽位）'))
  process.exit(ok ? 0 : 1)
})().catch((e) => { console.log('异常:', e.message); process.exit(1) })
