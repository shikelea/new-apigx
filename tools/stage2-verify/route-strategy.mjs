#!/usr/bin/env node
// T2.7 用户侧验收：**平台 Key 的默认路由策略真的按请求生效**
//
// 场景：同一模型两条健康供给（各有自己的上游 Key，便于从替身日志分辨）。
//   ① 把平台 Key 的 default_route_mode 设为 reliability → 请求应**只**落到质量更好的那条
//   ② 改回 auto → 请求应**两条都用到**（策略不生效时才会这样）
//
// 测量用替身自己的请求日志（按凭据指纹计数，日志不留明文；脚本本地复算指纹匹配）。
//
// 用法（分两阶段，中间由 PowerShell 用 psql 灌质量样本）：
//   node tools/stage2-verify/route-strategy.mjs --phase setup
//   node tools/stage2-verify/route-strategy.mjs --phase measure

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const PHASE = argOf('--phase', 'setup');
const ROUNDS = Number(argOf('--rounds', '10'));

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

const crypto = await import('node:crypto');
const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const OUT_FILE = path.join(STATE_DIR, 'route-strategy.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }
function saveOut(d) { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(OUT_FILE, JSON.stringify(d, null, 2)); }
function loadOut() { return JSON.parse(fs.readFileSync(OUT_FILE, 'utf8')); }

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

async function platformToken(root) {
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  return (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
}

function fingerprint(key) {
  const h = crypto.createHash('sha256').update(key).digest('hex').slice(0, 10);
  return `sha256:${h}/…${key.slice(-4)}`;
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');

  if (PHASE === 'setup') {
    const stamp = Date.now().toString().slice(-6);
    const MODEL = 'stub/strategy-' + stamp;
    const mkBase = async (tag) => {
      const name = 'STRAT-' + tag + '-' + stamp;
      await call(`${BASE}/api/channel/`, {
        method: 'POST', auth: root,
        body: { mode: 'single', channel: { name, type: 1, key: 'sk-' + tag + '-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
      });
      for (let i = 0; i < 10; i++) {
        const list = await call(`${BASE}/api/channel/?p=1&page_size=1000`, { auth: root });
        const hit = (list.json?.data?.items ?? []).find((c) => c?.name === name);
        if (hit) return hit;
        await new Promise((r) => setTimeout(r, 2000));
      }
      throw new Error('查不到底座渠道 ' + name);
    };
    const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'STRAT组-' + stamp, default_visibility: 2 } });
    const seed = async (tag) => {
      const baseChannel = await mkBase(tag);
      const upstreamKey = 'sk-' + tag + '-up-' + stamp;
      const ch = await call(`${BASE}/api/apigx/channels`, {
        method: 'POST', auth: root,
        body: { share_group_id: g.json.data.id, base_channel_id: baseChannel.id, name: 'STRAT渠道-' + tag + '-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
      });
      const channelId = ch.json.data.id;
      await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
        method: 'POST', auth: root, body: { stable_key_id: 'strat-' + tag + '-' + stamp, name: 'k', key: upstreamKey },
      });
      await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
      const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, {
        method: 'POST', auth: root, body: { upstream_model_id: MODEL },
      });
      const offeringId = off.json.data.id;
      await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
      await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });
      return { offeringId, channelId, upstreamKey };
    };
    const a = await seed('good');
    const b = await seed('meh');
    const vt = await platformToken(root);
    saveOut({ model: MODEL, stamp, tokenId: vt.id, a, b });
    info(`已建立两条供给：model=${MODEL}（A=good offering=${a.offeringId}，B=meh offering=${b.offeringId}）`);
    info('下一步：psql 给 A 灌更好的质量样本，然后执行 --phase measure');
    return;
  }

  const out = loadOut();
  const vt = { id: out.tokenId };
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;
  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: out.model, messages: [{ role: 'user', content: 'strategy probe' }] },
  });
  const setMode = async (mode) => {
    const r = await call(`${BASE}/api/apigx/platform-keys/${vt.id}/preferences`, {
      method: 'PUT', auth: root, body: { default_route_mode: mode },
    });
    if (r.status !== 200) throw new Error(`设置路由策略失败：${r.status} ${r.text.slice(0, 140)}`);
    info(`已把平台 Key 的默认路由策略设为 ${mode || '(空=自动)'}`);
  };
  const counts = async () => {
    const log = await call(`${STUB}/_control/requests?limit=200`);
    const entries = log.json?.requests ?? [];
    const fpA = fingerprint(out.a.upstreamKey); const fpB = fingerprint(out.b.upstreamKey);
    let a = 0; let b = 0;
    for (const entry of entries) {
      if (entry?.upstreamKey === fpA) a++;
      else if (entry?.upstreamKey === fpB) b++;
    }
    return { a, b };
  };
  const fire = async (n) => {
    await call(`${STUB}/_control/reset`, { method: 'POST' });
    const statuses = [];
    for (let i = 0; i < n; i++) {
      const r = await chat();
      statuses.push(r.status);
      await new Promise((res) => setTimeout(res, 120));
    }
    return statuses;
  };

  // 等模型可路由
  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('模型不可路由');

  // ① 策略 = reliability → 只应落到质量更好的 A
  await setMode('reliability');
  // 策略合格集合有 30 秒缓存：切换后等它过期
  info('等待策略缓存过期（32s）...');
  await new Promise((r) => setTimeout(r, 32000));
  const statuses1 = await fire(ROUNDS);
  const c1 = await counts();
  info(`reliability 模式：A=${c1.a} B=${c1.b}（状态 ${[...new Set(statuses1)].join(',')}）`);
  (statuses1.every((s) => s === 200) ? pass : fail)(`reliability 模式下请求全部成功 — ${statuses1.filter((s) => s === 200).length}/${ROUNDS}`);
  (c1.b === 0 && c1.a > 0 ? pass : fail)(`策略生效：流量只落到更优来源 — A=${c1.a} B=${c1.b}`);

  // ② 策略 = auto → 不再限定，两条都应被用到
  await setMode('auto');
  await new Promise((r) => setTimeout(r, 32000));
  const statuses2 = await fire(ROUNDS);
  const c2 = await counts();
  info(`auto 模式：A=${c2.a} B=${c2.b}（状态 ${[...new Set(statuses2)].join(',')}）`);
  (c2.a > 0 && c2.b > 0 ? pass : fail)(`auto 模式不加限定：两条来源都被用到 — A=${c2.a} B=${c2.b}`);

  // ③ 拼错的策略名必须被拒（否则用户以为设了、实际永远不生效）
  const bad = await call(`${BASE}/api/apigx/platform-keys/${vt.id}/preferences`, {
    method: 'PUT', auth: root, body: { default_route_mode: 'latencyy' },
  });
  (bad.status === 400 ? pass : fail)(`未知策略名被拒（不静默失效） — HTTP ${bad.status}`);
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
