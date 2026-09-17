#!/usr/bin/env node
// TD-08 复现与回归：指定来源（pin）是否绕过平台的「允许模型」硬限制。
//
// 背景：底座 `middleware/distributor.go` 的 pin 分支（`:48-78`）只校验渠道存在/启用/过滤器，
// 而 token 模型限制校验只在非 pin 分支（`:79-99`）。因此「固定来源」请求可以绕过允许模型限制。
//
// 本脚本做三件事（可重复执行，修复前后都能跑）：
//   A 基线：不带 pin，请求**不在允许列表里的模型** → 期望 403（证明限制本身有效）
//   B 漏洞：带 pin（指向提供该模型的渠道），请求同一个模型 → 修复前会 200（绕过），修复后应 403
//   C 反例：带 pin，请求**在允许列表里的模型** → 期望 200（证明修复没有把正常用法也拦掉）
//
// 用法：node tools/stage1-verify/td08-pin-model-limit.mjs

import fs from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const USER = 'apigxroot';
const PASS = 'ApigxDev#2026';
const TOKEN_NAME = 'apigx-stage0-verify';

const ALLOWED_MODEL = 'stub/model-x-202609';      // 允许列表里
const DISALLOWED_MODEL = 'stub/pin-model-202609'; // 不在允许列表里，但有渠道提供

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

async function login() {
  const c = sessions[USER];
  if (c?.token && c.expiresAt && Date.now() / 1000 < c.expiresAt - 120) {
    const probe = await call(`${BASE}/api/user/self`, { auth: `Bearer ${c.token}`, jar: false });
    if (probe.status === 200) return `Bearer ${c.token}`;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: USER, password: PASS }, jar: false });
  const t = r.json?.data?.access_token;
  if (!t) throw new Error(`登录失败 HTTP ${r.status} ${r.text.slice(0, 120)}`);
  sessions[USER] = { token: t, expiresAt: r.json?.data?.access_expires_at ?? null };
  saveSessions(sessions);
  return `Bearer ${t}`;
}

const line = (s) => console.log(s);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');
const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);

async function chat(model, auth) {
  return call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth, jar: false,
    body: { model, messages: [{ role: 'user', content: 'td08 probe' }] },
  });
}

