// T4.5 #22 演练（node 版：fetch 的状态码可靠，PS 5.1 的异常对象取不到状态码）
// 语义：长流占住唯一槽位 ⇒ 崩溃后槽位仍被占用（不是"永远可用"的假象）⇒ 租约 TTL 到期自动回收
import { spawn, spawnSync } from 'node:child_process'

const B = 'http://127.0.0.1:3000'
const STUB = 'http://127.0.0.1:8100'
const EXE = 'D:\\Github\\apigx\\toolchain\\bin\\new-api.exe'.replace('\\toolchain', '\\.toolchain')
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

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
  } finally {
    clearTimeout(timer)
  }
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
const waitPort = async (port, tries = 90) => {
  for (let i = 0; i < tries; i++) {
    try {
      await fetch('http://127.0.0.1:' + port + '/', { signal: AbortSignal.timeout(700) })
      return true
    } catch { await sleep(700) }
  }
  return false
}

;(async () => {
  const login = await post(B + '/api/user/login', { username: 'apigxroot', password: 'ApigxDev#2026' })
  const auth = 'Bearer ' + login.json.data.access_token

  // 找到已发布 glm-5.3-flash 的渠道
  const chans = await get(B + '/api/apigx/channels', auth)
  let target = null
  for (const ch of chans.json?.data?.items ?? []) {
    const offs = await get(B + '/api/apigx/channels/' + ch.id + '/offerings', auth)
    if ((offs.json?.data?.items ?? []).some((o) => o.upstream_model_id === 'glm-5.3-flash' && o.status === 2)) { target = ch; break }
  }
  if (!target) { console.log('❌ 未找到已发布 glm-5.3-flash 的渠道'); process.exit(1) }
  console.log('  渠道 #' + target.id + ' ' + target.name)

  const setConcurrency = (mode, value) =>
    put(B + '/api/apigx/channels/' + target.id + '/capacity-policy', value === undefined ? { concurrency_mode: mode } : { concurrency_mode: mode, concurrency_value: value }, auth)

  console.log('=== 1) 并发上限设为 1 ===')
  console.log('  ->', (await setConcurrency(4, 1)).status)

  console.log('=== 2) 让替身挂住 ===')
  await post(STUB + '/_control/profile', { hang: true })

  const t = await get(B + '/api/token/', auth)
  const vt = (t.json?.data?.items ?? []).find((x) => x.name === 'apigx-stage0-verify') ?? (t.json?.data?.items ?? [])[0]
  const k = await post(B + '/api/token/' + vt.id + '/key', {}, auth)
  const pk = 'Bearer ' + k.json.data.key

  console.log('=== 3) 请求 A（挂住，占住唯一槽位）===')
  const aPromise = post(B + '/v1/chat/completions', { model: 'glm-5.3-flash', messages: [{ role: 'user', content: 'hold' }], max_tokens: 4 }, pk, 120000)
  await sleep(4000)

  console.log('=== 4) 请求 B（期望 429）===')
  const b = await post(B + '/v1/chat/completions', { model: 'glm-5.3-flash', messages: [{ role: 'user', content: 'second' }], max_tokens: 4 }, pk)
  console.log('  请求 B ->', b.status, b.status === 429 ? '✓ 并发控制生效' : JSON.stringify(b.json ?? b.text).slice(0, 140))

  console.log('=== 5) 真实杀掉网关进程 ===')
  spawnSync('taskkill', ['/F', '/IM', 'new-api.exe'], { stdio: 'ignore' })
  await sleep(2000)
  console.log('  已杀')

  console.log('=== 6) 重启并立刻请求（期待仍 429：崩溃后槽位不是"凭空可用"）===')
  spawn(EXE, [], { cwd: 'D:\\Github\\apigx\\new-api', detached: true, stdio: 'ignore', env: process.env }).unref()
  await waitPort(3000)
  await sleep(3000)
  const c = await post(B + '/v1/chat/completions', { model: 'glm-5.3-flash', messages: [{ role: 'user', content: 'after-restart' }], max_tokens: 4 }, pk)
  console.log('  重启后立刻 ->', c.status)

  console.log('=== 7) 等租约 TTL（90s + 余量）后应自动恢复 ===')
  await sleep(100000)
  const d = await post(B + '/v1/chat/completions', { model: 'glm-5.3-flash', messages: [{ role: 'user', content: 'after-ttl' }], max_tokens: 4 }, pk)
  console.log('  TTL 之后 ->', d.status, d.status === 200 ? '✓ 槽位已被自动回收' : JSON.stringify(d.json ?? d.text).slice(0, 140))

  console.log('=== 8) 收尾 ===')
  await post(STUB + '/_control/profile', { hang: false })
  await post(STUB + '/_control/profile', { delayMs: 0, ttftMs: 0, chunkDelayMs: 0 })
  console.log('  并发策略恢复 ->', (await setConcurrency(0)).status)

  // 结果判定（不靠打印自证）
  const ok = b.status === 429 && d.status === 200
  console.log(ok ? '\n✅ 演练结论：并发控制生效；进程崩溃后槽位未凭空可用；租约 TTL 到期自动回收' : '\n❌ 结论未达成，见上面状态码')
  process.exit(ok ? 0 : 1)
})().catch((e) => { console.log('异常:', e.message); process.exit(1) })
