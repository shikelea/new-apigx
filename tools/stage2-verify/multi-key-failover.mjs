#!/usr/bin/env node
// Deployed HTTP acceptance for APIGX in-channel Key selection and failover.
// It creates three isolated models: normal polling, 429->success and
// 401->success. The controllable upstream records only credential fingerprints.

const argv = process.argv.slice(2);
const argOf = (name, fallback) => {
  const index = argv.indexOf(name);
  return index >= 0 && argv[index + 1] ? argv[index + 1] : fallback;
};

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const UPSTREAM = argOf('--upstream', STUB);
const ROUNDS = Number(argOf('--rounds', '8'));
const ROOT_USER = process.env.APIGX_VERIFY_ROOT_USER || argOf('--user', 'apigxroot');
const ROOT_PASSWORD = process.env.APIGX_VERIFY_ROOT_PASSWORD || argOf('--password', '');

const crypto = await import('node:crypto');
let failures = 0;
const pass = (message) => console.log(`  \x1b[32mPASS\x1b[0m  ${message}`);
const fail = (message) => { failures += 1; console.log(`  \x1b[31mFAIL\x1b[0m  ${message}`); };
const info = (message) => console.log(`  \x1b[36mINFO\x1b[0m  ${message}`);

async function call(url, { method = 'GET', body, auth } = {}) {
  const headers = { accept: 'application/json' };
  if (body !== undefined) headers['content-type'] = 'application/json';
  if (auth) headers.authorization = auth;
  const response = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let json = null;
  try { json = JSON.parse(text); } catch {}
  return { status: response.status, text, json };
}

function dataOf(response, label) {
  if (response.status !== 200 || response.json?.success === false || !response.json?.data) {
    throw new Error(`${label}失败：HTTP ${response.status} ${response.text.slice(0, 180)}`);
  }
  return response.json.data;
}

async function login() {
  const response = await call(`${BASE}/api/user/login`, {
    method: 'POST', body: { username: ROOT_USER, password: ROOT_PASSWORD },
  });
  const token = response.json?.data?.access_token;
  if (!token) throw new Error(`管理员登录失败：HTTP ${response.status} ${response.text.slice(0, 180)}`);
  return `Bearer ${token}`;
}

async function ensurePlatformKey(auth) {
  let response = await call(`${BASE}/api/token/`, { auth });
  let items = response.json?.data?.items ?? [];
  let token = items.find((item) => item?.name === 'apigx-deploy-verify') ?? items[0];
  if (!token) {
    response = await call(`${BASE}/api/token/`, {
      method: 'POST', auth,
      body: {
        name: 'apigx-deploy-verify', remain_quota: 0, unlimited_quota: true,
        expired_time: -1, model_limits_enabled: false, group: 'default',
      },
    });
    if (response.status !== 200 || response.json?.success !== true) {
      throw new Error(`创建验收平台 Key 失败：HTTP ${response.status} ${response.text.slice(0, 180)}`);
    }
    response = await call(`${BASE}/api/token/`, { auth });
    items = response.json?.data?.items ?? [];
    token = items.find((item) => item?.name === 'apigx-deploy-verify');
  }
  if (!token?.id) throw new Error('找不到验收平台 Key');
  const keyResponse = await call(`${BASE}/api/token/${token.id}/key`, { method: 'POST', auth });
  const key = keyResponse.json?.data?.key;
  if (!key) throw new Error('无法读取验收平台 Key 明文');
  return key;
}

async function ensureModelPricing(auth, model) {
  const snapshot = dataOf(
    await call(`${BASE}/api/option/model_pricing?model=${encodeURIComponent(model)}`, { auth }),
    `读取 ${model} 的全局价格`,
  );
  const entry = snapshot?.entries?.find((item) => item?.model_name === model);
  if (!entry?.version) throw new Error(`全局价格快照缺少模型 ${model}`);
  if (entry.effective?.ModelRatio !== undefined || entry.effective?.ModelPrice !== undefined) return;
  dataOf(await call(`${BASE}/api/option/model_pricing`, {
    method: 'PATCH', auth,
    body: {
      changes: [{
        model_name: model,
        expected_version: entry.version,
        pricing: { ModelRatio: 1, CompletionRatio: 2 },
      }],
    },
  }), `配置 ${model} 的全局价格`);
}

