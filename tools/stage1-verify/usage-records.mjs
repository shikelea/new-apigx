#!/usr/bin/env node
// T1.11 验收：用量记录（使用者视角）
//
// 验证：真实请求之后，使用者能在自己的用量记录里看到
//   模型 / 实际来源（分享渠道的对外标识或平台）/ token 数 / 耗时 / 估算成本，
// 并且**只能看到自己的**记录，响应里不含上游地址与凭据。
//
// 用法：node tools/stage1-verify/usage-records.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const MODEL = argOf('--model', 'stub/model-x-202609');

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

const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }

// 优先复用缓存会话：底座的**每用户会话数有上限**，反复登录会返回 409 AUTH_SESSION_LIMIT。
async function login(user, password) {
  const cached = sessions[user];
  if (cached?.token) {
    const probe = await call(`${BASE}/api/user/self`, { auth: 'Bearer ' + cached.token });
    if (probe.status === 200) return 'Bearer ' + cached.token;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) {
    throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 100)}（会话数达上限时请复用缓存会话）`);
  }
  sessions[user] = { token: r.json.data.access_token };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const authA = await login('apigxroot', 'ApigxDev#2026');
  const authB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  // 取一把可用令牌并真实发两次请求
  const tokens = await call(`${BASE}/api/token/`, { auth: authA });
  const items0 = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: authA });
  const platformKey = keyRes.json?.data?.key;
  if (!platformKey) throw new Error('取不到令牌明文');

  const requestIds = [];
  for (let i = 0; i < 2; i++) {
    const r = await call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: 'Bearer ' + platformKey,
      body: { model: MODEL, messages: [{ role: 'user', content: 'usage probe ' + i }] },
    });
    if (i === 0) check('真实请求成功（用于产生用量记录）', r.status === 200, `HTTP ${r.status}`);
  }

  // 查询自己的用量
  const usage = await call(`${BASE}/api/apigx/usage?page_size=10`, { auth: authA });
  check('用量接口可用', usage.status === 200, `HTTP ${usage.status}`);
  const items = usage.json?.data?.items ?? [];
  check('返回了用量记录', items.length > 0, `共 ${items.length} 条（total=${usage.json?.data?.total}）`);

  const latest = items[0] ?? {};
  info(`最近一条：model=${latest.model} tokens=${latest.total_tokens} cost_micro_usd=${latest.cost_micro_usd} elapsed_ms=${latest.elapsed_seconds} source=${JSON.stringify(latest.source)}`);
  check('记录里有模型名', latest.model === MODEL, `model=${latest.model}`);
  check('记录里有耗时（秒级精度，底座日志只有整秒）', typeof latest.elapsed_seconds === 'number', `elapsed_ms=${latest.elapsed_seconds}`);
  check('记录里有 token 数', (latest.total_tokens ?? 0) > 0, `total_tokens=${latest.total_tokens}`);
  check('记录里有估算成本', (latest.cost_micro_usd ?? 0) > 0, `cost_micro_usd=${latest.cost_micro_usd}`);
  check('记录里有实际来源（分享渠道或平台）',
    latest.source && (latest.source.kind === 'share' || latest.source.kind === 'platform'),
    JSON.stringify(latest.source));

  // 归属隔离：B 的用量里不应出现 A 刚才的 request_id
  const usageB = await call(`${BASE}/api/apigx/usage?page_size=10`, { auth: authB });
  const idsB = (usageB.json?.data?.items ?? []).map((x) => x.request_id).filter(Boolean);
  const leaked = idsB.filter((id) => id === latest.request_id);
  check('他人用量里不含本次请求', leaked.length === 0, `B 有 ${idsB.length} 条记录`);

  // 泄露扫描
  const forbidden = [
    { name: '上游地址', pattern: /127\.0\.0\.1:8100/ },
    { name: '凭据', pattern: /enc:v1:|sk-src-|sk-pool-|placeholder/ },
    { name: '堆栈/文件路径', pattern: /\.go:\d+|goroutine \d+/ },
  ];
  for (const f of forbidden) {
    check(`用量响应不含${f.name}`, !f.pattern.test(usage.text), f.pattern.test(usage.text) ? '出现泄露' : '未出现');
  }

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
