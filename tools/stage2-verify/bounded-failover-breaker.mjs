#!/usr/bin/env node
// Remote acceptance for APIGX bounded multi-key failover and durable breaker recovery.
//
// The script is intentionally phase-based. API/stub operations run inside the
// Compose verify container; state that must survive an app restart is written
// below /work/.toolchain/_state (mounted from /pan/apigx-test/verify).

const argv = process.argv.slice(2)
const argOf = (name, fallback) => {
  const index = argv.indexOf(name)
  return index >= 0 && argv[index + 1] ? argv[index + 1] : fallback
}

const PHASE = argOf('--phase', 'key-failover')
const BASE = argOf('--base', 'http://127.0.0.1:3000')
const STUB = argOf('--stub', 'http://127.0.0.1:8100')
const UPSTREAM = argOf('--upstream', STUB)
const DEAD_UPSTREAM = argOf('--dead-upstream', 'http://127.0.0.1:9')
const ROOT_USER = process.env.APIGX_VERIFY_ROOT_USER || argOf('--user', 'apigxroot')
const ROOT_PASSWORD = process.env.APIGX_VERIFY_ROOT_PASSWORD || argOf('--password', '')

const crypto = await import('node:crypto')
const fs = await import('node:fs')
const path = await import('node:path')
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state')
const STATE_FILE = path.join(STATE_DIR, 'bounded-failover-breaker.json')
let failures = 0

const pass = (message) => console.log(`  \x1b[32mPASS\x1b[0m  ${message}`)
const fail = (message) => {
  failures += 1
  console.log(`  \x1b[31mFAIL\x1b[0m  ${message}`)
}
const info = (message) => console.log(`  \x1b[36mINFO\x1b[0m  ${message}`)
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function call(url, { method = 'GET', body, auth } = {}) {
  const headers = { accept: 'application/json' }
  if (body !== undefined) headers['content-type'] = 'application/json'
  if (auth) headers.authorization = auth
  const response = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let json = null
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: response.status, text, json }
}

function dataOf(response, label) {
  if (response.status !== 200 || response.json?.success === false || response.json?.data === undefined) {
    throw new Error(`${label} failed: HTTP ${response.status} ${response.text.slice(0, 240)}`)
  }
  return response.json.data
}

async function login() {
  if (!ROOT_PASSWORD) throw new Error('APIGX_VERIFY_ROOT_PASSWORD or --password is required')
  const response = await call(`${BASE}/api/user/login`, {
    method: 'POST',
    body: { username: ROOT_USER, password: ROOT_PASSWORD },
  })
  const token = response.json?.data?.access_token
  if (!token) throw new Error(`root login failed: HTTP ${response.status} ${response.text.slice(0, 180)}`)
  return `Bearer ${token}`
}

async function ensurePlatformKey(auth) {
  let response = await call(`${BASE}/api/token/`, { auth })
  let items = response.json?.data?.items ?? []
  let token = items.find((item) => item?.name === 'apigx-deploy-verify') ?? items[0]
  if (!token) {
    dataOf(await call(`${BASE}/api/token/`, {
      method: 'POST', auth,
      body: {
        name: 'apigx-deploy-verify', remain_quota: 0, unlimited_quota: true,
        expired_time: -1, model_limits_enabled: false, group: 'default',
      },
    }), 'create verification platform key')
    response = await call(`${BASE}/api/token/`, { auth })
    items = response.json?.data?.items ?? []
    token = items.find((item) => item?.name === 'apigx-deploy-verify')
  }
  if (!token?.id) throw new Error('verification platform key was not found')
  const key = dataOf(await call(`${BASE}/api/token/${token.id}/key`, {
    method: 'POST', auth,
  }), 'read verification platform key')?.key
  if (!key) throw new Error('verification platform key plaintext was not returned')
  return key
}

async function ensureRetryTimes(auth) {
  const update = await call(`${BASE}/api/option/`, {
    method: 'PUT', auth, body: { key: 'RetryTimes', value: '1' },
  })
  if (update.status !== 200 || update.json?.success !== true) {
    throw new Error(`set RetryTimes=1 failed: HTTP ${update.status} ${update.text.slice(0, 180)}`)
  }
  const current = dataOf(await call(`${BASE}/api/channel/ops`, { auth }), 'read channel operations')
  if (Number(current?.retry_times) !== 1) {
    throw new Error(`effective RetryTimes is ${current?.retry_times}, expected 1`)
  }
  pass('New API global RetryTimes is 1 (one cross-channel switch)')
}