async function findBaseChannel(auth, name) {
  for (let attempt = 0; attempt < 12; attempt += 1) {
    const merged = [];
    for (let page = 1; page <= 20; page += 1) {
      const response = await call(`${BASE}/api/channel/?p=${page}&page_size=100`, { auth });
      const items = response.json?.data?.items ?? [];
      merged.push(...items);
      if (items.length < 100) break;
    }
    const found = merged.find((item) => item?.name === name);
    if (found) return found;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error(`查不到底座渠道 ${name}`);
}

async function seed(auth, groupId, stamp, tag) {
  const model = `stub/failover-${tag}-${stamp}`;
  const baseName = `FAIL-${tag}-${stamp}`;
  const created = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth,
    body: {
      mode: 'single',
      channel: {
        name: baseName, type: 1, key: `sk-placeholder-${tag}-${stamp}`,
        base_url: UPSTREAM, models: model, groups: ['default'], status: 1,
        priority: 0, weight: 1,
      },
    },
  });
  if (created.status !== 200 || created.json?.success === false) {
    throw new Error(`创建底座渠道 ${tag} 失败：${created.text.slice(0, 180)}`);
  }
  const baseChannel = await findBaseChannel(auth, baseName);
  const channel = dataOf(await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth,
    body: {
      share_group_id: groupId, base_channel_id: baseChannel.id,
      name: `FAIL渠道-${tag}-${stamp}`, protocol: 'openai', base_url: UPSTREAM,
      key_pool_mode: 0,
    },
  }), `创建分享渠道 ${tag}`);

  const keys = [`sk-${tag}-A-${stamp}`, `sk-${tag}-B-${stamp}`];
  for (const [index, key] of keys.entries()) {
    const response = await call(`${BASE}/api/apigx/channels/${channel.id}/keys`, {
      method: 'POST', auth,
      body: { stable_key_id: `${tag}-${index}-${stamp}`, name: `${tag}-${index}`, key, weight: 1 },
    });
    dataOf(response, `添加 ${tag} Key ${index + 1}`);
  }
  dataOf(await call(`${BASE}/api/apigx/channels/${channel.id}/apply`, { method: 'POST', auth }), `投影 ${tag}`);
  const offering = dataOf(await call(`${BASE}/api/apigx/channels/${channel.id}/offerings`, {
    method: 'POST', auth, body: { upstream_model_id: model },
  }), `创建 ${tag} 供给`);
  dataOf(await call(`${BASE}/api/apigx/price-rules`, {
    method: 'PUT', auth,
    body: { scope_type: 0, scope_id: offering.id, input_usd_per_million: 1, output_usd_per_million: 2 },
  }), `配置 ${tag} 价格`);
  dataOf(await call(`${BASE}/api/apigx/offerings/${offering.id}/submit`, { method: 'POST', auth }), `提交 ${tag} 供给`);
  dataOf(await call(`${BASE}/api/apigx/offerings/${offering.id}/publish`, {
    method: 'POST', auth, body: { reason_code: 'deployed_multi_key_failover_acceptance' },
  }), `发布 ${tag} 供给`);
  await ensureModelPricing(auth, model);
  return { model, keys };
}

const fingerprint = (key) => {
  const hash = crypto.createHash('sha256').update(key).digest('hex').slice(0, 10);
  return `sha256:${hash}/…${key.slice(-4)}`;
};

async function requests() {
  return (await call(`${STUB}/_control/requests?limit=200`)).json?.requests ?? [];
}

