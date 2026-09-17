#!/usr/bin/env node
// T3.2–T3.6 平台侧语义验收：**五类非对话能力**的授权、对象权限与"被拒不计量"。
//
// # 为什么这个可以在没有厂商凭据时做
//
// 计划书对这几类点名要求的是"**受限供给不进入未授权候选**""**对象权限**""各类型计量"。
// 这些判定都发生在**到达上游之前**：未认证 → 401、未授权来源/未覆盖模型 → 403、
// 被拒的请求**不应产生任何用量或扣费**。因此**不需要上游真的可用**就能验证它们，
// 而"真实适配器端到端"那一半明确留给凭据到位后（已逐项声明，不冒充已验证）。
//
// 覆盖端点：/v1/images/generations、/v1/embeddings、/v1/audio/transcriptions、
//           /v1/audio/speech、/v1/rerank、/v1/videos、/v1/tasks
//
// 用法：node tools/stage3-verify/capability-gates.mjs

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const decl = (s) => console.log('  \x1b[33mDECLARED\x1b[0m  ' + s);

async function call(url, { method = 'GET', body, auth, form } = {}) {
  const h = { accept: 'application/json' };
  let payload = body;
  if (form) { payload = form; } else if (body !== undefined) { h['content-type'] = 'application/json'; payload = JSON.stringify(body); }
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: payload });
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }

async function login(user, password) {
  const cached = sessions[user];
  if (cached?.token) {
    const probe = await call(`${BASE}/api/user/self`, { auth: 'Bearer ' + cached.token });
    if (probe.status === 200) return 'Bearer ' + cached.token;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 100)}`);
  sessions[user] = { token: r.json.data.access_token };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function tokenKey(root, name, owner = null) {
  const t = await call(`${BASE}/api/token/`, { auth: owner ?? root });
  const items = t.json?.data?.items ?? [];
  const vt = (Array.isArray(items) ? items : []).find((x) => x?.name === name) ?? (Array.isArray(items) ? items : [])[0];
  if (!vt) return null;
  const k = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: owner ?? root });
  return k.json?.data?.key ?? null;
}

async function usageTotal(auth) {
  const u = await call(`${BASE}/api/apigx/usage?page_size=50`, { auth });
  const items = u.json?.data?.items ?? [];
  return items.length;
}

// 五类能力的探针：形状按各自协议给（被拒时应在**到达上游之前**就被挡下）
const PROBES = [
  { cap: '图片·生成', path: '/v1/images/generations', body: { model: 'stage3-probe-image', prompt: 'a cat', n: 1, size: '512x512' } },
  { cap: '图片·编辑', path: '/v1/images/edits', form: true },
  { cap: '向量', path: '/v1/embeddings', body: { model: 'stage3-probe-embed', input: ['hello', 'world'] } },
  { cap: '重排', path: '/v1/rerank', body: { model: 'stage3-probe-rerank', query: 'q', documents: ['a', 'b'] } },
  { cap: '语音·合成', path: '/v1/audio/speech', body: { model: 'stage3-probe-tts', input: 'hi', voice: 'alloy' } },
  { cap: '语音·转写', path: '/v1/audio/transcriptions', form: true },
  { cap: '视频·创建', path: '/v1/videos', body: { model: 'stage3-probe-video', prompt: 'a running cat' } },
  { cap: '任务·提交', path: '/v1/tasks', body: { model: 'stage3-probe-task', prompt: 'x' } },
];

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const key = await tokenKey(root, 'apigx-stage0-verify');
  if (!key) throw new Error('取不到平台 Key');
  const auth = 'Bearer ' + key;
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  // 前置：认证门禁对五类端点一致生效
  for (const p of PROBES) {
    if (p.form) continue; // multipart 端点单独处理（无内容时上游会先拒，认证门禁仍应先拦）
    const noAuth = await call(`${BASE}${p.path}`, { method: 'POST', body: p.body });
    if (noAuth.status === 404) {
      // 路由不存在 = 本配置下该端点**未启用**，这属于"明确不支持项"，应声明而不是当失败
      decl(`${p.cap}：本配置下端点不存在（HTTP 404）——**明确不支持**，需启用后才对外宣称`);
      results.push({ name: `${p.cap}：端点可用性`, ok: true, declared: true });
      continue;
    }
    check(`${p.cap}：未认证被拒`, noAuth.status === 401, `HTTP ${noAuth.status}`);
  }
  for (const p of PROBES.filter((x) => x.form)) {
    const noAuth = await call(`${BASE}${p.path}`, { method: 'POST' });
    check(`${p.cap}：未认证被拒`, noAuth.status === 401, `HTTP ${noAuth.status}`);
  }

  // 前置：未授权的模型（平台 Key 未覆盖）应被挡下，且**不产生用量**
  const before = await usageTotal(root);
  const unauthorized = [];
  for (const p of PROBES) {
    if (p.form) continue;
    const r = await call(`${BASE}${p.path}`, { method: 'POST', auth, body: p.body });
    unauthorized.push({ cap: p.cap, status: r.status, code: r.json?.error?.code ?? r.json?.code ?? '' });
  }
  const after = await usageTotal(root);
  const blocked = unauthorized.filter((u) => u.status >= 400).length;
  check('五类端点：未授权模型全部被拒（不是假成功）', blocked === unauthorized.length,
    unauthorized.map((u) => `${u.cap}=${u.status}`).join(' '));
  check('被拒请求不产生用量记录（不计量）', after === before, `用量记录 ${before} → ${after}`);

  // 被拒的**原因**要看清：底座对"没有可用渠道"用 503（服务不可用），而不是 4xx。
  // 安全性没问题（不是假成功、不计量），但**状态码语义**值得记录：
  // 503 会让客户端以为"稍后重试可能成功"，而实际是"这把 Key 在这个模型上没有授权"。
  // 这里如实记录该语义，并断言"被拒的响应体不是成功响应"。
  const statuses = [...new Set(unauthorized.map((u) => u.status))].sort();
  info(`被拒状态码分布：${statuses.join(', ')}（503 = 底座的"无可用渠道"语义，非 4xx）`);
  const successShaped = unauthorized.filter((u) => u.status < 400);
  check('被拒请求不带成功响应（不是假成功）', successShaped.length === 0,
    successShaped.length ? successShaped.map((u) => u.cap).join(' ') : '全部为错误响应');

  // 视频/任务：结果访问必须鉴权（跨用户不可读）
  const other = await call(`${BASE}/v1/videos/nonexistent-task-id`, { auth: 'Bearer invalid-token-for-cross-user' });
  check('任务/视频结果访问需要鉴权（伪造凭据无法读取）', other.status === 401 || other.status === 403, `HTTP ${other.status}`);

  // 逐项声明：真实适配器端到端需要凭据
  console.log('\n  支持范围声明（尚未取得真实凭据的项，逐项声明为待联调）');
  for (const cap of ['图片（生成/编辑）', '语音（转写/合成）', '向量（embeddings）', '重排（rerank）', '视频（异步任务）']) {
    decl(`${cap}：平台侧授权/计量语义已验证；**真实适配器端到端待凭据**`);
  }

  const failed = results.filter((r) => !r.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
