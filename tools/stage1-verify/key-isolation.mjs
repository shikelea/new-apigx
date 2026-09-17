#!/usr/bin/env node
// T1.5 端到端验收：Key 级故障隔离在**真实请求路径**上是否生效
//
// 走完整链路：分享者建渠道 → 加两把 Key → apply 投影到底座渠道 →
// 让上游对第一次请求返回 401 → 再请求一次 → 观察：
//   ① 被拒的那把 Key 被隔离（status=invalid）
//   ② 另一把 Key 仍然服务（第二次请求 200）
//   ③ 底座按索引跳过失效 Key，而不是让整个渠道停摆
//
// 用法：node tools/stage1-verify/key-isolation.mjs
// 前置：PostgreSQL(5433) + Redis(6379) + 替身(8100) + 网关(3000, APIGX_KEY_STATS_ENABLED=true)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const MODEL = argOf('--model', 'stub/model-x-202609');
const KEY_A = argOf('--key-a', 'sk-iso-AAAA-1111111111');
const KEY_B = argOf('--key-b', 'sk-iso-BBBB-2222222222');

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
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function login(user, password) {
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 120)}`);
  return `Bearer ${r.json.data.access_token}`;
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  const stamp = Date.now().toString().slice(-6);

  // ---- 1) 建一条底座的影子渠道（指向替身，先放占位 Key）----
  const baseChannel = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: {
      mode: 'single',
      channel: {
        name: `ISO底座-${stamp}`, type: 1, key: 'placeholder-will-be-replaced',
        base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'],
        status: 1, priority: 0, weight: 1,
      },
    },
  });
  if (baseChannel.status !== 200 || baseChannel.json?.success === false) {
    throw new Error(`建底座渠道失败：HTTP ${baseChannel.status} ${baseChannel.text.slice(0, 160)}`);
  }
  // 底座的建渠道接口只回 {success:true}，不回 id —— 按名字查回来（不猜响应结构）
  const channelName = `ISO底座-${stamp}`;
  const listRes = await call(`${BASE}/api/channel/?page=1&page_size=200`, { auth: root });
  const allChannels = listRes.json?.data?.items ?? listRes.json?.data ?? [];
  const created = (Array.isArray(allChannels) ? allChannels : []).find((c) => c?.name === channelName);
  if (!created?.id) {
    throw new Error(`建好的底座渠道查不到：HTTP ${listRes.status} ${listRes.text.slice(0, 200)}`);
  }
  const baseChannelId = created.id;
  info(`底座影子渠道 id=${baseChannelId}（${channelName}）`);

  // ---- 2) 分享者建分组 + 渠道（绑定该底座渠道）+ 两把 Key ----
  let r = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: `ISO分组-${stamp}` } });
  if (r.status !== 200) throw new Error(`建分组失败：HTTP ${r.status} ${r.text.slice(0, 160)}`);
  const groupId = r.json.data.id;

  r = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: groupId, base_channel_id: baseChannelId, name: `ISO渠道-${stamp}`, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  if (r.status !== 200) throw new Error(`建分享渠道失败：HTTP ${r.status} ${r.text.slice(0, 160)}`);
  const shareChannelId = r.json.data.id;
  info(`分享渠道 id=${shareChannelId}`);

  for (const [stable, secret] of [['iso-a', KEY_A], ['iso-b', KEY_B]]) {
    const res = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
      method: 'POST', auth: root,
      body: { stable_key_id: `${stable}-${stamp}`, name: stable, key: secret },
    });
    if (res.status !== 200) throw new Error(`加 Key ${stable} 失败：HTTP ${res.status} ${res.text.slice(0, 160)}`);
  }

  // ---- 3) 应用（投影到底座渠道）----
  r = await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });
  check('投影到底座渠道（apply）', r.status === 200 && r.json?.data?.applied_keys === 2, `HTTP ${r.status} applied=${r.json?.data?.applied_keys}`);

  // ---- 4) 取一把平台令牌用于发请求 ----
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  const verifyToken = (Array.isArray(items) ? items : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  if (!verifyToken) throw new Error('找不到可用令牌');
  const keyRes = await call(`${BASE}/api/token/${verifyToken.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;
  if (!platformKey) throw new Error('取不到令牌明文');
  dim(`使用令牌 id=${verifyToken.id}`);

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: `Bearer ${platformKey}`,
    body: { model: MODEL, messages: [{ role: 'user', content: 'key isolation probe' }] },
  });

  // ---- 5) 第一次请求：让上游返回 401 ----
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { delayMs: 0, status: 401 } });
  const first = await chat();
  info(`第一次（上游 401）→ 客户端 HTTP ${first.status}`);
  check('上游 401 时请求失败', first.status !== 200, `HTTP ${first.status}`);

  // ---- 6) 第二次请求：上游恢复正常 ----
  await call(`${STUB}/_control/profile`, { method: 'POST', body: { status: 0 } });
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const second = await chat();
  const stubHits = (await call(`${STUB}/_control/requests?limit=10`)).json?.requests?.length ?? 0;
  info(`第二次 → 客户端 HTTP ${second.status}，替身收到 ${stubHits} 次`);
  check('上游恢复后请求成功（另一把 Key 仍在服务）', second.status === 200, `HTTP ${second.status}`);

  // ---- 7) 从接口观察 Key 状态 ----
  const list = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, { auth: root });
  const keys = list.json?.data?.items ?? [];
  check('Key 列表可读', keys.length === 2, `共 ${keys.length} 把`);
  const statuses = keys.map((k) => `${k.stable_key_id}:status=${k.status}:usable=${k.usable}`).join(' | ');
  info(statuses);
  const invalidCount = keys.filter((k) => k.status === 2).length;
  check('有一把 Key 被隔离（status=invalid）', invalidCount === 1, `invalid 数量=${invalidCount}`);
  check('另一把 Key 仍可用', keys.filter((k) => k.usable === true).length === 1, `可用数量=${keys.filter((k) => k.usable === true).length}`);

  // ---- 8) 响应中绝不含凭据 ----
  check('接口响应不含上游凭据', !list.text.includes(KEY_A) && !list.text.includes(KEY_B) && !list.text.includes('enc:v1:'));

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  console.log(`  供 psql 核对：base_channel_id=${baseChannelId} share_channel_id=${shareChannelId}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