async function setProfile(profile) {
  const response = await call(`${STUB}/_control/profile`, { method: 'POST', body: profile });
  if (response.status !== 200) throw new Error(`设置替身故障计划失败：HTTP ${response.status}`);
}

async function resetStub() {
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  await setProfile({ status: 0, statusPlan: [], delayMs: 0, hang: false, statusBody: null });
}

async function chat(platformKey, model) {
  return call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: `Bearer ${platformKey}`,
    body: { model, messages: [{ role: 'user', content: 'deployed routing acceptance' }] },
  });
}

async function waitUntilRoutable(platformKey, model) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const response = await chat(platformKey, model);
    if (response.status === 200) return;
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  throw new Error(`模型在等待窗口内仍不可路由：${model}`);
}

async function verifyFailover(platformKey, fixture, status) {
  await resetStub();
  await setProfile({ status: 0, statusPlan: [status, 0] });
  const response = await chat(platformKey, fixture.model);
  const entries = await requests();
  const expected = new Set(fixture.keys.map(fingerprint));
  const seen = entries.filter((entry) => expected.has(entry.upstreamKey)).map((entry) => entry.upstreamKey);
  const label = `${status} 后同渠道换 Key`;
  (response.status === 200 ? pass : fail)(`${label}后请求成功 — HTTP ${response.status}`);
  (seen.length === 2 && new Set(seen).size === 2 ? pass : fail)(`${label}且不重复失败 Key — attempts=${seen.length}, distinct=${new Set(seen).size}`);
}

async function main() {
  if (!ROOT_PASSWORD) throw new Error('请通过 APIGX_VERIFY_ROOT_PASSWORD 或 --password 提供验收管理员密码');
  const auth = await login();
  const platformKey = await ensurePlatformKey(auth);
  const stamp = Date.now().toString().slice(-7);
  const group = dataOf(await call(`${BASE}/api/apigx/share-groups`, {
    method: 'POST', auth, body: { name: `FAIL组-${stamp}`, default_visibility: 2 },
  }), '创建故障切换分组');
  dataOf(await call(`${BASE}/api/apigx/share-groups/${group.id}/publish-status`, {
    method: 'POST', auth, body: { status: 2 },
  }), '发布故障切换分组');

  const normal = await seed(auth, group.id, stamp, 'poll');
  const rateLimited = await seed(auth, group.id, stamp, 'rate');
  const unauthorized = await seed(auth, group.id, stamp, 'auth');
  await resetStub();
  await waitUntilRoutable(platformKey, normal.model);
  await waitUntilRoutable(platformKey, rateLimited.model);
  await waitUntilRoutable(platformKey, unauthorized.model);

  await resetStub();
  const statuses = [];
  for (let index = 0; index < ROUNDS; index += 1) {
    statuses.push((await chat(platformKey, normal.model)).status);
  }
  const entries = await requests();
  const expected = new Set(normal.keys.map(fingerprint));
  const counts = new Map([...expected].map((key) => [key, 0]));
  for (const entry of entries) {
    if (counts.has(entry.upstreamKey)) counts.set(entry.upstreamKey, counts.get(entry.upstreamKey) + 1);
  }
  const values = [...counts.values()];
  (statuses.every((status) => status === 200) ? pass : fail)(`轮询请求全部成功 — ${statuses.filter((status) => status === 200).length}/${ROUNDS}`);
  (values.every((count) => count > 0) && Math.max(...values) - Math.min(...values) <= 1 ? pass : fail)(`等权轮询覆盖两把 Key 且分布均衡 — ${values.join(':')}`);

  await verifyFailover(platformKey, rateLimited, 429);
  await verifyFailover(platformKey, unauthorized, 401);
  await resetStub();
  info(`验收模型：${normal.model}, ${rateLimited.model}, ${unauthorized.model}`);
}

main()
  .then(() => { if (failures > 0) process.exitCode = 1; })
  .catch((error) => { console.error(`\x1b[31m异常：${error.message}\x1b[0m`); process.exitCode = 2; });
