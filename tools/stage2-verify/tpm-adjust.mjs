#!/usr/bin/env node
// T2.5 验收：TPM 预留按**实际用量**退回（结算期调整）
//
// 做一件事：建一条渠道并把 TPM 上限设得很大，发**一次**请求，然后打印该渠道的窗口计数
// 供外部（PowerShell + redis-cli）核对。
//
//   调整开启 → 计数应≈实际 token 数（本场景约几十）
//   调整关闭 → 计数是最坏情况预留（prompt + 输出上限 8192 ≈ 8200+）
//
// 用法：node tools/stage2-verify/tpm-adjust.mjs --expect adjusted|reserved
// 前置：网关需按期望值设置 APIGX_TPM_ADJUST_ENABLED

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const EXPECT = argOf('--expect', 'adjusted');

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
const OUT_FILE = path.join(STATE_DIR, 'tpm-adjust.json');
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
  const stamp = Date.now().toString().slice(-6);
  const MODEL = 'stub/tpmadj-' + stamp;

  const channelName = 'TPMADJ底座-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-tpmadj-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await call(`${BASE}/api/channel/?page=1&page_size=1000`, { auth: root });
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'TPMADJ组-' + stamp } });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'TPMADJ渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: root, body: { stable_key_id: 'tpmadj-' + stamp, name: 'k', key: 'sk-tpmadjup-' + stamp },
  });
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });
  // TPM 上限设得足够大：本用例只想看计数，不想被限流
  const policy = await call(`${BASE}/api/apigx/channels/${shareChannelId}/capacity-policy`, {
    method: 'PUT', auth: root, body: { tpm_mode: 4, tpm_limit: 1000000 },
  });
  if (policy.status !== 200) throw new Error('设置 TPM 策略失败：' + policy.text.slice(0, 140));

  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'tpm adjust probe' }] },
  });

  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('模型不可路由');

  // 等策略缓存过期（30s），确保请求时 TPM 上限已生效
  info('等待策略缓存过期（32s）...');
  await new Promise((r) => setTimeout(r, 32000));

  const before = await call(`${BASE}/api/apigx/usage?page_size=5`, { auth: root });
  const request = await chat();
  const okStatus = request.status === 200;
  (okStatus ? pass : fail)(`请求成功（用于产生一次 TPM 预留） — HTTP ${request.status}`);
  if (!okStatus) {
    info('响应：' + request.text.slice(0, 160));
  }

  // 从用量里取本次实际 token 数（作为"实际用量"的独立来源）
  const usage = await call(`${BASE}/api/apigx/usage?page_size=1`, { auth: root });
  const latest = (usage.json?.data?.items ?? [])[0] ?? {};
  info(`用量接口给出的最近一次：model=${latest.model} total_tokens=${latest.total_tokens}`);

  fs.mkdirSync(STATE_DIR, { recursive: true });
  fs.writeFileSync(OUT_FILE, JSON.stringify({
    baseChannelId: base.id, shareChannelId, model: MODEL, expect: EXPECT,
    actualTokens: latest.total_tokens ?? 0,
  }, null, 2));
  info(`已写出待核对信息：base_channel_id=${base.id} 期望=${EXPECT}`);
  info('下一步：外部读取 Redis 里的 channel tpm 计数并核对');
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
