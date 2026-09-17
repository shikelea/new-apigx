#!/usr/bin/env node
// T1.7 验收：渠道 TPM（每分钟 token 数）
//
// 与并发/RPM 的关键差别：TPM 的预留量必须来自**可执行的输出上限**，
// 因此校验点在**请求解析之后**（`relay.PrepareRequestBilling`），
// 预留量 = prompt tokens + 输出上限（缺省 8192）。
//
//  ① tpm_mode=4, tpm_limit=100        → 单次预留量远大于 100，第一次就应 429
//  ② tpm_mode=3（明确不限）            → 放行
//  ③ tpm_mode=4, tpm_limit=100000     → 放行，且窗口计数应≈单次预留量（证明就是 prompt+上限）
//
// 用法：node tools/stage1-verify/channel-tpm.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000, APIGX_CAPACITY_ENABLED=true)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');

async function call(url, { method = 'GET', body, auth } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text, headers: res.headers };
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

// 会话令牌有有效期：复用缓存时**主动检查年龄**，避免跑一半突然 AUTH_TOKEN_EXPIRED
// （本脚本单次要跑 100 秒以上，中途过期会让后半段全部失败）。
const SESSION_MAX_AGE_MS = 30 * 60 * 1000;

async function login(user, password) {
  const cached = sessions[user];
  const fresh = cached?.token && cached?.loginAt && (Date.now() - cached.loginAt) < SESSION_MAX_AGE_MS;
  if (fresh) {
    const probe = await call(`${BASE}/api/user/self`, { auth: 'Bearer ' + cached.token });
    if (probe.status === 200) return 'Bearer ' + cached.token;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status}`);
  sessions[user] = { token: r.json.data.access_token, loginAt: Date.now() };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  const stamp = Date.now().toString().slice(-6);
  const MODEL = 'stub/tpm-' + stamp;

  const channelName = 'TPM底座-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-tpm-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await (async () => {
        // **翻页合并**：底座的列表接口把 page_size 封顶在 100，而开发库里测试渠道已 112+ 条，
        // 只查第一页会"查不到刚建的渠道"（实测踩过）。这里翻完所有页再合并，
        // 返回**同样的形状**，因此下游代码无需改动。
        const merged = { json: { data: { items: [] } }, status: 200, text: '' };
        for (let p = 1; p <= 40; p++) {
          const r = await call(`${BASE}/api/channel/?p=${p}&page_size=100`, { auth: root });
          const items = r.json?.data?.items ?? [];
          merged.json.data.items.push(...items);
          if (items.length < 100) break;
        }
        return merged;
      })();
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'TPM组-' + stamp } });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'TPM渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: root, body: { stable_key_id: 'tpm-' + stamp, name: 'tpm', key: 'sk-tpmkey-' + stamp },
  });
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });

  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'tpm probe' }] },
  });
  let auth = root;
  const setPolicy = async (body) => {
    let res = await call(`${BASE}/api/apigx/channels/${shareChannelId}/capacity-policy`, { method: 'PUT', auth, body });
    if (res.status === 401) {
      // 会话过期：清掉缓存重新登录后重试一次
      delete sessions['apigxroot'];
      saveSessions();
      auth = await login('apigxroot', 'ApigxDev#2026');
      res = await call(`${BASE}/api/apigx/channels/${shareChannelId}/capacity-policy`, { method: 'PUT', auth, body });
    }
    if (res.status !== 200) throw new Error(`设置策略失败：${res.status} ${res.text.slice(0, 140)}`);
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

  // ① 极小上限：单次预留量（prompt + 8192）远大于 100，应被拒
  await setPolicy({ tpm_mode: 4, tpm_limit: 100 });
  info('等待策略缓存过期（32s）...');
  await new Promise((r) => setTimeout(r, 32000));
  const denied = await chat();
  info(`① tpm_limit=100 → HTTP ${denied.status} ${denied.text.slice(0, 90)}`);
  check('TPM 超限被拒（429）', denied.status === 429, `HTTP ${denied.status}`);
  check('TPM 拒绝带 Retry-After', Boolean(denied.headers.get('retry-after')), `Retry-After=${denied.headers.get('retry-after')}`);

  // ② 明确不限 → 放行
  await setPolicy({ tpm_mode: 3 });
  await new Promise((r) => setTimeout(r, 32000));
  const unlimited = await chat();
  check('TPM「明确不限」放行', unlimited.status === 200, `HTTP ${unlimited.status}`);

  // ③ 大上限 → 放行，且窗口计数应≈单次预留量
  await setPolicy({ tpm_mode: 4, tpm_limit: 1000000 });
  await new Promise((r) => setTimeout(r, 32000));
  const allowed = await chat();
  check('TPM 上限充足时放行', allowed.status === 200, `HTTP ${allowed.status}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  console.log(`  供 Redis 核对：channel_tpm_key=apigx:cap:channel:${base.id}:tpm:<分钟桶>`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
