#!/usr/bin/env node
// T1.10 验收（续）：允许模型 + 固定来源
//
//  ① allowed_model_ids=[别的模型] → 拒绝
//  ② allowed_model_ids=[本模型]   → 放行
//  ③ 固定来源 = 该渠道的供给        → 放行
//  ④ 固定来源 = 无法解析的供给      → 拒绝（pinned_source_unavailable）
//
// 用法：node tools/stage1-verify/pinned-and-models.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');

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
  const stamp = Date.now().toString().slice(-6);
  const MODEL = 'stub/pin-' + stamp;

  // 底座渠道（由 root 的分享渠道绑定；模型只由它提供）
  const channelName = 'PIN底座-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-pin-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await call(`${BASE}/api/channel/?page=1&page_size=200`, { auth: root });
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'PIN组-' + stamp, default_visibility: 2 } });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'PIN渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: root, body: { stable_key_id: 'pin-' + stamp, name: 'pin', key: 'sk-pinkey-' + stamp },
  });
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });

  // 一条供给（固定来源要指向它）
  const off = await call(`${BASE}/api/apigx/channels/${shareChannelId}/offerings`, {
    method: 'POST', auth: root, body: { upstream_model_id: MODEL },
  });
  if (off.status !== 200) throw new Error('建供给失败：' + off.text.slice(0, 120));
  const offeringId = off.json.data.id;
  await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
  await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });

  // 平台 Key
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'pin probe' }] },
  });
  const setPrefs = async (body) => {
    const res = await call(`${BASE}/api/apigx/platform-keys/${vt.id}/preferences`, { method: 'PUT', auth: root, body });
    if (res.status !== 200) throw new Error(`设置偏好失败：${res.status} ${res.text.slice(0, 120)}`);
    return res.json.data;
  };

  // 等模型可路由
  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('等待模型可路由超时');

  // ① 允许模型不含本模型 → 拒绝
  await setPrefs({ scope_mode: 0, allowed_model_ids: ['stub/other-' + stamp], allow_fallback: false });
  const deniedByModel = await chat();
  info(`① allowed_model_ids 不含本模型 → HTTP ${deniedByModel.status} ${deniedByModel.text.slice(0, 90)}`);
  check('不在允许模型列表内的请求被拒绝', deniedByModel.status !== 200, `HTTP ${deniedByModel.status}`);

  // ② 允许模型含本模型 → 放行
  await setPrefs({ scope_mode: 0, allowed_model_ids: [MODEL], allow_fallback: false });
  const allowedByModel = await chat();
  check('允许模型列表内的请求放行', allowedByModel.status === 200, `HTTP ${allowedByModel.status}`);

  // ③ 固定来源 = 本渠道的供给 → 放行
  await setPrefs({ scope_mode: 2, pinned_offering_id: offeringId, allow_fallback: false });
  const pinnedOK = await chat();
  check('固定来源指向正确供给时放行', pinnedOK.status === 200, `HTTP ${pinnedOK.status}`);

  // ④ 固定来源 = 无法解析的供给 → 拒绝
  await setPrefs({ scope_mode: 2, pinned_offering_id: 99999999, allow_fallback: false });
  const pinnedBad = await chat();
  info(`④ 固定来源指向不可用供给 → HTTP ${pinnedBad.status} ${pinnedBad.text.slice(0, 90)}`);
  check('固定来源不可用时拒绝', pinnedBad.status !== 200, `HTTP ${pinnedBad.status}`);

  // 复原
  await setPrefs({ scope_mode: 0, allow_fallback: true });
  const restored = await chat();
  check('复原默认后恢复可用', restored.status === 200, `HTTP ${restored.status}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
