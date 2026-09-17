#!/usr/bin/env node
// 阶段 2 回扫：**接真实上游做端到端验证**（不是替身）。
//
// 与其它验收脚本的分工：其它脚本用替身验证"平台自己的语义"（原子性、幂等、分流），
// 本脚本验证**真实第三方上游**下整条链路仍然正确：真实鉴权、真实响应体（含非标准字段）、
// 真实 usage 计费、质量样本落库，以及"**价格未知 ≠ 零**"在真实模型上的表现。
//
// 密钥**只从环境变量读取**，绝不写进仓库：
//   APIGX_REAL_UPSTREAM_KEY=...  node tools/stage2-verify/real-upstream.mjs
//
// 用法：
//   node tools/stage2-verify/real-upstream.mjs \
//     --upstream https://tokenrhythm.studio/v1 --model glm-5.3-flash --phase setup
//   node tools/stage2-verify/real-upstream.mjs --model glm-5.3-flash --phase verify

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
// 注意：这里取的是**根形式**（不带 /v1）。底座的 OpenAI 适配器会在 base_url 之后
// 再拼 `/v1/chat/completions`，因此写成 `.../v1` 会得到 `/v1/v1/...` → 上游 404
// （本脚本回扫时正是这么发现的；我们这层的渠道接口已做归一化，但**底座渠道**由平台设置，
//  这里显式给正确形态）。
const UPSTREAM = argOf('--upstream', 'https://tokenrhythm.studio');
const MODEL = argOf('--model', 'glm-5.3-flash');
const PHASE = argOf('--phase', 'setup');
const UPSTREAM_KEY = process.env.APIGX_REAL_UPSTREAM_KEY ?? '';

async function call(url, { method = 'GET', body, auth, timeoutMs = 120000 } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal });
    const text = await res.text();
    let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
    return { status: res.status, json, text };
  } finally {
    clearTimeout(timer);
  }
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const dim = (s) => console.log('  \x1b[2m' + s + '\x1b[0m');

