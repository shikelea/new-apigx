#!/usr/bin/env node
// B4 用户侧验收：**平台 Key 的默认路由策略真的按请求生效**
//
// 场景：同一模型两条健康供给（各有自己的上游 Key，便于从替身日志分辨）。
//   ① 空值 / auto / reliability / latency / throughput 都应只选择质量更好的 A
//   ② A 的渠道并发占满时，auto 应立即改选仍有余量的 B
//   ③ A 释放后，下一请求应立即恢复选择 A（不能命中旧的最终路由结果缓存）
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

function expectOK(result, action) {
  if (result.status !== 200 || result.json?.success === false) {
    throw new Error(`${action}失败：HTTP ${result.status} ${result.text.slice(0, 180)}`);
  }
  return result.json?.data ?? result.json;
}

let failureCount = 0;
const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => { failureCount++; console.log('  \x1b[31mFAIL\x1b[0m  ' + s); };
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
  if (SELF_TEST_FAILURE) {
    fail('受控失败自测：验证脚本必须返回退出码 1');
    return;
  }
  const root = await login('apigxroot', 'ApigxDev#2026');

  if (PHASE === 'setup') {
    const stamp = Date.now().toString().slice(-6);
    const MODEL = 'stub/strategy-' + stamp;
    const mkBase = async (tag) => {
      const name = 'STRAT-' + tag + '-' + stamp;
      const created = await call(`${BASE}/api/channel/`, {
        method: 'POST', auth: root,
        body: { mode: 'single', channel: { name, type: 1, key: 'sk-' + tag + '-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
      });
      expectOK(created, `创建底座渠道 ${tag}`);
      for (let i = 0; i < 10; i++) {
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
        const hit = (list.json?.data?.items ?? []).find((c) => c?.name === name);
        if (hit) return hit;
        await new Promise((r) => setTimeout(r, 2000));
      }
      throw new Error('查不到底座渠道 ' + name);
    };
    const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'STRAT组-' + stamp, default_visibility: 2 } });
    const group = expectOK(g, '创建分享分组');
    const groupPublished = await call(`${BASE}/api/apigx/share-groups/${group.id}/publish-status`, {
      method: 'POST', auth: root, body: { status: 2 },
    });
    expectOK(groupPublished, '发布分享分组');
    const seed = async (tag) => {
      const baseChannel = await mkBase(tag);
      const upstreamKey = 'sk-' + tag + '-up-' + stamp;
      const ch = await call(`${BASE}/api/apigx/channels`, {
        method: 'POST', auth: root,
        body: { share_group_id: group.id, base_channel_id: baseChannel.id, name: 'STRAT渠道-' + tag + '-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
      });
      const channelId = expectOK(ch, `创建分享渠道 ${tag}`).id;
      const key = await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
        method: 'POST', auth: root, body: { stable_key_id: 'strat-' + tag + '-' + stamp, name: 'k', key: upstreamKey },
      });
      expectOK(key, `添加上游 Key ${tag}`);
      const applied = await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
      expectOK(applied, `投影渠道 ${tag}`);
      const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, {
        method: 'POST', auth: root, body: { upstream_model_id: MODEL },
      });
      const offeringId = expectOK(off, `创建供给 ${tag}`).id;
      const price = await call(`${BASE}/api/apigx/price-rules`, {
        method: 'PUT', auth: root,
        body: { scope_type: 0, scope_id: offeringId, input_usd_per_million: 1, output_usd_per_million: 2 },
      });
      expectOK(price, `配置供给价格 ${tag}`);
      const submitted = await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
      expectOK(submitted, `提交供给 ${tag}`);
      const published = await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, {
        method: 'POST', auth: root, body: { reason_code: 'b4_route_verification' },
      });
      expectOK(published, `发布供给 ${tag}`);
      return { offeringId, channelId, upstreamKey };
    };
    const a = await seed('good');
    const b = await seed('meh');
    const vt = await platformToken(root);
    if (!vt?.id) throw new Error('找不到用于验收的平台 Key');
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

  const assertOnlyA = async (mode) => {
    await setMode(mode);
    const statuses = await fire(ROUNDS);
    const count = await counts();
    const label = mode || '(空=auto)';
    info(`${label} 模式：A=${count.a} B=${count.b}（状态 ${[...new Set(statuses)].join(',')}）`);
    (statuses.every((s) => s === 200) ? pass : fail)(`${label} 模式下请求全部成功 — ${statuses.filter((s) => s === 200).length}/${ROUNDS}`);
    (count.a === ROUNDS && count.b === 0 ? pass : fail)(`${label} 只选择综合更优的 A — A=${count.a} B=${count.b}`);
  };

  const waitForARequest = async () => {
    for (let i = 0; i < 40; i++) {
      const count = await counts();
      if (count.a > 0) return;
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    throw new Error('等待 A 到达替身超时');
  };

  // 等模型可路由
  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('模型不可路由');

  // ① 默认综合自动和三种显式质量策略，都必须只选择质量更好的 A。
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0, status: 0, hang: false } });
  for (const mode of ['', 'auto', 'reliability', 'latency', 'throughput']) {
    await assertOnlyA(mode);
  }

  // ② 拼错的策略名必须被拒（否则用户以为设了、实际永远不生效）
  const bad = await call(`${BASE}/api/apigx/platform-keys/${vt.id}/preferences`, {
    method: 'PUT', auth: root, body: { default_route_mode: 'latencyy' },
  });
  (bad.status === 400 ? pass : fail)(`未知策略名被拒（不静默失效） — HTTP ${bad.status}`);

  // ③ A 质量更好，但它只有 1 个并发槽。先用一个延迟请求占满 A，第二个 auto
  // 请求必须在决策阶段看见实时占用并改选 B；释放后再立即恢复 A。
  const policy = await call(`${BASE}/api/apigx/channels/${out.a.channelId}/capacity-policy`, {
    method: 'PUT', auth: root, body: { concurrency_mode: 4, concurrency_value: 1 },
  });
  if (policy.status !== 200) throw new Error(`设置 A 并发上限失败：${policy.status} ${policy.text.slice(0, 140)}`);
  await setMode('auto');
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 1500, status: 0, hang: false } });

  const holding = chat();
  await waitForARequest();
  const fallback = await chat();
  const held = await holding;
  const during = await counts();
  info(`A 满载期间：A=${during.a} B=${during.b}（首请求 ${held.status}，并发请求 ${fallback.status}）`);
  (held.status === 200 && fallback.status === 200 ? pass : fail)('A 满载期间两次请求均由可用渠道完成');
  (during.a === 1 && during.b === 1 ? pass : fail)(`A 满载后 auto 改选 B — A=${during.a} B=${during.b}`);

  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0 } });
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const recovered = await chat();
  const after = await counts();
  info(`A 释放后：A=${after.a} B=${after.b}（状态 ${recovered.status}）`);
  (recovered.status === 200 ? pass : fail)('A 释放后的下一请求成功');
  (after.a === 1 && after.b === 0 ? pass : fail)(`没有旧最终结果缓存：下一请求立即恢复 A — A=${after.a} B=${after.b}`);
}

main()
  .then(() => { if (failureCount > 0) process.exitCode = 1; })
  .catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
