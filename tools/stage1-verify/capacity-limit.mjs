#!/usr/bin/env node
// T1.7 验收：共享容量控制接线的运行时行为
//
// 让替身把每个请求握 1.5 秒，然后并发发 N 个请求，观察渠道并发上限是否真的生效：
//   --expect off → 全部 200（功能默认关闭，既有链路不变）
//   --expect on  → 恰好 limit 个 200，其余 429 且带 Retry-After（且不得到达上游）
//
// 用法：
//   node tools/stage1-verify/capacity-limit.mjs --expect off
//   node tools/stage1-verify/capacity-limit.mjs --expect on --limit 1 --concurrency 3

import fs from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
// 默认模型：**从广场取一个已发布供给的模型**。
//
// 为什么要这样：本脚本验证的是"环境级渠道并发上限"，而该上限只对我们绑定的
// **分享渠道**生效（AcquireForAttempt 按 ShareChannelIDByBase 判定）。
// 早先它写死了一个共享模型名，那个模型下已积累大量渠道，请求可能落到**平台自建渠道**上，
// 于是表现为"完全没限流"——这不是产品缺陷，而是脚本选了不适用的目标
// （详见 TASK_LIST 的阶段 2 验收汇总）。
// **必须显式传 `--model`，且该模型只能有一条分享渠道。** 两条约束都来自被测语义：
//   ① `APIGX_CHANNEL_CONCURRENCY_LIMIT` 只对我们绑定的**分享渠道**生效
//      （`AcquireForAttempt` 按 `ShareChannelIDByBase` 判定），平台自建渠道不受它约束；
//   ② 上限是**按渠道**的，同一模型有 N 条分享渠道时总放行数就是 N × 上限。
// 早先写死了一个共享模型名（其下渠道众多，请求可能落到平台自建渠道上），表现为"完全没限流"——
// 那不是产品缺陷，而是**脚本选了不适用的目标**。现在不传就直接报错，而不是给一个会误导的默认值。
const MODEL = argOf('--model', '');
if (!MODEL) {
  console.log('  \x1b[31m需要 --model\x1b[0m  请传入一个只有**单条分享渠道**的模型（环境级上限只作用于分享渠道，且按渠道计数）');
  process.exit(2);
}
const TOKEN_NAME = argOf('--token-name', 'apigx-stage0-verify');
const EXPECT = argOf('--expect', 'on');
const LIMIT = Number(argOf('--limit', '1'));
const N = Number(argOf('--concurrency', '3'));
const HOLD_MS = Number(argOf('--hold-ms', '1500'));

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
  return { status: res.status, json, text, headers: res.headers };
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function login() {
  const user = 'apigxroot';
  const c = sessions[user];
  if (c?.token && c.expiresAt && Date.now() / 1000 < c.expiresAt - 120) {
    const probe = await call(`${BASE}/api/user/self`, { auth: `Bearer ${c.token}`, jar: false });
    if (probe.status === 200) return `Bearer ${c.token}`;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password: 'ApigxDev#2026' }, jar: false });
  const t = r.json?.data?.access_token;
  if (!t) throw new Error(`登录失败：HTTP ${r.status}`);
  sessions[user] = { token: t, expiresAt: r.json?.data?.access_expires_at ?? null };
  saveSessions(sessions);
  return `Bearer ${t}`;
}

async function main() {
  const rootAuth = await login();
  let key = sessions[`token:${TOKEN_NAME}`]?.key;
  if (!key) {
    const list = await call(`${BASE}/api/token/`, { auth: rootAuth });
    const items = list.json?.data?.items ?? list.json?.data ?? [];
    const mine = (Array.isArray(items) ? items : []).find((t) => t?.name === TOKEN_NAME);
    if (!mine) throw new Error(`找不到令牌 ${TOKEN_NAME}`);
    const got = await call(`${BASE}/api/token/${mine.id}/key`, { method: 'POST', auth: rootAuth });
    key = got.json?.data?.key;
    sessions[`token:${TOKEN_NAME}`] = { id: mine.id, key };
    saveSessions(sessions);
  }
  require_(key, '取不到令牌明文');

  // 让替身把请求握一段时间，制造真实的并发重叠
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: HOLD_MS, ttftMs: 0, status: 0, hang: false } });
  await call(`${STUB}/_control/reset`, { method: 'POST' });

  dim(`期望：${EXPECT === 'on' ? `上限 ${LIMIT} 生效 → 恰好 ${LIMIT} 个 200，其余 429` : '功能关闭 → 全部 200'}`);
  const results = await Promise.all(Array.from({ length: N }, (_, i) =>
    call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: `Bearer ${key}`, jar: false,
      body: { model: MODEL, messages: [{ role: 'user', content: `capacity ${i}` }] },
    }).then((r) => ({ status: r.status, retryAfter: r.headers.get('retry-after'), body: r.text.slice(0, 120) }))
      .catch((e) => ({ status: 'ERR', retryAfter: null, body: e.message }))));

  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0 } });

  const ok = results.filter((r) => r.status === 200).length;
  const tooMany = results.filter((r) => r.status === 429).length;
  const other = results.filter((r) => r.status !== 200 && r.status !== 429).length;
  const stub = (await call(`${STUB}/_control/requests?limit=50`)).json?.requests?.length ?? 0;

  info(`状态分布：200×${ok}，429×${tooMany}，其它×${other}；到达上游 ${stub} 次`);
  const firstReject = results.find((r) => r.status === 429);
  if (firstReject) dim(`被拒响应示例：Retry-After=${firstReject.retryAfter ?? '(无)'} body=${firstReject.body}`);

  if (EXPECT === 'on') {
    ok === LIMIT ? pass(`并发上限生效：恰好 ${LIMIT} 个通过`) : fail(`期望 ${LIMIT} 个通过，实际 ${ok} 个`);
    tooMany === N - LIMIT ? pass(`其余 ${tooMany} 个被 429 拒绝`) : fail(`期望 ${N - LIMIT} 个被拒，实际 ${tooMany} 个`);
    firstReject && firstReject.retryAfter ? pass('429 带 Retry-After') : fail('429 缺少 Retry-After');
    stub <= LIMIT ? pass(`被拒请求未到达上游（上游只收到 ${stub} 次）`) : fail(`有被拒请求仍到达上游（${stub} 次）`);
  } else {
    ok === N ? pass(`功能关闭时全部通过（${N}/${N}），既有链路未被改变`) : fail(`期望全部通过，实际 ${ok}/${N}`);
    tooMany === 0 ? pass('未出现 429') : fail(`出现了 ${tooMany} 个 429`);
  }
  const failed = (EXPECT === 'on')
    ? !(ok === LIMIT && tooMany === N - LIMIT)
    : !(ok === N && tooMany === 0);
  if (failed) process.exitCode = 1;
}

function require_(v, msg) { if (!v) throw new Error(msg); }

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