const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const REAL_FILE = path.join(STATE_DIR, 'real-upstream.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }
function saveReal(d) { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(REAL_FILE, JSON.stringify(d, null, 2)); }
function loadReal() { return JSON.parse(fs.readFileSync(REAL_FILE, 'utf8')); }

async function login(user, password) {
  const cached = sessions[user];
  if (cached?.token) {
    const probe = await call(`${BASE}/api/user/self`, { auth: 'Bearer ' + cached.token });
    if (probe.status === 200) return 'Bearer ' + cached.token;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status}`);
  sessions[user] = { token: r.json.data.access_token };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  if (PHASE === 'setup') {
    if (!UPSTREAM_KEY) throw new Error('缺少 APIGX_REAL_UPSTREAM_KEY 环境变量（密钥只从环境变量读取，不写仓库）');
    const stamp = Date.now().toString().slice(-6);
    const channelName = 'REAL-tokenrhythm-' + stamp;
    const created = await call(`${BASE}/api/channel/`, {
      method: 'POST', auth: root,
      body: {
        mode: 'single',
        channel: {
          name: channelName, type: 1, key: UPSTREAM_KEY, base_url: UPSTREAM,
          models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1,
        },
      },
    });
    if (created.status !== 200) throw new Error('建底座渠道失败：' + created.text.slice(0, 200));
    let base = null;
    for (let i = 0; i < 10 && !base; i++) {
      const list = await call(`${BASE}/api/channel/?page=1&page_size=1000`, { auth: root });
      base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
      if (!base) await new Promise((r) => setTimeout(r, 2000));
    }
    if (!base?.id) throw new Error('查不到刚建的底座渠道');

    const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'REAL组-' + stamp, default_visibility: 2 } });
    const ch = await call(`${BASE}/api/apigx/channels`, {
      method: 'POST', auth: root,
      body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'REAL渠道-' + stamp, protocol: 'openai', base_url: UPSTREAM },
    });
    if (ch.status !== 200) throw new Error('建分享渠道失败：' + ch.text.slice(0, 200));
    const channelId = ch.json.data.id;
    // **供给密钥就是真实上游凭据本身**：平台的语义是"分享者提供的上游 Key 会经加密后
    // 投影到底座的多 Key 列表"，因此出站用的就是这一把。用假 Key 只会得到 401。
    const upstreamKeyForPool = UPSTREAM_KEY;
    await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
      method: 'POST', auth: root, body: { stable_key_id: 'real-' + stamp, name: 'primary', key: upstreamKeyForPool },
    });
    await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
    const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, {
      method: 'POST', auth: root, body: { upstream_model_id: MODEL },
    });
    if (off.status !== 200) throw new Error('建供给失败：' + off.text.slice(0, 200));
    const offeringId = off.json.data.id;
    await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
    await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });

    saveReal({ stamp, model: MODEL, upstream: UPSTREAM, baseChannelId: base.id, channelId, offeringId });
    info(`已把真实上游接成供给：base=${base.id} channel=${channelId} offering=${offeringId}`);
    dim(`底座按数据库同步渠道（约 60 秒）后才可路由，因此 setup 与 verify 分两步。`);
    return;
  }

  // ---- verify ----
  const real = loadReal();
  if (!real?.channelId) throw new Error('请先执行 --phase setup');
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items) ? items : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const ask = (maxTokens) => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: real.model, messages: [{ role: 'user', content: 'Reply with the single word: pong' }], max_tokens: maxTokens },
  });

  // 等可路由（底座渠道同步 + 能力索引）
  let response = null;
  for (let i = 0; i < 30; i++) {
    response = await ask(24);
    if (!response.text.includes('model_not_found')) break;
    dim(`等待底座同步渠道（第 ${i + 1} 次探测）...`);
    await new Promise((r) => setTimeout(r, 5000));
  }
  const body = response?.json ?? {};
  check('真实上游请求成功（经平台完整链路）', response?.status === 200, `HTTP ${response?.status}`);
  if (response?.status !== 200) {
    info('响应：' + response.text.slice(0, 300));
  } else {
    const choice = body?.choices?.[0];
    check('返回真实补全内容（非替身应答）', Boolean(choice?.message?.content || choice?.message?.reasoning_content),
      `finish_reason=${choice?.finish_reason}`);
    check('usage 由真实上游给出', Boolean(body?.usage && body.usage.total_tokens > 0),
      `tokens=${body?.usage?.prompt_tokens}+${body?.usage?.completion_tokens}=${body?.usage?.total_tokens}`);
    dim('响应片段：' + JSON.stringify(body).slice(0, 200));
  }

  // 平台侧记账：用量记录
  // **必须匹配本次真实模型**：早先只取最近一条就判 `Boolean(latest.model)`，
  // 会被历史记录命中（假阳性——实测它匹配到了一条 stub 模型的旧记录）。
  const usage = await call(`${BASE}/api/apigx/usage?page_size=20`, { auth: root });
  const records = usage.json?.data?.items ?? [];
  const mine = records.find((x) => x?.model === real.model);
  check('平台记录了这次真实调用', Boolean(mine), mine ? `model=${mine.model} tokens=${mine.total_tokens}` : `最近记录里没有 ${real.model}`);

  // 供给与质量视图
  const explain = await call(`${BASE}/api/apigx/route-explain?model=${encodeURIComponent(real.model)}`, { auth: root });
  const candidates = explain.json?.data?.candidates ?? [];
  check('真实供给出现在可解释路由结果里', candidates.length > 0, `候选数=${candidates.length}`);
  if (candidates.length > 0) {
    const first = candidates[0];
    dim(`候选：offering=${first.offering_public_id} samples=${first.sample_count} insufficient=${first.insufficient} price_known=${first.price_known}`);
    check('价格未知 ≠ 零（真实模型未摄入价格时如实标注）', first.price_known === false,
      `price_known=${first.price_known}`);
  }

  // 质量样本：真实请求后应至少有一个样本（20 次以内仍应标 Insufficient）
  const second = await call(`${BASE}/api/apigx/route-explain?model=${encodeURIComponent(real.model)}`, { auth: root });
  const c2 = (second.json?.data?.candidates ?? [])[0] ?? {};
  check('样本不足时如实标注（不伪造成功率）', c2.insufficient === true || (c2.sample_count ?? 0) === 0,
    `samples=${c2.sample_count} insufficient=${c2.insufficient}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
