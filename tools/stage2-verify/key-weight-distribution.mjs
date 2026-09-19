#!/usr/bin/env node
// T2.10 验收：**Key 权重真的影响上游流量分配**（T2.6）
//
// 做法：一条分享渠道挂两把上游 Key（sk-a / sk-b），权重设成 3:1。
//   每把真实 Key 只投影一个槽位，底座在选 Key 时读取原生权重，于是真实请求按 3:1 落在两把 Key 上。
//   测量手段：**替身自己记录的请求日志**（每秒请求的授权头都留了下来），
//   因此看到的是"上游实际收到哪把 Key"，而不是我们自己算出来的数字。
//
// 用法（分两阶段，setup 通过公开 API 写入权重）：
//   node tools/stage2-verify/key-weight-distribution.mjs --phase setup
//   node tools/stage2-verify/key-weight-distribution.mjs --phase measure --rounds 16

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const PHASE = argOf('--phase', 'setup');
const ROUNDS = Number(argOf('--rounds', '16'));
const SELF_TEST_FAILURE = argv.includes('--self-test-failure');

async function call(url, { method = 'GET', body, auth } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

let failureCount = 0;
const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => { failureCount++; console.log('  \x1b[31mFAIL\x1b[0m  ' + s); };
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const expectOK = (response, label) => {
  if (response.status !== 200 || response.json?.success === false || !response.json?.data) {
    throw new Error(`${label}失败：HTTP ${response.status} ${response.text.slice(0, 180)}`);
  }
  return response.json.data;
};

const crypto = await import('node:crypto');
const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const KW_FILE = path.join(STATE_DIR, 'key-weight-dist.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }
function saveKw(data) { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(KW_FILE, JSON.stringify(data, null, 2)); }
function loadKw() { return JSON.parse(fs.readFileSync(KW_FILE, 'utf8')); }

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

async function platformKey(root) {
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  return keyRes.json?.data?.key;
}

async function main() {
  if (SELF_TEST_FAILURE) {
    fail('受控失败自测：验证脚本必须返回退出码 1');
    return;
  }
  const root = await login('apigxroot', 'ApigxDev#2026');

  if (PHASE === 'setup') {
    const stamp = Date.now().toString().slice(-6);
    const MODEL = 'stub/keyweight-' + stamp;
    const channelName = 'KW底座-' + stamp;
    await call(`${BASE}/api/channel/`, {
      method: 'POST', auth: root,
      body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-kw-ignored-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
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

    const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'KW组-' + stamp, default_visibility: 2 } });
    const group = expectOK(g, '创建分享分组');
    const groupPublished = await call(`${BASE}/api/apigx/share-groups/${group.id}/publish-status`, {
      method: 'POST', auth: root, body: { status: 2 },
    });
    expectOK(groupPublished, '发布分享分组');
    const ch = await call(`${BASE}/api/apigx/channels`, {
      method: 'POST', auth: root,
      body: { share_group_id: group.id, base_channel_id: base.id, name: 'KW渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100', key_pool_mode: 0 },
    });
    const channelId = expectOK(ch, '创建分享渠道').id;

    // 两把上游 Key，凭据用**可辨认**的值，便于从替身日志里区分
    const keyA = 'sk-kw-A-' + stamp;
    const keyB = 'sk-kw-B-' + stamp;
    for (const [stable, secret, weight] of [['kw-a', keyA, 3], ['kw-b', keyB, 1]]) {
      const r = await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
        method: 'POST', auth: root, body: { stable_key_id: stable + '-' + stamp, name: stable, key: secret, weight },
      });
      if (r.status !== 200) throw new Error('建 Key 失败：' + r.text.slice(0, 140));
    }
    const applied = await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
    expectOK(applied, '投影分享渠道');
    const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, {
      method: 'POST', auth: root, body: { upstream_model_id: MODEL },
    });
    const offeringId = expectOK(off, '创建供给').id;
    const price = await call(`${BASE}/api/apigx/price-rules`, {
      method: 'PUT', auth: root,
      body: { scope_type: 0, scope_id: offeringId, input_usd_per_million: 1, output_usd_per_million: 2 },
    });
    expectOK(price, '配置供给价格');
    const submitted = await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
    expectOK(submitted, '提交供给');
    const published = await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, {
      method: 'POST', auth: root, body: { reason_code: 'stage_c_key_weight_verification' },
    });
    expectOK(published, '发布供给');

    saveKw({ model: MODEL, stamp, channelId, baseChannelId: base.id, keyA, keyB });
    info(`已建立渠道与两把 Key：channel=${channelId} base=${base.id}`);
    info('权重已通过 API 写为 A=3 / B=1；等待模型同步后执行 --phase measure');
    return;
  }

  if (PHASE === 'measure') {
    const kw = loadKw();
    // 重新投影：确认底座单槽位投影携带当前原生权重，不复制明文 Key。
    const applied = await call(`${BASE}/api/apigx/channels/${kw.channelId}/apply`, { method: 'POST', auth: root });
    if (applied.status !== 200) throw new Error('重投影失败：' + applied.text.slice(0, 140));
    info('已按原生权重重新投影（每把真实 Key 仍只有一个槽位）');

    const root2 = root;
    const key = await platformKey(root2);
    const chat = () => call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: 'Bearer ' + key,
      body: { model: kw.model, messages: [{ role: 'user', content: 'key weight probe' }] },
    });

    let routable = false;
    for (let i = 0; i < 24 && !routable; i++) {
      const probe = await chat();
      if (!probe.text.includes('model_not_found')) { routable = true; break; }
      await new Promise((r) => setTimeout(r, 5000));
    }
    if (!routable) throw new Error('模型不可路由');

    // 清空替身日志，只统计本轮
    await call(`${STUB}/_control/reset`, { method: 'POST' });
    const statuses = [];
    for (let i = 0; i < ROUNDS; i++) {
      const r = await chat();
      statuses.push(r.status);
      await new Promise((res) => setTimeout(res, 120));
    }
    const okCount = statuses.filter((s) => s === 200).length;
    (okCount === ROUNDS ? pass : fail)(`请求全部成功 — ${okCount}/${ROUNDS}`);

    // 从**替身自己的日志**里数每把 Key 收到多少次（这是上游侧的事实）。
    //
    // 注意：替身不存原始凭据，只存**指纹**（`sha256:前10位/…后4位`）——这是对的
    // （日志里不该留明文）。因此这里按它的规则**本地复算**指纹再匹配，
    // 既不要求替身记录明文，也能确定地分辨两把 Key。
    const log = await call(`${STUB}/_control/requests?limit=200`);
    const entries = log.json?.requests ?? [];
    const fp = (key) => {
      const h = crypto.createHash('sha256').update(key).digest('hex').slice(0, 10);
      return `sha256:${h}/…${key.slice(-4)}`;
    };
    const fpA = fp(kw.keyA); const fpB = fp(kw.keyB);
    let countA = 0; let countB = 0;
    for (const entry of entries) {
      if (entry?.upstreamKey === fpA) countA++;
      else if (entry?.upstreamKey === fpB) countB++;
    }
    info(`上游侧统计（替身日志）：A=${countA} 次，B=${countB} 次（共 ${entries.length} 条记录）`);
    if (countA + countB === 0) {
      fail('替身日志里没有可识别的 Key（检查凭据是否被中继改写）');
    } else {
      const ratio = countB === 0 ? Infinity : countA / countB;
      // 权重 3:1 → 期望 A 明显多于 B。样本有限，只断言"方向正确且 A 占多数"，
      // 不对精确比例下结论（底座的轮询还受游标起始位置影响）。
      const okDirection = countA > countB;
      (okDirection ? pass : fail)(`流量按权重倾斜（A 应明显多于 B） — A=${countA} B=${countB} 比值=${ratio === Infinity ? '∞' : ratio.toFixed(2)}`);
      (countA >= ROUNDS * 0.5 ? pass : fail)(`高权重 Key 承担多数流量 — A 占 ${Math.round((countA / (countA + countB)) * 100)}%`);
    }
    return;
  }

  throw new Error('未知阶段：' + PHASE);
}

main()
  .then(() => { if (failureCount > 0) process.exitCode = 1; })
  .catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
