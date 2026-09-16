#!/usr/bin/env node
// T1.4 验收：分享者接口的端到端流程与两条安全不变量
//
//  1. 分享者 A 能建分组 → 渠道 → 加 Key；列表/详情可见
//  2. **归属隔离**：另一个账号对 A 的资源一律 404（不是 403，避免暴露存在性）
//  3. **凭据不泄露**：加 Key 的响应只给脱敏；列表里没有明文也没有密文信封
//
// 密文落库由调用方用 psql 另行核对（脚本会打印 key id 便于核对）。
//
// 用法：node tools/stage1-verify/share-api.mjs

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const PLAIN = argOf('--secret', 'sk-e2e-PLAINTEXT-must-not-leak-1234');

let cookies = [];
async function call(url, { method = 'GET', body, auth } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (cookies.length) h.cookie = cookies.join('; ');
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const raw = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
  for (const c of raw) { const p = c.split(';')[0]; cookies = cookies.filter((x) => x.split('=')[0] !== p.split('=')[0]); cookies.push(p); }
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);

async function login(user, pass_) {
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password: pass_ } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 100)}`);
  return `Bearer ${r.json.data.access_token}`;
}

async function main() {
  const authA = await login('apigxroot', 'ApigxDev#2026');
  const authB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  // 1) A 建分组
  const stamp = Date.now().toString().slice(-6);
  let r = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: authA, body: { name: `E2E 分组 ${stamp}` } });
  check('创建分享分组', r.status === 200 && r.json?.data?.id > 0, `HTTP ${r.status} id=${r.json?.data?.id}`);
  const groupID = r.json?.data?.id;

  // 2) A 建渠道（必须绑定一条底座渠道；用一个测试用底座渠道 ID）
  const baseChannelID = Number(stamp); // 每次运行用不同值，避免唯一索引冲突
  r = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: authA,
    body: { share_group_id: groupID, base_channel_id: baseChannelID, name: `E2E 渠道 ${stamp}`, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  check('创建分享渠道', r.status === 200 && r.json?.data?.id > 0, `HTTP ${r.status} ${r.json?.message ?? ''}`);
  const channelID = r.json?.data?.id;

  // 3) A 加一把上游 Key（明文只在这一刻出现）
  r = await call(`${BASE}/api/apigx/channels/${channelID}/keys`, {
    method: 'POST', auth: authA,
    body: { stable_key_id: `e2e-${stamp}`, name: 'E2E Key', key: PLAIN },
  });
  const addBody = r.text;
  check('添加上游 Key', r.status === 200, `HTTP ${r.status} ${r.json?.message ?? ''} masked=${r.json?.data?.masked}`);
  check('添加响应不回显明文', !addBody.includes(PLAIN));
  check('添加响应不回显密文信封', !addBody.includes('enc:v1:'));
  const keyID = r.json?.data?.id;

  // 4) 列表：不给明文、不给信封
  r = await call(`${BASE}/api/apigx/channels/${channelID}/keys`, { auth: authA });
  check('Key 列表可读', r.status === 200 && r.json?.data?.items?.length === 1, `HTTP ${r.status}`);
  check('列表不含明文', !r.text.includes(PLAIN));
  check('列表不含密文信封', !r.text.includes('enc:v1:'));
  check('列表带可用性判断', r.text.includes('"usable"'), 'usable 字段存在');

  // 5) 归属隔离：B 对 A 的资源一律 404
  const probes = [
    ['读 A 的分组', 'GET', `/api/apigx/share-groups/${groupID}`],
    ['读 A 的分组下渠道', 'GET', `/api/apigx/share-groups/${groupID}/channels`],
    ['读 A 的渠道', 'GET', `/api/apigx/channels/${channelID}`],
    ['读 A 的 Key 池', 'GET', `/api/apigx/channels/${channelID}/keys`],
    ['删 A 的渠道', 'DELETE', `/api/apigx/channels/${channelID}`],
  ];
  for (const [name, method, path] of probes) {
    const res = await call(`${BASE}${path}`, { method, auth: authB });
    check(`${name} → 404`, res.status === 404, `HTTP ${res.status}`);
  }

  // 6) B 的列表里没有 A 的分组
  r = await call(`${BASE}/api/apigx/share-groups`, { auth: authB });
  const names = (r.json?.data?.items ?? []).map((x) => x.name);
  check('B 的列表不含 A 的分组', !names.some((n) => String(n).includes(stamp)), `B 有 ${names.length} 个分组`);

  // 7) 未登录被拒
  r = await call(`${BASE}/api/apigx/share-groups`);
  check('未登录访问被拒', r.status === 401, `HTTP ${r.status}`);

  info(`供 psql 核对：channel_id=${channelID} key_id=${keyID}`);
  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
