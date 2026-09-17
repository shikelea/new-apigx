#!/usr/bin/env node
// T1.8 验收：一键获取上游模型 + 导入为草稿供给
//
// 验证：
//   ① 一键获取能真正拉到上游模型列表（复用底座按渠道类型分派的取模型能力）
//   ② import=true 会把它们落成**草稿**供给（导入 ≠ 发布）
//   ③ 反复获取导入是**幂等**的（已存在的被跳过而不是报错）
//   ④ 越权：他人的渠道取不到（404）
//
// 用法：node tools/stage1-verify/fetch-models.mjs
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

const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }

// 复用缓存会话：底座的每用户会话数有上限，反复登录会 409
async function login(user, password) {
  const cached = sessions[user];
  if (cached?.token) {
    const probe = await call(`${BASE}/api/user/self`, { auth: 'Bearer ' + cached.token });
    if (probe.status === 200) return 'Bearer ' + cached.token;
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status} ${r.text.slice(0, 120)}`);
  sessions[user] = { token: r.json.data.access_token };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const authA = await login('apigxroot', 'ApigxDev#2026');
  const authB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  const stamp = Date.now().toString().slice(-6);

  // 底座渠道 → 分享渠道 → 一把真实可用的 Key（取模型要真的去问上游）
  const channelName = 'FETCH底座-' + stamp;
  const created = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: authA,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-stub-' + stamp, base_url: 'http://127.0.0.1:8100', models: 'stub/model-x-202609', groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  if (created.status !== 200 || created.json?.success === false) throw new Error('建底座渠道失败：' + created.text.slice(0, 120));
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await call(`${BASE}/api/channel/?page=1&page_size=1000`, { auth: authA });
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: authA, body: { name: 'FETCH分组-' + stamp } });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: authA,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'FETCH渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: authA, body: { stable_key_id: 'fetch-' + stamp, name: 'fetch', key: 'sk-fetch-' + stamp },
  });
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: authA });

  // ① 预览：只拉取不落库
  const preview = await call(`${BASE}/api/apigx/channels/${shareChannelId}/fetch-models`, { method: 'POST', auth: authA, body: {} });
  check('一键获取可用', preview.status === 200, `HTTP ${preview.status} ${preview.text.slice(0, 100)}`);
  const models = preview.json?.data?.models ?? [];
  info(`上游返回 ${models.length} 个模型：${models.slice(0, 5).join(', ')}`);
  check('拿到了上游模型列表', models.length > 0, `${models.length} 个`);

  const before = await call(`${BASE}/api/apigx/channels/${shareChannelId}/offerings`, { auth: authA });
  check('预览不落库', (before.json?.data?.items ?? []).length === 0, '预览后仍无供给');

  // ② 导入为草稿
  const imported = await call(`${BASE}/api/apigx/channels/${shareChannelId}/fetch-models`, { method: 'POST', auth: authA, body: { import: true } });
  check('导入成功', imported.status === 200 && imported.json?.data?.created === models.length,
    `created=${imported.json?.data?.created}`);
  const after = await call(`${BASE}/api/apigx/channels/${shareChannelId}/offerings`, { auth: authA });
  const items = after.json?.data?.items ?? [];
  check('供给已落库', items.length === models.length, `${items.length} 条`);
  check('导入的供给是草稿', items.every((x) => x.status === 0), `状态=${[...new Set(items.map((x) => x.status))].join(',')}`);

  // ③ 幂等：再次导入应全部跳过
  const again = await call(`${BASE}/api/apigx/channels/${shareChannelId}/fetch-models`, { method: 'POST', auth: authA, body: { import: true } });
  check('重复导入幂等（全部跳过）', again.status === 200 && again.json?.data?.created === 0,
    `created=${again.json?.data?.created} skipped=${(again.json?.data?.skipped ?? []).length}`);

  // ④ 越权
  const intrude = await call(`${BASE}/api/apigx/channels/${shareChannelId}/fetch-models`, { method: 'POST', auth: authB, body: {} });
  check('他人无法对他人的渠道取模型', intrude.status === 404, `HTTP ${intrude.status}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
