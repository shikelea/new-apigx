#!/usr/bin/env node
// T1.9 验收：广场与详情在**真实网关**上的行为
//
//  ① 未认证 → 401
//  ② 只有「已发布 + 分组公开」的供给出现在广场
//  ③ 详情可按 public_id 取到；未发布的详情返回 404（不泄露存在性）
//  ④ 对外只给平台**已具备**的能力；声明但未具备的单独列出
//  ⑤ 响应不含内部标识（渠道内部 ID、上游地址、凭据）
//
// 用法：node tools/stage1-verify/plaza.mjs

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');

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
  const root = await login('apigxroot', 'ApigxDev#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  const stamp = Date.now().toString().slice(-6);
  const model = 'plaza-' + stamp;

  // 未认证
  const anon = await call(`${BASE}/api/apigx/plaza`);
  check('未认证访问广场 → 401', anon.status === 401, `HTTP ${anon.status}`);

  // 公开分组 + 渠道 + 供给
  const g = await call(`${BASE}/api/apigx/share-groups`, {
    method: 'POST', auth: root,
    body: { name: 'PLAZA组-' + stamp, default_visibility: 2 },
  });
  if (g.status !== 200) throw new Error('建公开分组失败：' + g.text.slice(0, 120));
  const groupId = g.json.data.id;

  const channelName = 'PLAZA底座-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-plaza-' + stamp, base_url: 'http://127.0.0.1:8100', models: model, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await call(`${BASE}/api/channel/?p=1&page_size=1000`, { auth: root });
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: groupId, base_channel_id: base.id, name: 'PLAZA渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;

  const off = await call(`${BASE}/api/apigx/channels/${shareChannelId}/offerings`, {
    method: 'POST', auth: root,
    body: { upstream_model_id: model, capabilities: { streaming: true, vision: true } },
  });
  if (off.status !== 200) throw new Error('建供给失败：' + off.text.slice(0, 120));
  const offeringId = off.json.data.id;
  const publicId = off.json.data.public_id;

  // 草稿阶段：不该出现
  let plaza = await call(`${BASE}/api/apigx/plaza?page_size=100`, { auth: root });
  const draftVisible = (plaza.json?.data?.items ?? []).some((x) => x.model === model);
  check('草稿供给不出现在广场', !draftVisible);
  const draftDetail = await call(`${BASE}/api/apigx/plaza/${publicId}`, { auth: root });
  check('未发布的详情 → 404（不泄露存在性）', draftDetail.status === 404, `HTTP ${draftDetail.status}`);

  // 提交 + 发布
  await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
  const pub = await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });
  check('管理员可发布', pub.status === 200, `HTTP ${pub.status}`);

  plaza = await call(`${BASE}/api/apigx/plaza?page_size=100`, { auth: root });
  const item = (plaza.json?.data?.items ?? []).find((x) => x.model === model);
  check('已发布 + 公开分组的供给出现在广场', Boolean(item), item ? `offering=${item.offering_public_id}` : '未出现');
  if (item) {
    check('广场给出对外可辨识的来源', Boolean(item.channel_public_id), `channel_public_id=${item.channel_public_id}`);
    check('只暴露已具备的能力（streaming=true）', item.capabilities?.streaming === true);
    check('未具备的能力为 false（vision=false）', item.capabilities?.vision === false);
    const unverified = item.unverified_capabilities ?? [];
    check('声明但未具备的能力单独列出', unverified.includes('vision'), `unverified=${unverified.join(',')}`);
  }

  const detail = await call(`${BASE}/api/apigx/plaza/${publicId}`, { auth: root });
  check('详情可按 public_id 取到', detail.status === 200 && detail.json?.data?.model === model, `HTTP ${detail.status}`);

  // 泄露扫描
  const forbidden = [
    { name: '上游地址', pattern: /127\.0\.0\.1:8100/ },
    { name: '凭据', pattern: /sk-plaza-|enc:v1:/ },
    { name: '内部渠道 ID 字段', pattern: /"channel_id"|"base_channel_id"|"share_channel_id"/ },
    { name: '堆栈', pattern: /\.go:\d+/ },
  ];
  for (const f of forbidden) {
    check(`广场响应不含${f.name}`, !f.pattern.test(plaza.text), f.pattern.test(plaza.text) ? '出现泄露' : '未出现');
  }

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
