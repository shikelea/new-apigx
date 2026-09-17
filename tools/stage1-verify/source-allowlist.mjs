#!/usr/bin/env node
// T1.10 验收：平台 Key 的「允许来源」与回退语义（执行侧）
//
// 场景：某模型**只由分享者 B 的渠道**提供。根用户（分享者 A）的平台 Key 配置「允许来源」：
//   ① allowed=[A]（不含 B）+ 不可回退 → 必须**拒绝**（不能借道不在允许列表里的来源）
//   ② allowed=[B] + 不可回退           → 必须**放行**
//   ③ allowed=[A] + 可回退             → 仍然**拒绝**：回退只是允许底座重选，
//     并不会绕过限制（候选里没有合格来源时不能成功）——这一点最容易被误解，必须验证
//
// 用法：node tools/stage1-verify/source-allowlist.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000)

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
  return { status: res.status, json, text };
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function login(user, password) {
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status}`);
  const auth = 'Bearer ' + r.json.data.access_token;
  // 登录响应里没有用户 id，必须另外取（否则允许列表会写成 null，测试会"因为错误的原因"通过）
  const self = await call(`${BASE}/api/user/self`, { auth });
  const userId = self.json?.data?.id;
  if (!userId) throw new Error(`${user} 取不到用户 id`);
  return { auth, userId };
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const userB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  const stamp = Date.now().toString().slice(-6);
  const MODEL = 'stub/src-' + stamp;

  // 1) 底座渠道（由 B 的分享渠道绑定）
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root.auth,
    body: { mode: 'single', channel: { name: 'SRC底座-' + stamp, type: 1, key: 'placeholder', base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  const listRes = await (async () => {
    // 翻页合并：page_size 封顶 100，只看第一页会"查不到刚建的渠道"
    const merged = { json: { data: { items: [] } }, status: 200, text: '' };
    for (let p = 1; p <= 40; p++) {
      const r = await call(`${BASE}/api/channel/?p=${p}&page_size=100`, { auth: root.auth });
      const it = r.json?.data?.items ?? [];
      merged.json.data.items.push(...it);
      if (it.length < 100) break;
    }
    return merged;
  })();
  const all = listRes.json?.data?.items ?? listRes.json?.data ?? [];
  const baseChannel = (Array.isArray(all) ? all : []).find((c) => c?.name === 'SRC底座-' + stamp);
  if (!baseChannel?.id) throw new Error('查不到刚建的底座渠道');

  // 2) **B** 建分组与渠道（这样渠道的所有者是 B）
  let r = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: userB.auth, body: { name: 'SRC分组-' + stamp } });
  if (r.status !== 200) throw new Error('B 建分组失败：' + r.text.slice(0, 160));
  const groupId = r.json.data.id;
  r = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: userB.auth,
    body: { share_group_id: groupId, base_channel_id: baseChannel.id, name: 'SRC渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  if (r.status !== 200) throw new Error('B 建渠道失败：' + r.text.slice(0, 160));
  const shareChannelId = r.json.data.id;
  r = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: userB.auth,
    body: { stable_key_id: 'src-key-' + stamp, name: 'src', key: 'sk-src-' + stamp },
  });
  if (r.status !== 200) throw new Error('B 加 Key 失败：' + r.text.slice(0, 160));
  r = await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: userB.auth });
  if (r.status !== 200) throw new Error('apply 失败：' + r.text.slice(0, 160));
  dim(`渠道所有者 = B(userId=${userB.userId})，模型 ${MODEL} 仅由该渠道提供`);

  // 3) 根用户的平台 Key
  const tokens = await call(`${BASE}/api/token/`, { auth: root.auth });
  const items = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  const vt = (Array.isArray(items) ? items : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root.auth });
  const platformKey = keyRes.json?.data?.key;
  if (!platformKey) throw new Error('取不到令牌明文');
  const tokenId = vt.id;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'source probe' }] },
  });

  const setPrefs = async (body) => {
    const res = await call(`${BASE}/api/apigx/platform-keys/${tokenId}/preferences`, { method: 'PUT', auth: root.auth, body });
    if (res.status !== 200) throw new Error(`设置偏好失败：${res.status} ${res.text.slice(0, 160)}`);
    return res.json.data;
  };

  // 先确认默认（未限制来源）时可以成功——证明链路本身通
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  let warm = null;
  for (let i = 0; i < 20; i++) {
    warm = await chat();
    if (!warm.text.includes('model_not_found')) break;
    await new Promise((res) => setTimeout(res, 5000));
  }
  check('默认（未限制来源）可正常调用', warm?.status === 200, `HTTP ${warm?.status}`);

  // ① allowed=[A]（不含 B）+ 不可回退 → 拒绝
  const rootId = root.userId;
  await setPrefs({ scope_mode: 1, allowed_owner_user_ids: [rootId], allow_fallback: false });
  const denied = await chat();
  info(`① allowed=[A] 不可回退 → HTTP ${denied.status} ${denied.text.slice(0, 80)}`);
  check('不在允许列表的来源被拒绝', denied.status !== 200, `HTTP ${denied.status}`);

  // ② allowed=[B] → 放行
  await setPrefs({ scope_mode: 1, allowed_owner_user_ids: [userB.userId], allow_fallback: false });
  const allowed = await chat();
  info(`② allowed=[B] → HTTP ${allowed.status}`);
  check('允许列表内的来源被放行', allowed.status === 200, `HTTP ${allowed.status}`);

  // ③ allowed=[A] + 可回退 → 仍拒绝（回退不能绕过限制）
  await setPrefs({ scope_mode: 1, allowed_owner_user_ids: [rootId], allow_fallback: true });
  const fallbackOnly = await chat();
  info(`③ allowed=[A] 可回退 → HTTP ${fallbackOnly.status} ${fallbackOnly.text.slice(0, 80)}`);
  check('可回退不等于绕过限制（候选里没有合格来源时仍失败）', fallbackOnly.status !== 200, `HTTP ${fallbackOnly.status}`);

  // ④ 复原为默认，确认恢复可用
  await setPrefs({ scope_mode: 0, allow_fallback: true });
  const restored = await chat();
  check('复原默认后恢复可用', restored.status === 200, `HTTP ${restored.status}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