async function main() {
  const dashAuth = await login();
  dim('root 登录 OK');

  // 找到平台令牌
  const list = await call(`${BASE}/api/token/`, { auth: dashAuth });
  const items = list.json?.data?.items ?? list.json?.data ?? [];
  const tk = (Array.isArray(items) ? items : []).find((t) => t?.name === TOKEN_NAME);
  if (!tk) throw new Error(`找不到令牌 ${TOKEN_NAME}`);
  let key = sessions[`token:${TOKEN_NAME}`]?.key;
  if (!key) {
    const got = await call(`${BASE}/api/token/${tk.id}/key`, { method: 'POST', auth: dashAuth });
    key = got.json?.data?.key;
    if (key) { sessions[`token:${TOKEN_NAME}`] = { id: tk.id, key }; saveSessions(sessions); }
  }
  if (!key) throw new Error('取不到令牌明文');
  dim(`令牌 id=${tk.id} key=${key.slice(0, 8)}…`);

  // 找渠道：谁提供 DISALLOWED_MODEL，谁提供 ALLOWED_MODEL
  const chList = await call(`${BASE}/api/channel/?p=1&page_size=100`, { auth: dashAuth });
  const chs = chList.json?.data?.items ?? chList.json?.data ?? [];
  const findCh = (m) => (Array.isArray(chs) ? chs : []).find((c) => String(c?.models || '').split(',').map((x) => x.trim()).includes(m));
  const chDisallowed = findCh(DISALLOWED_MODEL);
  const chAllowed = findCh(ALLOWED_MODEL);
  if (!chDisallowed || !chAllowed) throw new Error('找不到测试渠道，先跑 tools/stage0-verify/wire-base.mjs 与 acceptance.mjs（A2）');
  dim(`渠道：${DISALLOWED_MODEL} → id=${chDisallowed.id}（${chDisallowed.name}）；${ALLOWED_MODEL} → id=${chAllowed.id}（${chAllowed.name}）`);

  // 给令牌加上「只允许 ALLOWED_MODEL」的限制
  const set = await call(`${BASE}/api/token/`, {
    method: 'PUT', auth: dashAuth,
    body: {
      id: tk.id, name: TOKEN_NAME, unlimited_quota: true, expired_time: -1,
      model_limits_enabled: true, model_limits: ALLOWED_MODEL,
    },
  });
  if (set.json?.success !== true) throw new Error(`设置模型限制失败：${set.json?.message ?? set.text.slice(0, 160)}`);
  dim(`已把令牌限制为只允许 ${ALLOWED_MODEL}（model_limits="${ALLOWED_MODEL}"）`);

  const results = [];

  line('\n=== A 基线：不带 pin 请求未授权模型（期望 403）===');
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const a = await chat(DISALLOWED_MODEL, `Bearer ${key}`);
  line(`  HTTP ${a.status} ${a.json?.error?.message ?? ''}`.slice(0, 160));
  const aOk = a.status === 403;
  aOk ? pass('非 pin 路径确实拦住了未授权模型') : fail(`非 pin 路径未拦截（HTTP ${a.status}），基线不成立`);
  results.push({ name: 'A 基线', ok: aOk });

  line('\n=== B 漏洞：带 pin 指向提供该模型的渠道（修复前应 200＝绕过；修复后应 403）===');
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const b = await chat(DISALLOWED_MODEL, `Bearer ${key}-${chDisallowed.id}`);
  line(`  HTTP ${b.status} ${b.json?.error?.message ?? ''}`.slice(0, 200));
  const stubB = (await call(`${STUB}/_control/requests?limit=5`)).json;
  const reached = stubB?.requests?.length ?? 0;
  info(`该请求到达上游 ${reached} 次`);
  const bBypassed = b.status === 200;
  if (bBypassed) {
    fail(`绕过确认：pin 到渠道 ${chDisallowed.id} 后，未授权模型被放行（HTTP 200，且${reached ? '已发往上游' : '未发往上游'}）`);
  } else {
    pass(`pin 路径同样执行了授权校验：HTTP ${b.status}，未授权模型被拒`);
  }
  results.push({ name: 'B pin 绕过', ok: !bBypassed, bypassed: bBypassed });

  line('\n=== C 反例：带 pin 请求已授权模型（期望 200，证明修复未误伤正常用法）===');
  const c = await chat(ALLOWED_MODEL, `Bearer ${key}-${chAllowed.id}`);
  line(`  HTTP ${c.status} ${c.json?.error?.message ?? ''}`.slice(0, 160));
  const cOk = c.status === 200;
  cOk ? pass('已授权模型在 pin 下仍可正常调用') : fail(`已授权模型被误拦（HTTP ${c.status}）`);
  results.push({ name: 'C 反例', ok: cOk });

  // 复原令牌（关闭模型限制）
  await call(`${BASE}/api/token/`, {
    method: 'PUT', auth: dashAuth,
    body: { id: tk.id, name: TOKEN_NAME, unlimited_quota: true, expired_time: -1, model_limits_enabled: false, model_limits: '' },
  });
  dim('已复原令牌（关闭模型限制）');

  line('\n=== 结论 ===');
  const bypass = results.find((r) => r.name === 'B pin 绕过');
  if (bypass && !bypass.ok) {
    line('  \x1b[33mTD-08 缺陷复现成功：指定来源可以绕过「允许模型」硬限制。\x1b[0m');
    line('  → 需要在 middleware/distributor.go 把模型限制校验提前为 pin / 非 pin 共用。');
    process.exitCode = 3;
  } else if (results.every((r) => r.ok)) {
    line('  \x1b[32mTD-08 已修复：pin 与非 pin 走同一授权校验，且正常用法未被误伤。\x1b[0m');
  } else {
    line('  \x1b[31m存在失败项，见上。\x1b[0m');
    process.exitCode = 1;
  }
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
