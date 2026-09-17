#!/usr/bin/env node
// T1.7 验收：**共享上游账户池**的并发上限（防容量叠加）
//
// 场景：同一个分享渠道下有 **2 把不同的上游 Key**，但它们属于**同一个上游账户池**。
// 若只按 Key 计数，两把 Key 会各占一份容量 → 实际把上游账户的并发放大一倍。
// 本脚本验证：账户池维度生效时，**即使换用另一把 Key，也仍然受池子上限约束**。
//
// 用法：node tools/stage1-verify/account-pool-limit.mjs
// 前置：PG(5433) + Redis(6379) + 替身(8100) + 网关(3000)，
//       且网关启用容量控制：APIGX_CAPACITY_ENABLED=true
//                              APIGX_UPSTREAM_ACCOUNT_CONCURRENCY_LIMIT=1

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const MODEL_ARG = argOf('--model', '');
const ACCOUNT_ID = Number(argOf('--account-id', '777001'));
const N = Number(argOf('--concurrency', '3'));

async function call(url, { method = 'GET', body, auth } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function login() {
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: 'apigxroot', password: 'ApigxDev#2026' } });
  if (!r.json?.data?.access_token) throw new Error('登录失败：HTTP ' + r.status);
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const root = await login();
  const stamp = Date.now().toString().slice(-6);
  const MODEL = MODEL_ARG || ('stub/pool-' + stamp);
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  dim('本次独占模型：' + MODEL);

  // 1) 底座影子渠道
  const created = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: 'POOL底座-' + stamp, type: 1, key: 'placeholder', base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  if (created.status !== 200) throw new Error('建底座渠道失败：' + created.text.slice(0, 160));
  const channelName = 'POOL底座-' + stamp;
  const listRes = await call(`${BASE}/api/channel/?page=1&page_size=200`, { auth: root });
  const all = listRes.json?.data?.items ?? listRes.json?.data ?? [];
  const baseChannel = (Array.isArray(all) ? all : []).find((c) => c?.name === channelName);
  if (!baseChannel?.id) throw new Error('查不到刚建的底座渠道');
  dim(`底座渠道 id=${baseChannel.id}`);

  // 2) 分享分组 + 渠道
  let r = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'POOL分组-' + stamp } });
  if (r.status !== 200) throw new Error('建分组失败：' + r.text.slice(0, 160));
  const groupId = r.json.data.id;
  r = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: groupId, base_channel_id: baseChannel.id, name: 'POOL渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  if (r.status !== 200) throw new Error('建分享渠道失败：' + r.text.slice(0, 160));
  const shareChannelId = r.json.data.id;
  dim(`分享渠道 id=${shareChannelId}`);

  // 3) 两把**不同**的 Key，但声明属于**同一个上游账户池**
  for (const [stable, secret] of [['pool-a', 'sk-pool-AAAA-' + stamp], ['pool-b', 'sk-pool-BBBB-' + stamp]]) {
    const res = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
      method: 'POST', auth: root,
      body: { stable_key_id: stable + '-' + stamp, name: stable, key: secret, upstream_account_id: ACCOUNT_ID },
    });
    if (res.status !== 200) throw new Error(`加 Key ${stable} 失败：${res.status} ${res.text.slice(0, 160)}`);
  }
  r = await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });
  check('投影到底座渠道（apply）', r.status === 200 && r.json?.data?.applied_keys === 2, `HTTP ${r.status} applied=${r.json?.data?.applied_keys}`);

  // 4) 平台令牌
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  const verifyToken = (Array.isArray(items) ? items : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  if (!verifyToken) throw new Error('找不到可用令牌');
  const keyRes = await call(`${BASE}/api/token/${verifyToken.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;
  if (!platformKey) throw new Error('取不到令牌明文');

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'account pool probe' }] },
  });

  // 5) 等新模型进入网关路由缓存（能力行已入库，只是缓存需同步）
  let routable = false;
  for (let i = 0; i < 20 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((res) => setTimeout(res, 5000));
  }
  if (!routable) throw new Error('等待模型可路由超时');
  dim('新模型已进入网关路由缓存');

  // 6) 并发打请求：池子上限 = 1，因此**即使有两把 Key 也只能有一个在飞**
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 1500, ttftMs: 0, status: 0, hang: false } });

  const burst = await Promise.all(Array.from({ length: N }, () => chat().catch((e) => ({ status: 'ERR', text: e.message }))));
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0 } });

  const okCount = burst.filter((x) => x.status === 200).length;
  const rejected = burst.filter((x) => x.status === 429).length;
  const stubHits = (await call(`${STUB}/_control/requests?limit=50`)).json?.requests?.length ?? 0;
  info(`状态分布：200×${okCount}，429×${rejected}；到达上游 ${stubHits} 次`);

  check('账户池上限生效：并发 3 只放行 1 个（两把 Key 也不能叠加）', okCount === 1, `通过 ${okCount} 个`);
  check('其余请求被 429 拒绝', rejected === N - 1, `被拒 ${rejected} 个`);
  check('被拒请求未到达上游', stubHits <= 1, `上游收到 ${stubHits} 次`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