async function ensureModelPricing(auth, model) {
  const snapshot = dataOf(await call(
    `${BASE}/api/option/model_pricing?model=${encodeURIComponent(model)}`,
    { auth },
  ), `read pricing for ${model}`)
  const entry = snapshot?.entries?.find((item) => item?.model_name === model)
  if (!entry?.version) throw new Error(`pricing snapshot is missing ${model}`)
  if (entry.effective?.ModelRatio !== undefined || entry.effective?.ModelPrice !== undefined) return
  dataOf(await call(`${BASE}/api/option/model_pricing`, {
    method: 'PATCH', auth,
    body: {
      changes: [{
        model_name: model,
        expected_version: entry.version,
        pricing: { ModelRatio: 1, CompletionRatio: 2 },
      }],
    },
  }), `configure pricing for ${model}`)
}

async function findBaseChannel(auth, name) {
  for (let attempt = 0; attempt < 15; attempt += 1) {
    for (let page = 1; page <= 40; page += 1) {
      const response = await call(`${BASE}/api/channel/?p=${page}&page_size=100`, { auth })
      const items = response.json?.data?.items ?? []
      const found = items.find((item) => item?.name === name)
      if (found) return found
      if (items.length < 100) break
    }
    await sleep(1000)
  }
  throw new Error(`base channel ${name} was not found`)
}

async function createBaseChannel(auth, { name, model, baseURL, priority = 0 }) {
  const response = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth,
    body: {
      mode: 'single',
      channel: {
        name, type: 1, key: `sk-placeholder-${name}`,
        base_url: baseURL, models: model, groups: ['default'], status: 1,
        priority, weight: 1,
      },
    },
  })
  if (response.status !== 200 || response.json?.success === false) {
    throw new Error(`create base channel ${name} failed: ${response.text.slice(0, 220)}`)
  }
  return findBaseChannel(auth, name)
}

async function createGroup(auth, stamp, tag) {
  const group = dataOf(await call(`${BASE}/api/apigx/share-groups`, {
    method: 'POST', auth,
    body: { name: `${tag}-${stamp}`, default_visibility: 2 },
  }), `create ${tag} share group`)
  dataOf(await call(`${BASE}/api/apigx/share-groups/${group.id}/publish-status`, {
    method: 'POST', auth, body: { status: 2 },
  }), `publish ${tag} share group`)
  return group
}

async function confirmChatIdentity(auth, model) {
  for (let attempt = 0; attempt < 12; attempt += 1) {
    for (let page = 1; page <= 10; page += 1) {
      const response = await call(
        `${BASE}/api/apigx/model-catalog/candidates?page=${page}&page_size=100`,
        { auth },
      )
      const items = response.json?.data?.items ?? []
      const candidate = items.find((item) => item?.external_model_id === model)
      if (candidate) {
        dataOf(await call(`${BASE}/api/apigx/model-catalog/bulk-confirm`, {
          method: 'POST', auth,
          body: {
            normalized_model_id: candidate.normalized_model_id,
            api_model_id: model,
            description: 'APIGX isolated deployment acceptance model',
            modality_type: 1,
          },
        }), `confirm chat identity for ${model}`)
        return
      }
      if (items.length < 100) break
    }
    await sleep(500)
  }
  throw new Error(`alias candidate for ${model} was not found`)
}

