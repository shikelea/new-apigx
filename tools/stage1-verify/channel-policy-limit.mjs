#!/usr/bin/env node
// T1.7 验收：容量上限来自**策略库**（apigx_capacity_policies），而非环境变量
//
// 三个阶段（同一个渠道，只改策略）：
//   ① 策略 = 明确值 1  → 并发 3 只放行 1 个
//   ② 策略 = 明确不限  → 并发 3 全部放行（证明「明确不限」不是「未配置」）
//   ③ 策略 = 未配置    → 并发 3 全部放行（过渡行为：未配置不施加约束）
//
// 用法：node tools/stage1-verify/channel-policy-limit.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000, APIGX_CAPACITY_ENABLED=true, 且**不设**任何 *_CONCURRENCY_LIMIT)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
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
  const MODEL = 'stub/pol-' + stamp;
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  // 准备：底座渠道 + 分享渠道 + 两把 Key
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: 'POL底座-' + stamp, type: 1, key: 'placeholder', base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  const listRes = await call(`${BASE}/api/channel/?page=1&page_size=200`, { auth: root });
  const all = listRes.json?.data?.items ?? listRes.json?.data ?? [];
  const baseChannel = (Array.isArray(all) ? all : []).find((c) => c?.name === 'POL底座-' + stamp);
  if (!baseChannel?.id) throw new Error('查不到刚建的底座渠道');

  let r = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'POL分组-' + stamp } });
  if (r.status !== 200) throw new Error('建分组失败：' + r.text.slice(0, 160));
  const groupId = r.json.data.id;
  r = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: groupId, base_channel_id: baseChannel.id, name: 'POL渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  if (r.status !== 200) throw new Error('建分享渠道失败：' + r.text.slice(0, 160));
  const shareChannelId = r.json.data.id;
  for (const [stable, secret] of [['pol-a', 'sk-pol-AAAA-' + stamp], ['pol-b', 'sk-pol-BBBB-' + stamp]]) {
    const res = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
      method: 'POST', auth: root, body: { stable_key_id: stable + '-' + stamp, name: stable, key: secret },
    });
    if (res.status !== 200) throw new Error(`加 Key ${stable} 失败：${res.text.slice(0, 160)}`);
  }
  r = await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });
  check('投影到底座渠道（apply）', r.status === 200 && r.json?.data?.applied_keys === 2, `HTTP ${r.status}`);

  // 平台令牌
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  const vt = (Array.isArray(items) ? items : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;
  if (!platformKey) throw new Error('取不到令牌明文');

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'policy probe' }] },
  });

  let routable = false;
  for (let i = 0; i < 20 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((res) => setTimeout(res, 5000));
  }
  if (!routable) throw new Error('等待模型可路由超时');

  const burst = async () => {
    await call(`${STUB}/_control/reset`, { method: 'POST' });
    await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 1500, ttftMs: 0, status: 0, hang: false } });
    const out = await Promise.all(Array.from({ length: N }, () => chat().catch((e) => ({ status: 'ERR', text: e.message }))));
    await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0 } });
    return { ok: out.filter((x) => x.status === 200).length, rejected: out.filter((x) => x.status === 429).length };
  };

  const setPolicy = async (mode, value) => {
    const body = value === undefined ? { concurrency_mode: mode } : { concurrency_mode: mode, concurrency_value: value };
    const res = await call(`${BASE}/api/apigx/channels/${shareChannelId}/capacity-policy`, { method: 'PUT', auth: root, body });
    if (res.status !== 200) throw new Error(`设置策略失败：${res.status} ${res.text.slice(0, 160)}`);
    return res.json.data;
  };

  // ① 明确值 1
  const p1 = await setPolicy(4, 1);
  dim(`策略①：明确值 concurrency=${p1.concurrency_value}`);
  // 策略缓存 TTL 30s：等它过期，避免读到上一轮的空策略
  info('等待策略缓存过期（32s）...');
  await new Promise((res) => setTimeout(res, 32000));
  let res1 = await burst();
  info(`① 200×${res1.ok}，429×${res1.rejected}`);
  check('策略「明确值 1」生效：并发 3 只放行 1 个', res1.ok === 1 && res1.rejected === N - 1, `200×${res1.ok} / 429×${res1.rejected}`);

  // ② 明确不限
  await setPolicy(3);
  dim('策略②：明确不限');
  await new Promise((res) => setTimeout(res, 32000));
  let res2 = await burst();
  info(`② 200×${res2.ok}，429×${res2.rejected}`);
  check('策略「明确不限」全部放行（不等于未配置）', res2.ok === N && res2.rejected === 0, `200×${res2.ok} / 429×${res2.rejected}`);

  // ③ 未配置
  await setPolicy(0);
  dim('策略③：未配置');
  await new Promise((res) => setTimeout(res, 32000));
  let res3 = await burst();
  info(`③ 200×${res3.ok}，429×${res3.rejected}`);
  check('策略「未配置」不施加约束（过渡行为）', res3.ok === N && res3.rejected === 0, `200×${res3.ok} / 429×${res3.rejected}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
