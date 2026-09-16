#!/usr/bin/env node
// T1.6 验收：APIGX 免费共享模式（usage_only）
//
// 验证四件事（全部针对本地替身上游，不产生真实费用）：
//   ① 零余额用户可调用     —— 这是「取消充值余额前置条件」的核心
//   ② 钱包完全不动         —— 只记账，不改用户余额
//   ③ 平台 Key 额度仍然生效 —— 去掉的是**用户钱包**前置，不是令牌额度闸门
//   ④ 成本仍在记账         —— usage_only 不等于「免费模型」，估算成本必须为正
//
// 用法（A/B）：
//   底座以 APIGX_USAGE_ONLY=true  启动 → node tools/stage1-verify/usage-only.mjs --expect on
//   底座以默认（不设该变量）启动     → node tools/stage1-verify/usage-only.mjs --expect off

import fs from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const ROOT_USER = 'apigxroot';
const ROOT_PASS = 'ApigxDev#2026';
const USER_B = 'apigxuserb';
const USER_B_PASS = 'ApigxB#2026';
const B_TOKEN_NAME = 'apigx-stage0-userb';
const MODEL = argOf('--model', 'stub/model-x-202609');
const EXPECT = argOf('--expect', 'on'); // on = 期望零余额可调用；off = 期望被拒

const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
const saveSessions = (s) => { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(s, null, 2)); };