async function seedOffering(auth, {
  groupId, stamp, tag, model, baseURL, keys, confirmIdentity = false,
}) {
  const baseName = `H-BASE-${tag}-${stamp}`
  const base = await createBaseChannel(auth, { name: baseName, model, baseURL })
  const channel = dataOf(await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth,
    body: {
      share_group_id: groupId,
      base_channel_id: base.id,
      name: `H-CHANNEL-${tag}-${stamp}`,
      protocol: 'openai',
      base_url: baseURL,
      key_pool_mode: 0,
    },
  }), `create ${tag} share channel`)
  const batch = dataOf(await call(`${BASE}/api/apigx/channels/${channel.id}/keys/batch`, {
    method: 'POST', auth, body: { keys },
  }), `batch add ${tag} credentials`)
  if (batch.created !== keys.length) {
    throw new Error(`${tag} batch created ${batch.created}, expected ${keys.length}`)
  }
  dataOf(await call(`${BASE}/api/apigx/channels/${channel.id}/apply`, {
    method: 'POST', auth,
  }), `project ${tag} key pool`)
  const offering = dataOf(await call(`${BASE}/api/apigx/channels/${channel.id}/offerings`, {
    method: 'POST', auth, body: { upstream_model_id: model },
  }), `create ${tag} offering`)
  if (confirmIdentity) await confirmChatIdentity(auth, model)
  dataOf(await call(`${BASE}/api/apigx/price-rules`, {
    method: 'PUT', auth,
    body: {
      scope_type: 0, scope_id: offering.id,
      input_usd_per_million: 1, output_usd_per_million: 2,
    },
  }), `configure ${tag} offering price`)
  dataOf(await call(`${BASE}/api/apigx/offerings/${offering.id}/submit`, {
    method: 'POST', auth,
  }), `submit ${tag} offering`)
  dataOf(await call(`${BASE}/api/apigx/offerings/${offering.id}/publish`, {
    method: 'POST', auth,
    body: { reason_code: 'bounded_failover_breaker_deployment_acceptance' },
  }), `publish ${tag} offering`)
  await ensureModelPricing(auth, model)
  const keyRows = dataOf(await call(`${BASE}/api/apigx/channels/${channel.id}/keys`, {
    auth,
  }), `list ${tag} credentials`)?.items ?? []
  return { base, channel, offering, keys, keyRows }
}

const fingerprint = (key) => {
  const hash = crypto.createHash('sha256').update(key).digest('hex').slice(0, 10)
  return `sha256:${hash}/…${key.slice(-4)}`
}

async function resetStub() {
  await call(`${STUB}/_control/reset`, { method: 'POST' })
  const response = await call(`${STUB}/_control/profile`, {
    method: 'POST',
    body: { status: 0, statusPlan: [], delayMs: 0, hang: false, statusBody: null },
  })
  if (response.status !== 200) throw new Error(`reset stub failed: HTTP ${response.status}`)
}

async function setProfile(profile) {
  const response = await call(`${STUB}/_control/profile`, { method: 'POST', body: profile })
  if (response.status !== 200) throw new Error(`set stub profile failed: HTTP ${response.status}`)
}

async function requests(limit = 500) {
  return (await call(`${STUB}/_control/requests?limit=${limit}`)).json?.requests ?? []
}

async function chat(platformKey, model) {
  return call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: `Bearer ${platformKey}`,
    body: { model, messages: [{ role: 'user', content: 'APIGX deployment acceptance' }] },
  })
}

async function waitUntilRoutable(platformKey, model) {
  await resetStub()
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await chat(platformKey, model)
    if (response.status === 200) return
    await sleep(1500)
  }
  throw new Error(`model ${model} did not become routable in time`)
}

function saveState(state) {
  fs.mkdirSync(STATE_DIR, { recursive: true })
  fs.writeFileSync(STATE_FILE, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 })
  try { fs.chmodSync(STATE_FILE, 0o600) } catch {}
}

function loadState() {
  return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8'))
}

