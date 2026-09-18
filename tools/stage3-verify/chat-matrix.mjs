#!/usr/bin/env node
// T3.1 验收：对话侧完整化——**经我们的平台**打真实上游，逐项验证协议矩阵。
//
// 覆盖：同步 Chat Completions、**SSE 流式**、**工具调用**、**Anthropic Messages**、**Responses**。
// 每一项都记录"支持 / 不支持 / 未验证"，**不支持项明确声明**（计划书要求：尚未支持的维度
// 不能在界面宣称已生效）。
//
// 用法：
//   APIGX_REAL_UPSTREAM_KEY=... node tools/stage3-verify/chat-matrix.mjs
//   （分两阶段：--phase setup 建供给；--phase verify 跑矩阵）

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const UPSTREAM = argOf('--upstream', 'https://tokenrhythm.studio');
const PHASE = argOf('--phase', 'all');
const UPSTREAM_KEY = process.env.APIGX_REAL_UPSTREAM_KEY ?? '';

// 参与矩阵的模型：全部来自实测的上游能力标记
const CHAT_MODEL = argOf('--chat-model', 'glm-5.3-flash');       // 基础对话
const ANTHROPIC_MODEL = argOf('--anthropic-model', 'glm-5.1');   // supports_anthropic
const RESPONSES_MODEL = argOf('--responses-model', 'qwen3.7-max'); // supports_responses
const TOOL_MODEL = argOf('--tool-model', 'glm-5.3-flash');       // supports_tools

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const unsupported = (s) => console.log('  \x1b[33mDECLARED-UNSUPPORTED\x1b[0m  ' + s);

async function call(url, { method = 'GET', body, auth, raw = false, timeoutMs = 180000 } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal });
    if (raw) return { status: res.status, text: await res.text(), headers: res.headers };
    const text = await res.text();
    let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
    return { status: res.status, json, text, headers: res.headers };
  } finally { clearTimeout(timer); }
}