let cookies = [];
function rememberCookies(res) {
  const raw = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
  for (const c of raw) { const p = c.split(';')[0]; cookies = cookies.filter((x) => x.split('=')[0] !== p.split('=')[0]); cookies.push(p); }
}
async function call(url, { method = 'GET', body, headers = {}, auth, jar = true } = {}) {
  const h = { accept: 'application/json', ...headers };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (jar && cookies.length) h.cookie = cookies.join('; ');
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  if (jar) rememberCookies(res);
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

async function login(user, pass) {
  const c = sessions[user];
  if (c?.token && c.expiresAt && Date.now() / 1000 < c.expiresAt - 120) {
    const probe = await call(`${BASE}/api/user/self`, { auth: `Bearer ${c.token}`, jar: false });
    if (probe.status === 200) { return `Bearer ${c.token}`; }
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password: pass }, jar: false });
  const t = r.json?.data?.access_token;
  if (!t) throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 120)}`);
  sessions[user] = { token: t, expiresAt: r.json?.data?.access_expires_at ?? null };
  saveSessions(sessions);
  return `Bearer ${t}`;
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function main() {
  const rootAuth = await login(ROOT_USER, ROOT_PASS);
  const authB = await login(USER_B, USER_B_PASS);
  dim(`模式期望：${EXPECT === 'on' ? 'usage_only 开启（零余额应可调用）' : '默认（零余额应被拒）'}`);

  // 找用户 B 与它的令牌
  const users = await call(`${BASE}/api/user/?page=1&page_size=100`, { auth: rootAuth });
  const uItems = users.json?.data?.items ?? users.json?.data ?? [];
  const userB = (Array.isArray(uItems) ? uItems : []).find((u) => u?.username === USER_B);
  if (!userB) throw new Error('找不到用户 B，先跑 stage0 的 acceptance.mjs（A1）');

  const tokens = await call(`${BASE}/api/token/`, { auth: authB });
  const tItems = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  let tok = (Array.isArray(tItems) ? tItems : []).find((t) => t?.name === B_TOKEN_NAME);
  if (!tok) throw new Error(`用户 B 没有名为 ${B_TOKEN_NAME} 的令牌`);
  let key = sessions[`token:${B_TOKEN_NAME}`]?.key;
  if (!key) {
    const got = await call(`${BASE}/api/token/${tok.id}/key`, { method: 'POST', auth: authB });
    key = got.json?.data?.key;
    if (key) { sessions[`token:${B_TOKEN_NAME}`] = { id: tok.id, key }; saveSessions(sessions); }
  }
  if (!key) throw new Error('取不到用户 B 的令牌明文');
  dim(`用户 B id=${userB.id}，令牌 id=${tok.id}`);

  const chat = (auth) => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth, jar: false,
    body: { model: MODEL, messages: [{ role: 'user', content: 'usage_only probe' }] },
  });

  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); ok ? pass(`${name}${detail ? ' — ' + detail : ''}`) : fail(`${name}${detail ? ' — ' + detail : ''}`); };

  // ---- 准备：令牌设为无限额度（先隔离「用户钱包」这一个变量）----
  await call(`${BASE}/api/token/`, { method: 'PUT', auth: authB, body: { id: tok.id, name: B_TOKEN_NAME, unlimited_quota: true, expired_time: -1, model_limits_enabled: false, model_limits: '' } });

  // ---- ① 用户钱包置 0 ----
  await call(`${BASE}/api/user/manage`, { method: 'POST', auth: rootAuth, body: { id: userB.id, action: 'add_quota', mode: 'override', value: 0 } });
  const q0 = (await call(`${BASE}/api/user/self`, { auth: authB })).json?.data?.quota;
  check('用户 B 钱包已置 0', q0 === 0, `quota=${q0}`);

  // ---- ② 零余额调用 ----
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const r1 = await chat(`Bearer ${key}`);
  const stub1 = (await call(`${STUB}/_control/requests?limit=5`)).json?.requests?.length ?? 0;
  info(`零余额调用 -> HTTP ${r1.status}，到达上游 ${stub1} 次${r1.status !== 200 ? '，' + (r1.json?.error?.message ?? '').slice(0, 90) : ''}`);
  if (EXPECT === 'on') {
    check('零余额用户可调用（usage_only）', r1.status === 200, `HTTP ${r1.status}`);
  } else {
    check('零余额用户被拒（默认行为）', r1.status === 403, `HTTP ${r1.status}`);
  }

  // ---- ③ 钱包是否被动过 ----
  const qAfter = (await call(`${BASE}/api/user/self`, { auth: authB })).json?.data?.quota;
  const usedAfter = (await call(`${BASE}/api/user/self`, { auth: authB })).json?.data?.used_quota;
  info(`调用后：quota=${qAfter}，used_quota=${usedAfter}`);
  if (EXPECT === 'on') {
    check('用户钱包未被扣减', qAfter === 0, `quota 仍为 ${qAfter}（usage_only 只记账）`);
  } else {
    info('默认模式下请求被拒，钱包必然未变动');
  }

  // ---- ④ 平台 Key 额度闸门仍然生效 ----
  await call(`${BASE}/api/token/`, { method: 'PUT', auth: authB, body: { id: tok.id, name: B_TOKEN_NAME, unlimited_quota: false, remain_quota: 0, expired_time: -1 } });
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const r2 = await chat(`Bearer ${key}`);
  const stub2 = (await call(`${STUB}/_control/requests?limit=5`)).json?.requests?.length ?? 0;
  // 底座对「令牌额度不足」返回 401（凭据层面），不是 403；两者都算拒绝，关键是**不能 200**。
  check('平台 Key 额度为 0 时仍被拒（令牌闸门未被 usage_only 放开）',
    r2.status === 401 || r2.status === 403,
    `HTTP ${r2.status}，到达上游 ${stub2} 次`);
  // 还原为无限额度
  await call(`${BASE}/api/token/`, { method: 'PUT', auth: authB, body: { id: tok.id, name: B_TOKEN_NAME, unlimited_quota: true, remain_quota: 0, expired_time: -1 } });

  // ---- ⑤ 成本是否仍在记账 ----
  if (EXPECT === 'on') {
    const after = await call(`${BASE}/api/token/`, { auth: authB });
    const items2 = after.json?.data?.items ?? after.json?.data ?? [];
    const mine2 = (Array.isArray(items2) ? items2 : []).find((t) => t?.id === tok.id);
    const used = mine2?.used_quota ?? 0;
    check('成本仍在记账（used_quota 增长）', used > 0, `令牌 used_quota=${used}`);
  } else {
    info('默认模式下不检查记账（请求被拒）');
  }

  // ---- 复原 ----
  await call(`${BASE}/api/user/manage`, { method: 'POST', auth: rootAuth, body: { id: userB.id, action: 'add_quota', mode: 'override', value: 1000000 } });
  dim('已复原用户 B 的额度');

  const failed = results.filter((r) => !r.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