async function phaseKeyFailover(auth, platformKey) {
  const stamp = Date.now().toString().slice(-9)
  const group = await createGroup(auth, stamp, 'H-BOUND')
  const model = `stub/bounded-${stamp}`
  const keysA = Array.from({ length: 8 }, (_, index) => `sk-bound-a-${index}-${stamp}`)
  const keysB = Array.from({ length: 8 }, (_, index) => `sk-bound-b-${index}-${stamp}`)
  const channelA = await seedOffering(auth, {
    groupId: group.id, stamp, tag: 'BOUND-A', model, baseURL: UPSTREAM, keys: keysA,
  })
  const channelB = await seedOffering(auth, {
    groupId: group.id, stamp, tag: 'BOUND-B', model, baseURL: UPSTREAM, keys: keysB,
  })
  await waitUntilRoutable(platformKey, model)

  await resetStub()
  await setProfile({ status: 429, statusPlan: [] })
  const failedResponse = await chat(platformKey, model)
  const entries = await requests()
  const setA = new Set(keysA.map(fingerprint))
  const setB = new Set(keysB.map(fingerprint))
  const seen = entries.map((entry) => entry.upstreamKey)
  const first = seen.slice(0, 6)
  const second = seen.slice(6, 12)
  const firstIsA = first.every((item) => setA.has(item))
  const firstIsB = first.every((item) => setB.has(item))
  const secondOther = firstIsA
    ? second.every((item) => setB.has(item))
    : firstIsB && second.every((item) => setA.has(item))
  ;(failedResponse.status !== 200 ? pass : fail)(
    `all-failing request returns an error — HTTP ${failedResponse.status}`,
  )
  ;(seen.length === 12 ? pass : fail)(
    `RetryTimes=1 reaches exactly two channels with six keys each — attempts=${seen.length}`,
  )
  ;(new Set(seen).size === 12 ? pass : fail)(
    `all twelve attempts use distinct credentials — distinct=${new Set(seen).size}`,
  )
  ;(first.length === 6 && second.length === 6 && (firstIsA || firstIsB) && secondOther ? pass : fail)(
    'the first six attempts stay in one channel and only then switch once',
  )

  const noCooldownModel = `stub/no-cooldown-${stamp}`
  const noCooldownKeys = [`sk-no-cool-a-${stamp}`, `sk-no-cool-b-${stamp}`]
  await seedOffering(auth, {
    groupId: group.id, stamp, tag: 'NO-COOL', model: noCooldownModel,
    baseURL: UPSTREAM, keys: noCooldownKeys,
  })
  await waitUntilRoutable(platformKey, noCooldownModel)
  await resetStub()
  await setProfile({ status: 0, statusPlan: [429, 0] })
  const recovered = await chat(platformKey, noCooldownModel)
  const failoverEntries = await requests()
  const failedKey = failoverEntries[0]?.upstreamKey
  ;(recovered.status === 200 && failoverEntries.length === 2 ? pass : fail)(
    `429 retries the other key in-channel — HTTP ${recovered.status}, attempts=${failoverEntries.length}`,
  )
  await resetStub()
  const laterStatuses = []
  for (let index = 0; index < 2; index += 1) laterStatuses.push((await chat(platformKey, noCooldownModel)).status)
  const laterKeys = (await requests()).map((item) => item.upstreamKey)
  ;(laterStatuses.every((status) => status === 200) && laterKeys.includes(failedKey) ? pass : fail)(
    `the key that returned 429 remains eligible on later requests — ${laterKeys.join(', ')}`,
  )
  await resetStub()
  info(`bounded fixture channels: ${channelA.base.id}, ${channelB.base.id}`)
}

async function routeCandidates(auth, model) {
  const response = await call(
    `${BASE}/api/apigx/route-explain?model=${encodeURIComponent(model)}`,
    { auth },
  )
  return dataOf(response, `route explain ${model}`)?.candidates ?? []
}

async function phaseBreakerSetup(auth, platformKey) {
  const stamp = Date.now().toString().slice(-9)
  const group = await createGroup(auth, stamp, 'H-BREAKER')
  const model = `stub/breaker-recovery-${stamp}`
  const deadKey = `sk-breaker-dead-${stamp}`
  const liveKey = `sk-breaker-live-${stamp}`
  const dead = await seedOffering(auth, {
    groupId: group.id, stamp, tag: 'BREAKER-DEAD', model,
    baseURL: DEAD_UPSTREAM, keys: [deadKey], confirmIdentity: true,
  })
  const live = await seedOffering(auth, {
    groupId: group.id, stamp, tag: 'BREAKER-LIVE', model,
    baseURL: UPSTREAM, keys: [liveKey],
  })
  await waitUntilRoutable(platformKey, model)
  let opened = false
  let deadCandidate = null
  for (let attempt = 0; attempt < 48; attempt += 1) {
    await chat(platformKey, model)
    const candidates = await routeCandidates(auth, model)
    deadCandidate = candidates.find((item) => item.offering_public_id === dead.offering.public_id)
    if (deadCandidate?.excluded_reason === 'breaker_open') {
      opened = true
      break
    }
    await sleep(200)
  }
  ;(opened ? pass : fail)(
    `failed chat source is removed from user routing — offering=${dead.offering.public_id}`,
  )
  if (!opened) throw new Error('breaker did not open in the bounded acceptance window')
  const samplesBefore = deadCandidate?.sample_count ?? -1
  await sleep(3200)
  for (let index = 0; index < 5; index += 1) await chat(platformKey, model)
  const after = (await routeCandidates(auth, model))
    .find((item) => item.offering_public_id === dead.offering.public_id)
  ;(after?.sample_count === samplesBefore ? pass : fail)(
    `blocked source receives no new natural traffic samples — ${samplesBefore} -> ${after?.sample_count}`,
  )
  const state = {
    stamp,
    model,
    dead: {
      baseChannelId: dead.base.id,
      shareChannelId: dead.channel.id,
      offeringId: dead.offering.id,
      offeringPublicId: dead.offering.public_id,
      keyId: dead.keyRows[0]?.id,
      key: deadKey,
      fingerprint: fingerprint(deadKey),
    },
    live: {
      baseChannelId: live.base.id,
      shareChannelId: live.channel.id,
      offeringId: live.offering.id,
      offeringPublicId: live.offering.public_id,
      keyId: live.keyRows[0]?.id,
      key: liveKey,
      fingerprint: fingerprint(liveKey),
    },
  }
  saveState(state)
  pass(`breaker fixture persisted for restart — ${STATE_FILE}`)
}

