#!/usr/bin/env node
// 只做一件事：登录底座 -> 取平台令牌明文 -> 发一条对话请求 -> 回查替身取证。
// 用于反复验证「底座 → 替身上游」这条最小路径（渠道刚建时底座缓存默认 60s 才同步）。
//
// 用法：
//   node tools/stage0-verify/chat-probe.mjs
//   node tools/stage0-verify/chat-probe.mjs --model stub/model-x-202609 --wait 65

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const USER = argOf('--user', 'apigxroot');
const PASS = argOf('--pass', 'ApigxDev#2026');
const MODEL = argOf('--model', 'stub/model-x-202609');
const TOKEN_NAME = argOf('--token-name', 'apigx-stage0-verify');
const WAIT_S = Number(argOf('--wait', '0'));

let cookies = [];
function rememberCookies(res) {
  const raw = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
  for (const c of raw) {
    const pair = c.split(';')[0];
    cookies = cookies.filter((x) => x.split('=')[0] !== pair.split('=')[0]);
    cookies.push(pair);
  }
}
async function call(url, { method = 'GET', body, headers = {}, auth } = {}) {
  const h = { accept: 'application/json', ...headers };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (cookies.length) h.cookie = cookies.join('; ');
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  rememberCookies(res);
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

const sleep = (s) => new Promise((r) => setTimeout(r, s * 1000));

(async () => {
  const login = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: USER, password: PASS } });
  const dashAuth = `Bearer ${login.json?.data?.access_token}`;
  if (!dashAuth.endsWith('undefined')) console.log('登录 OK');

  const list = await call(`${BASE}/api/token/`, { auth: dashAuth });
  const items = list.json?.data?.items ?? list.json?.data ?? [];
  const mine = (Array.isArray(items) ? items : []).find((t) => t?.name === TOKEN_NAME);
  if (!mine) throw new Error(`找不到名为 ${TOKEN_NAME} 的令牌，先运行 wire-base.mjs`);
  const got = await call(`${BASE}/api/token/${mine.id}/key`, { method: 'POST', auth: dashAuth });
  const tokenKey = got.json?.data?.key;
  if (!tokenKey) throw new Error('取不到令牌明文');
  console.log(`令牌 id=${mine.id} key=${tokenKey.slice(0, 8)}…`);

  if (WAIT_S > 0) {
    console.log(`等待 ${WAIT_S}s 让底座同步渠道缓存…`);
    await sleep(WAIT_S);
  }

  await call(`${STUB}/_control/reset`, { method: 'POST' });

  const chat = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST',
    auth: `Bearer ${tokenKey}`,
    headers: { 'x-apigx-offering': 'offering_should_be_stripped', 'x-apigx-route': 'latency' },
    body: { model: MODEL, messages: [{ role: 'user', content: 'stage0 ping' }] },
  });
  console.log(`\nPOST /v1/chat/completions -> ${chat.status}`);
  console.log(chat.text.slice(0, 400));

  const rec = await call(`${STUB}/_control/requests?limit=5`);
  const reqs = rec.json?.requests ?? [];
  console.log(`\n替身收到 ${reqs.length} 条请求`);
  for (const q of reqs) {
    console.log('  ' + JSON.stringify({ path: q.path, upstreamKey: q.upstreamKey, platformHeaders: q.platformHeaders, body: (q.body || '').slice(0, 140) }));
  }
  process.exitCode = chat.status === 200 && reqs.length > 0 ? 0 : 1;
})().catch((e) => { console.error('异常：' + e.message); process.exitCode = 2; });
