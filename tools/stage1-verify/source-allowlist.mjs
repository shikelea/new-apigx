#!/usr/bin/env node
// B1 验收：资源 ACL 与平台 Key「允许来源」必须同时满足。
//
// 场景：管理员 A 发布一个受限分享资源，普通用户 B 使用自己的平台 Key：
//   ① A 授权 B，Key 也允许 A      → 放行
//   ② A 授权 B，Key 只允许 B      → 503（平台 Key 范围拒绝）
//   ③ Key 允许 A，但 A 撤销授权    → 503（资源 ACL 拒绝）
//   ④ 允许回退也不能绕过任一限制  → 仍为统一 503
//
// 用法：node tools/stage1-verify/source-allowlist.mjs
// 前置：PG + Redis + 本地替身(8100) + 网关(3000)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const SOURCE_DENIAL = 'no available source for the requested model';

async function call(url, { method = 'GET', body, auth } = {}) {
  const headers = { accept: 'application/json' };
  if (body !== undefined) headers['content-type'] = 'application/json';
  if (auth) headers.authorization = auth;
  const res = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

async function login(user, password) {
  const response = await call(`${BASE}/api/user/login`, {
    method: 'POST',
    body: { username: user, password },
  });
  if (!response.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${response.status}`);
  const auth = 'Bearer ' + response.json.data.access_token;
  const self = await call(`${BASE}/api/user/self`, { auth });
  const userId = self.json?.data?.id;
  if (!userId) throw new Error(`${user} 取不到用户 id`);
  return { auth, userId };
}

async function findBaseChannel(auth, name) {
  for (let attempt = 0; attempt < 10; attempt++) {
    for (let page = 1; page <= 40; page++) {
      const response = await call(`${BASE}/api/channel/?p=${page}&page_size=100`, { auth });
      const items = response.json?.data?.items ?? [];
      const found = items.find((channel) => channel?.name === name);
      if (found) return found;
      if (items.length < 100) break;
    }
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  return null;
}

async function createToken(auth, name) {
  const created = await call(`${BASE}/api/token/`, {
    method: 'POST',
    auth,
    body: {
      name,
      remain_quota: 0,
      unlimited_quota: true,
      expired_time: -1,
      model_limits_enabled: false,
    },
  });
  if (created.status !== 200 || created.json?.success === false) {
    throw new Error(`创建平台 Key 失败：${created.status} ${created.text.slice(0, 160)}`);
  }
  const listed = await call(`${BASE}/api/token/`, { auth });
  const items = listed.json?.data?.items ?? listed.json?.data ?? [];
  const token = (Array.isArray(items) ? items : []).find((item) => item?.name === name);
  if (!token?.id) throw new Error('找不到刚创建的平台 Key');
  const revealed = await call(`${BASE}/api/token/${token.id}/key`, { method: 'POST', auth });
  const key = revealed.json?.data?.key;
  if (!key) throw new Error('取不到平台 Key 明文');
  return { id: token.id, key };
}

function isSourceDenied(response) {
  return response?.status === 503 && response.text.includes(SOURCE_DENIAL);
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const userB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const check = (name, ok, detail) => {
    results.push({ name, ok });
    (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`);
  };
  const stamp = Date.now().toString().slice(-6);
  const model = 'stub/src-' + stamp;
  const baseName = 'SRC底座-' + stamp;

  // A 创建底座渠道及受限分享资源。受限分组创建后即处于已发布状态；
  // Offering 仍必须经过配价、提交和平台发布，避免用未发布资源制造假通过。
  const baseCreated = await call(`${BASE}/api/channel/`, {
    method: 'POST',
    auth: root.auth,
    body: {
      mode: 'single',
      channel: {
        name: baseName,
        type: 1,
        key: 'placeholder',
        base_url: 'http://127.0.0.1:8100',
        models: model,
        groups: ['default'],
        status: 1,
        priority: 0,
        weight: 1,
      },
    },
  });
  if (baseCreated.status !== 200 || baseCreated.json?.success === false) {
    throw new Error('建底座渠道失败：' + baseCreated.text.slice(0, 160));
  }
  const baseChannel = await findBaseChannel(root.auth, baseName);
  if (!baseChannel?.id) throw new Error('查不到刚建的底座渠道');

  let response = await call(`${BASE}/api/apigx/share-groups`, {
    method: 'POST',
    auth: root.auth,
    body: { name: 'SRC受限分组-' + stamp, default_visibility: 1 },
  });
  if (response.status !== 200 || response.json?.data?.publish_status !== 2) {
    throw new Error('建已发布受限分组失败：' + response.text.slice(0, 160));
  }
  const groupId = response.json.data.id;

  response = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST',
    auth: root.auth,
    body: {
      share_group_id: groupId,
      base_channel_id: baseChannel.id,
      name: 'SRC渠道-' + stamp,
      protocol: 'openai',
      base_url: 'http://127.0.0.1:8100',
    },
  });
  if (response.status !== 200) throw new Error('建分享渠道失败：' + response.text.slice(0, 160));
  const shareChannelId = response.json.data.id;

  response = await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST',
    auth: root.auth,
    body: { stable_key_id: 'src-key-' + stamp, name: 'src', key: 'sk-src-' + stamp },
  });
  if (response.status !== 200) throw new Error('加 Key 失败：' + response.text.slice(0, 160));
  response = await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, {
    method: 'POST',
    auth: root.auth,
  });
  if (response.status !== 200) throw new Error('应用分享渠道失败：' + response.text.slice(0, 160));

  response = await call(`${BASE}/api/apigx/channels/${shareChannelId}/offerings`, {
    method: 'POST',
    auth: root.auth,
    body: { upstream_model_id: model, capabilities: { streaming: true } },
  });
  if (response.status !== 200) throw new Error('建 Offering 失败：' + response.text.slice(0, 160));
  const offeringId = response.json.data.id;

  response = await call(`${BASE}/api/apigx/price-rules`, {
    method: 'PUT',
    auth: root.auth,
    body: {
      scope_type: 0,
      scope_id: offeringId,
      input_usd_per_million: 1,
      output_usd_per_million: 2,
      lock: true,
    },
  });
  if (response.status !== 200) throw new Error('Offering 配价失败：' + response.text.slice(0, 160));
  response = await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root.auth });
  if (response.status !== 200) throw new Error('提交 Offering 失败：' + response.text.slice(0, 160));
  response = await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root.auth });
  if (response.status !== 200) throw new Error('发布 Offering 失败：' + response.text.slice(0, 160));
  dim(`资源所有者=A(userId=${root.userId})，使用者=B(userId=${userB.userId})，model=${model}`);

  const token = await createToken(userB.auth, 'B1-source-' + stamp);
  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST',
    auth: 'Bearer ' + token.key,
    body: { model, messages: [{ role: 'user', content: 'source ACL probe' }] },
  });
  const setPreferences = async (body) => {
    const updated = await call(`${BASE}/api/apigx/platform-keys/${token.id}/preferences`, {
      method: 'PUT',
      auth: userB.auth,
      body,
    });
    if (updated.status !== 200) {
      throw new Error(`设置平台 Key 偏好失败：${updated.status} ${updated.text.slice(0, 160)}`);
    }
  };

  // 先授予分组访问权，再等待新模型进入底座能力缓存；只有真实 200 才算链路成立。
  const grantCreated = await call(`${BASE}/api/apigx/access/grants`, {
    method: 'POST',
    auth: root.auth,
    body: { resource_type: 0, resource_id: groupId, target_user_id: userB.userId },
  });
  if (grantCreated.status !== 200 || !grantCreated.json?.data?.id) {
    throw new Error('创建分组 Grant 失败：' + grantCreated.text.slice(0, 160));
  }
  const grantId = grantCreated.json.data.id;
  await setPreferences({
    scope_mode: 1,
    allowed_owner_user_ids: [root.userId],
    allow_fallback: false,
  });
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  let allowed = null;
  for (let attempt = 0; attempt < 20; attempt++) {
    allowed = await chat();
    if (allowed.status === 200 || !allowed.text.includes('model_not_found')) break;
    await new Promise((resolve) => setTimeout(resolve, 5000));
  }
  check('资源 Grant + Key 允许分享者时放行', allowed?.status === 200, `HTTP ${allowed?.status}`);

  // Grant 仍有效，但 Key 只允许 B 自己；模型唯一来源属于 A，必须统一拒绝。
  await setPreferences({
    scope_mode: 1,
    allowed_owner_user_ids: [userB.userId],
    allow_fallback: false,
  });
  const keyDenied = await chat();
  info(`Key 范围拒绝 → HTTP ${keyDenied.status} ${keyDenied.text.slice(0, 100)}`);
  check('平台 Key 来源范围与资源 ACL 求交', isSourceDenied(keyDenied), `HTTP ${keyDenied.status}`);

  // Key 改回允许 A 后恢复；随后撤销 Grant，证明仅满足 Key 范围仍不能调用。
  await setPreferences({
    scope_mode: 1,
    allowed_owner_user_ids: [root.userId],
    allow_fallback: false,
  });
  const restored = await chat();
  check('恢复 Key 允许范围后重新放行', restored.status === 200, `HTTP ${restored.status}`);

  const revoked = await call(`${BASE}/api/apigx/access/grants/${grantId}`, {
    method: 'DELETE',
    auth: root.auth,
  });
  if (revoked.status !== 200) throw new Error('撤销 Grant 失败：' + revoked.text.slice(0, 160));
  const grantDenied = await chat();
  info(`Grant 撤销 → HTTP ${grantDenied.status} ${grantDenied.text.slice(0, 100)}`);
  check('撤销 Grant 后立即统一拒绝', isSourceDenied(grantDenied), `HTTP ${grantDenied.status}`);

  await setPreferences({
    scope_mode: 1,
    allowed_owner_user_ids: [root.userId],
    allow_fallback: true,
  });
  const fallbackDenied = await chat();
  check('允许回退也不能绕过已撤销 Grant', isSourceDenied(fallbackDenied), `HTTP ${fallbackDenied.status}`);

  const failed = results.filter((item) => !item.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((error) => {
  console.error('\x1b[31m异常：' + error.message + '\x1b[0m');
  process.exitCode = 2;
});