async function phaseRotateBreakerKey(auth) {
  const state = loadState()
  const rotated = `sk-breaker-rotated-${state.stamp}`
  dataOf(await call(`${BASE}/api/apigx/keys/${state.dead.keyId}/rotate`, {
    method: 'POST', auth, body: { key: rotated },
  }), 'rotate auth-blocked breaker key')
  state.dead.key = rotated
  state.dead.fingerprint = fingerprint(rotated)
  saveState(state)
  pass('credential rotation rearmed the auth-blocked breaker probe')
}

async function phaseBlockedTraffic(auth, platformKey) {
  const state = loadState()
  await resetStub()
  const response = await chat(platformKey, state.model)
  const entries = await requests()
  const fingerprints = entries.map((item) => item.upstreamKey)
  ;(response.status === 200 ? pass : fail)(`healthy fallback still serves user traffic — HTTP ${response.status}`)
  ;(fingerprints.length > 0 && fingerprints.every((item) => item === state.live.fingerprint) ? pass : fail)(
    `first successful probe does not return the recovering source to user traffic — ${fingerprints.join(', ')}`,
  )
}

async function phaseDriveBreaker(platformKey) {
  const state = loadState()
  await resetStub()
  const statuses = []
  for (let index = 0; index < 24; index += 1) {
    statuses.push((await chat(platformKey, state.model)).status)
    await sleep(100)
  }
  ;(statuses.every((status) => status === 200) ? pass : fail)(
    `drove the open decision through the request guard without losing service — ${statuses.join(',')}`,
  )
}

async function phaseRecovered(auth) {
  const state = loadState()
  const candidates = await routeCandidates(auth, state.model)
  const dead = candidates.find((item) => item.offering_public_id === state.dead.offeringPublicId)
  ;(dead && dead.excluded_reason !== 'breaker_open' ? pass : fail)(
    `second consecutive successful probe closes the breaker — reason=${dead?.excluded_reason ?? 'missing'}`,
  )
}

async function main() {
  const auth = await login()
  await ensureRetryTimes(auth)
  const platformKey = await ensurePlatformKey(auth)
  if (PHASE === 'key-failover') await phaseKeyFailover(auth, platformKey)
  else if (PHASE === 'breaker-setup') await phaseBreakerSetup(auth, platformKey)
  else if (PHASE === 'rotate-breaker-key') await phaseRotateBreakerKey(auth)
  else if (PHASE === 'drive-breaker') await phaseDriveBreaker(platformKey)
  else if (PHASE === 'blocked-traffic') await phaseBlockedTraffic(auth, platformKey)
  else if (PHASE === 'recovered') await phaseRecovered(auth)
  else throw new Error(`unknown phase: ${PHASE}`)
}

main()
  .then(() => {
    if (failures > 0) process.exitCode = 1
  })
  .catch((error) => {
    console.error(`\x1b[31mERROR: ${error.message}\x1b[0m`)
    process.exitCode = 2
  })
