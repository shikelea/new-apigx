#!/usr/bin/env node
// T1.12 验收：拒绝清单（错误码 + 不泄露）
//
// 逐条验证四类拒绝的状态码与"不泄露"性质，并对照底座旧接口的已知偏离：
//   A. 未认证            → 401
//   B. 越权/不存在        → 404，且**与不存在的响应逐字节一致**（不泄露存在性）
//   C. 来源不允许         → 503 + 统一文案（不区分不存在、未发布、未授权或范围不符）
//   D. 容量超限           → 429 + Retry-After
//   E. 泄露扫描：上述所有拒绝响应都不得包含内部 id、上游地址、凭据、堆栈
//
// 用法：node tools/stage1-verify/rejection-matrix.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000, APIGX_CAPACITY_ENABLED=true)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const SOURCE_DENIAL = 'no available source for the requested model';

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
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function login(user, password) {
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败`);
  const auth = 'Bearer ' + r.json.data.access_token;
  const self = await call(`${BASE}/api/user/self`, { auth });
  return { auth, userId: self.json?.data?.id, tokenName: user };
}

async function makeChannel(auth, stamp, model, ownerAuth, shareGroupName) {
  const name = 'REJ底座-' + stamp;
  const created = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth,
    body: { mode: 'single', channel: { name, type: 1, key: 'placeholder', base_url: 'http://127.0.0.1:8100', models: model, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  if (created.status !== 200 || created.json?.success === false) throw new Error('建底座渠道失败：' + created.text.slice(0, 120));
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await (async () => {
          // 翻页合并：底座把 page_size 封顶在 100，只看第一页会"查不到刚建的渠道"（实测踩过）
          const merged = { json: { data: { items: [] } }, status: 200, text: '' };
          for (let p = 1; p <= 40; p++) {
            const r = await call(`${BASE}/api/channel/?p=${p}&page_size=100`, { auth: auth });
            const it = r.json?.data?.items ?? [];
            merged.json.data.items.push(...it);
            if (it.length < 100) break;
          }
          return merged;
        })();
    const items = list.json?.data?.items ?? [];
    base = items.find((c) => c?.name === name);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');
  const g = await call(`${BASE}/api/apigx/share-groups`, {
    method: 'POST', auth: ownerAuth,
    body: { name: shareGroupName, default_visibility: 1 },
  });
  if (g.status !== 200 || g.json?.data?.publish_status !== 2) {
    throw new Error('建已发布受限分组失败：' + g.text.slice(0, 120));
  }
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: ownerAuth,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'REJ渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  if (ch.status !== 200) throw new Error('建分享渠道失败：' + ch.text.slice(0, 120));
  const shareChannelId = ch.json.data.id;
  const k = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: ownerAuth, body: { stable_key_id: 'rej-' + stamp, name: 'rej', key: 'sk-rej-' + stamp },
  });
  if (k.status !== 200) throw new Error('加 Key 失败：' + k.text.slice(0, 120));
  const applied = await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: ownerAuth });
  if (applied.status !== 200) throw new Error('应用分享渠道失败：' + applied.text.slice(0, 120));

  const offering = await call(`${BASE}/api/apigx/channels/${shareChannelId}/offerings`, {
    method: 'POST', auth: ownerAuth, body: { upstream_model_id: model },
  });
  if (offering.status !== 200) throw new Error('建 Offering 失败：' + offering.text.slice(0, 120));
  const offeringId = offering.json.data.id;
  const priced = await call(`${BASE}/api/apigx/price-rules`, {
    method: 'PUT', auth,
    body: {
      scope_type: 0,
      scope_id: offeringId,
      input_usd_per_million: 1,
      output_usd_per_million: 2,
      lock: true,
    },
  });
  if (priced.status !== 200) throw new Error('Offering 配价失败：' + priced.text.slice(0, 120));
  const submitted = await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: ownerAuth });
  if (submitted.status !== 200) throw new Error('提交 Offering 失败：' + submitted.text.slice(0, 120));
  const published = await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth });
  if (published.status !== 200) throw new Error('发布 Offering 失败：' + published.text.slice(0, 120));
  return { baseChannelId: base.id, shareChannelId, offeringId };
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const userB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const bodies = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  const stamp = Date.now().toString().slice(-6);
  const recorded = (label, res) => { bodies.push({ label, text: res.text }); return res; };

  // ---- A. 未认证 → 401 ----
  const noAuth = recorded('A', await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', body: { model: 'stub/rej-' + stamp, messages: [{ role: 'user', content: 'x' }] },
  }));
  check('A 未认证 → 401', noAuth.status === 401, `HTTP ${noAuth.status}`);

  // ---- B. 越权/不存在 → 404 且与不存在一致 ----
  const chA = await makeChannel(root.auth, stamp, 'stub/rej-' + stamp, root.auth, 'REJ分组A-' + stamp);
  const other = recorded('B-越权', await call(`${BASE}/api/apigx/channels/${chA.shareChannelId}`, { auth: userB.auth }));
  const missing = recorded('B-不存在', await call(`${BASE}/api/apigx/channels/99999999`, { auth: userB.auth }));
  check('B 越权访问 → 404', other.status === 404, `HTTP ${other.status}`);
  check('B 与「不存在」逐字节一致（不泄露存在性）', other.text === missing.text,
    `越权=${other.text.slice(0, 40)} 不存在=${missing.text.slice(0, 40)}`);

  // ---- C. 来源不允许 → 统一 503 ----
  // 模型只由 root 的已发布分享资源提供；把 root 的平台 Key 限制为「只允许 B」后，
  // 必须返回不泄露具体原因的统一 503。
  const tokens = await call(`${BASE}/api/token/`, { auth: root.auth });
  const items = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items) ? items : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root.auth });
  const platformKey = keyRes.json?.data?.key;
  const setPrefs = (body) => call(`${BASE}/api/apigx/platform-keys/${vt.id}/preferences`, { method: 'PUT', auth: root.auth, body });

  // 新建渠道的模型→渠道路由能力缓存在网关内存里，默认 60s 才同步。
  // 先在默认范围下拿到真实 200，证明后续 503 来自准入拒绝，而不是模型尚未可路由。
  await setPrefs({ scope_mode: 0, allow_fallback: true });
  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: 'Bearer ' + platformKey,
      body: { model: 'stub/rej-' + stamp, messages: [{ role: 'user', content: 'warm' }] },
    });
    if (probe.status === 200) { routable = true; break; }
    if (!probe.text.includes('model_not_found')) {
      throw new Error(`模型预热失败：HTTP ${probe.status} ${probe.text.slice(0, 120)}`);
    }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('等待已发布 Offering 可路由超时（网关始终 model_not_found）');
  dim('模型已进入网关路由缓存');

  await setPrefs({ scope_mode: 1, allowed_owner_user_ids: [userB.userId], allow_fallback: false });

  const denied = recorded('C', await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: 'stub/rej-' + stamp, messages: [{ role: 'user', content: 'x' }] },
  }));
  info(`C 来源不允许 → HTTP ${denied.status} ${denied.text.slice(0, 110)}`);
  check('C 来源不允许 → 统一 503', denied.status === 503 && denied.text.includes(SOURCE_DENIAL), `HTTP ${denied.status}`);

  // ---- D. 容量超限 → 429 + Retry-After ----
  await setPrefs({ scope_mode: 0, allow_fallback: true }); // 复原来源限制，避免干扰容量判定
  await call(`${BASE}/api/apigx/channels/${chA.shareChannelId}/capacity-policy`, {
    method: 'PUT', auth: root.auth, body: { concurrency_mode: 4, concurrency_value: 1 },
  });
  // 策略缓存 TTL 30s：等它过期后再压测
  info('等待策略缓存过期（32s）...');
  await new Promise((r) => setTimeout(r, 32000));
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 1500, ttftMs: 0, status: 0, hang: false } });
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const burst = await Promise.all([0, 1, 2].map(() => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: 'stub/rej-' + stamp, messages: [{ role: 'user', content: 'x' }] },
  }).catch(() => ({ status: 'ERR', text: '', headers: new Map() }))));
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0 } });
  const rejected = burst.filter((r) => r.status === 429);
  const okCount = burst.filter((r) => r.status === 200).length;
  info(`D 并发 3（上限 1）→ 200×${okCount}，429×${rejected.length}`);
  check('D 容量超限 → 429', rejected.length === 2 && okCount === 1, `200×${okCount} / 429×${rejected.length}`);
  const retryAfter = rejected[0]?.headers?.get?.('retry-after');
  check('D 429 带 Retry-After', Boolean(retryAfter), `Retry-After=${retryAfter}`);
  rejected.forEach((r, i) => bodies.push({ label: 'D-' + i, text: r.text }));

  // ---- E. 泄露扫描 ----
  const forbidden = [
    { name: '内部渠道 id', pattern: new RegExp('"channel_id"\\s*:\\s*' + chA.baseChannelId) },
    { name: '上游地址', pattern: /127\.0\.0\.1:8100/ },
    { name: '凭据', pattern: /sk-rej-|enc:v1:/ },
    { name: '堆栈/文件路径', pattern: /\.go:\d+|goroutine \d+/ },
  ];
  for (const f of forbidden) {
    const hit = bodies.find((b) => f.pattern.test(b.text));
    check(`E 拒绝响应不含${f.name}`, !hit, hit ? `出现在 ${hit.label}: ${hit.text.slice(0, 80)}` : '未出现');
  }

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