const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const OUT_FILE = path.join(STATE_DIR, 'chat-matrix.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }

async function login(user, password) {
  const cached = sessions[user];
  if (cached?.token) {
    const probe = await call(`${BASE}/api/user/self`, { auth: 'Bearer ' + cached.token });
    if (probe.status === 200) return 'Bearer ' + cached.token;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 120)}`);
  sessions[user] = { token: r.json.data.access_token };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function platformKey(root) {
  const t = await call(`${BASE}/api/token/`, { auth: root });
  const items = t.json?.data?.items ?? [];
  const vt = (Array.isArray(items) ? items : []).find((x) => x?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  const k = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  return k.json?.data?.key;
}

// 建一条供给：真实上游 + 指定模型（一个模型一条，便于按能力分别验证）
async function seedOffering(root, model, stamp, baseChannelName) {
  let base = null;
  const list = await call(`${BASE}/api/channel/?p=1&page_size=100`, { auth: root });
  base = (list.json?.data?.items ?? []).find((c) => c?.name === baseChannelName);
  if (!base) {
    await call(`${BASE}/api/channel/`, {
      method: 'POST', auth: root,
      body: { mode: 'single', channel: { name: baseChannelName, type: 1, key: UPSTREAM_KEY, base_url: UPSTREAM, models: model, groups: ['default'], status: 1, priority: 0, weight: 1 } },
    });
    for (let attempt = 0; attempt < 10 && !base; attempt++) {
      const merged = { items: [] };
      for (let p = 1; p <= 40; p++) {
        const r = await call(`${BASE}/api/channel/?p=${p}&page_size=100`, { auth: root });
        const it = r.json?.data?.items ?? [];
        merged.items.push(...it);
        if (it.length < 100) break;
      }
      base = merged.items.find((c) => c?.name === baseChannelName);
      if (!base) await new Promise((r) => setTimeout(r, 2000));
    }
  }
  if (!base?.id) throw new Error('查不到底座渠道 ' + baseChannelName);

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'T31组-' + stamp, default_visibility: 2 } });
  // U-1：分组新建后是「待审核」，本脚本代表"平台已审核通过"的正常状态
  await call(`${BASE}/api/apigx/share-groups/${g.json.data.id}/publish-status`, {
    method: 'POST',
    auth: root,
    body: { status: 2 },
  });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'T31渠道-' + model.replace(/[^\w.-]/g, '_') + '-' + stamp, protocol: 'openai', base_url: UPSTREAM },
  });
  const channelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
    method: 'POST', auth: root, body: { stable_key_id: 't31-' + model.replace(/[^\w.-]/g, '_') + '-' + stamp, name: 'k', key: UPSTREAM_KEY },
  });
  await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
  const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, { method: 'POST', auth: root, body: { upstream_model_id: model } });
  const offeringId = off.json.data.id;
  await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
  await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });
  return { model, channelId, offeringId };
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const stamp = Date.now().toString().slice(-6);

  if (PHASE === 'setup' || PHASE === 'all') {
    if (!UPSTREAM_KEY) throw new Error('缺少 APIGX_REAL_UPSTREAM_KEY（密钥只从环境变量读取）');
    const seeded = {};
    for (const [tag, model] of [['chat', CHAT_MODEL], ['anthropic', ANTHROPIC_MODEL], ['responses', RESPONSES_MODEL], ['tool', TOOL_MODEL]]) {
      seeded[tag] = await seedOffering(root, model, stamp, 'T31底座-' + model.replace(/[^\W]/g, '_') + '-' + stamp);
      info(`已建供给 ${tag}: ${model}`);
    }
    fs.mkdirSync(STATE_DIR, { recursive: true });
    fs.writeFileSync(OUT_FILE, JSON.stringify({ stamp, seeded }, null, 2));
    if (PHASE === 'setup') { info('下一步：等底座同步（约 60 秒）后执行 --phase verify'); return; }
  }

  const state = JSON.parse(fs.readFileSync(OUT_FILE, 'utf8'));
  const key = await platformKey(root);
  const auth = 'Bearer ' + key;
  const results = [];
  const record = (name, status, detail) => {
    results.push({ name, status, detail });
    if (status === 'pass') pass(`${name}${detail ? ' — ' + detail : ''}`);
    else if (status === 'unsupported') unsupported(`${name}${detail ? ' — ' + detail : ''}`);
    else fail(`${name}${detail ? ' — ' + detail : ''}`);
  };

  // ---- 1) 同步 Chat Completions ----
  const sync = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth,
    body: { model: CHAT_MODEL, messages: [{ role: 'user', content: 'Say exactly: pong' }], max_tokens: 24 },
  });
  record('对话·同步 Chat Completions', sync.status === 200 ? 'pass' : 'fail', `HTTP ${sync.status}，finish=${sync.json?.choices?.[0]?.finish_reason}`);

  // ---- 2) SSE 流式 ----
  const stream = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth, raw: true,
    body: { model: CHAT_MODEL, messages: [{ role: 'user', content: 'Count 1 to 3.' }], max_tokens: 32, stream: true },
  });
  const chunks = stream.text.split('\n').filter((l) => l.startsWith('data:') && !l.includes('[DONE]')).length;
  const hasDone = stream.text.includes('[DONE]');
  const isSse = String(stream.headers?.get('content-type') ?? '').includes('text/event-stream');
  record('对话·SSE 流式', isSse && chunks >= 2 && hasDone ? 'pass' : 'fail',
    `content-type=${String(stream.headers?.get('content-type')).slice(0, 40)} 数据块=${chunks} 含[DONE]=${hasDone}`);

  // ---- 3) 工具调用 ----
  const toolCall = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth,
    body: {
      model: TOOL_MODEL,
      messages: [{ role: 'user', content: 'What is the weather in Shanghai? Use the tool.' }],
      max_tokens: 128,
      tools: [{
        type: 'function',
        function: { name: 'get_weather', description: 'Get weather', parameters: { type: 'object', properties: { city: { type: 'string' } }, required: ['city'] } },
      }],
      tool_choice: 'auto',
    },
  });
  const called = toolCall.json?.choices?.[0]?.message?.tool_calls?.length ?? 0;
  record('对话·工具调用', called > 0 ? 'pass' : 'fail',
    called > 0 ? `模型发起 ${called} 个工具调用` : `未发起工具调用（HTTP ${toolCall.status}）`);

  // ---- 4) Anthropic Messages 协议 ----
  const anthropic = await call(`${BASE}/v1/messages`, {
    method: 'POST', auth,
    body: { model: ANTHROPIC_MODEL, max_tokens: 32, messages: [{ role: 'user', content: 'Say exactly: pong' }] },
  });
  record('对话·Anthropic Messages', anthropic.status === 200 ? 'pass' : 'fail',
    `HTTP ${anthropic.status}${anthropic.status !== 200 ? '：' + anthropic.text.slice(0, 120) : ''}`);

  // ---- 5) Responses 协议 ----
  const responses = await call(`${BASE}/v1/responses`, {
    method: 'POST', auth,
    body: { model: RESPONSES_MODEL, input: 'Say exactly: pong', max_output_tokens: 32 },
  });
  record('对话·Responses', responses.status === 200 ? 'pass' : 'fail',
    `HTTP ${responses.status}${responses.status !== 200 ? '：' + responses.text.slice(0, 120) : ''}`);

  // ---- 汇总：逐项记录支持/不支持 ----
  console.log('\n  协议矩阵（本平台 × 真实上游）');
  for (const r of results) {
    const mark = r.status === 'pass' ? '支持' : r.status === 'unsupported' ? '明确不支持' : '未通过';
    console.log(`    ${r.name.padEnd(28)} ${mark}${r.detail ? '  (' + r.detail + ')' : ''}`);
  }
  const failed = results.filter((r) => r.status === 'fail');
  console.log(`\n  共 ${results.length} 项，支持 ${results.filter((r) => r.status === 'pass').length}，不支持 ${results.filter((r) => r.status === 'unsupported').length}，未通过 ${failed.length}`);
  fs.writeFileSync(OUT_FILE, JSON.stringify({ ...state, results }, null, 2));
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
