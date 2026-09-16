#!/usr/bin/env node
// T0.9：阶段 0 的 5 项验收实验（全部对本地替身，不花钱、不碰真实上游）
//
//   A1 两账号归属隔离
//   A2 定向渠道 + 三把 Key 不被二次抽选
//   A3 无余额但正成本估算（对应 usage_only 落点）
//   A4 预算与并发准入
//   A5 任务 / 文件对象隔离
//
// 用法：
//   node tools/stage0-verify/acceptance.mjs                # 全部跑
//   node tools/stage0-verify/acceptance.mjs --only A1,A2   # 只跑部分
//   node tools/stage0-verify/acceptance.mjs --wait 65      # 建渠道后等缓存同步
//
// 设计原则：**只测量、不美化**。底座当前不具备的能力，直接如实报告为「缺口」，
// 因为阶段 0 的目的就是判定三条路径能否在既有扩展点闭合。

import fs from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };

// 会话缓存：底座的 /api/user/login 挂了 CriticalRateLimit（实测连打会返回 429），
// 因此把 access_token 缓存到工作区内复用，只在过期时才重新登录。
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
function loadSessions() { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } }
function saveSessions(s) { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(s, null, 2)); }
const sessions = loadSessions();

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const ROOT_USER = 'apigxroot';
const ROOT_PASS = 'ApigxDev#2026';
const USER_B = 'apigxuserb';
const USER_B_PASS = 'ApigxB#2026';
const VERIFY_TOKEN = 'apigx-stage0-verify';
const WAIT_S = Number(argOf('--wait', '0'));
const ONLY = (argOf('--only', '') || '').split(',').map((s) => s.trim()).filter(Boolean);

const PIN_MODEL = 'stub/pin-model-202609';
// A3/A4 用一个「确定已有渠道」的模型，避免把「找不到渠道」误判成「余额不足」。
// 默认复用 tools/stage0-verify/wire-base.mjs 建好的那条渠道所用的模型。
const LIVE_MODEL = argOf('--model', 'stub/model-x-202609');
const CH_A = 'stub-pin-a';
const CH_B = 'stub-pin-b';
const KEYS_A = ['sk-stub-PIN-A1', 'sk-stub-PIN-A2'];
const KEYS_B = ['sk-stub-PIN-B1'];

let cookies = [];
function rememberCookies(res) {
  const raw = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
  for (const c of raw) {
    const pair = c.split(';')[0];
    cookies = cookies.filter((x) => x.split('=')[0] !== pair.split('=')[0]);
    cookies.push(pair);
  }
}
async function call(url, { method = 'GET', body, headers = {}, auth, jar = true } = {}) {
  const h = { accept: 'application/json', ...headers };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (jar && cookies.length) h.cookie = cookies.join('; ');
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  if (jar) rememberCookies(res);
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}
const sleep = (s) => new Promise((r) => setTimeout(r, s * 1000));

const findings = [];
function note(id, verdict, detail) {
  findings.push({ id, verdict, detail });
  const tag = verdict === 'PASS' ? '\x1b[32mPASS\x1b[0m'
    : verdict === 'GAP' ? '\x1b[33mGAP \x1b[0m'
    : verdict === 'FAIL' ? '\x1b[31mFAIL\x1b[0m' : '\x1b[36mINFO\x1b[0m';
  console.log(`  ${tag}  [${id}] ${detail}`);
}
const step = (t) => console.log(`\n=== ${t} ===`);
const dim = (s) => console.log('\x1b[2m  ' + s + '\x1b[0m');

let dashAuth = null;

async function login(user, pass) {
  const cached = sessions[user];
  if (cached?.token && cached.expiresAt && Date.now() / 1000 < cached.expiresAt - 120) {
    const probe = await call(`${BASE}/api/user/self`, { auth: `Bearer ${cached.token}`, jar: false });
    if (probe.status === 200) { dim(`复用 ${user} 的缓存会话令牌`); return `Bearer ${cached.token}`; }
  }
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password: pass }, jar: false });
  if (r.status === 429) {
    throw new Error(`登录 ${user} 被限流(429)：底座在 /api/user/login 上挂了 CriticalRateLimit，请等待窗口重置后重试（或复用 .toolchain/_state/sessions.json 里的令牌）`);
  }
  const t = r.json?.data?.access_token;
  if (!t) throw new Error(`${user} 登录失败: HTTP ${r.status} ${JSON.stringify(r.json)?.slice(0, 160) ?? r.text.slice(0, 160)}`);
  sessions[user] = { token: t, expiresAt: r.json?.data?.access_expires_at ?? null };
  saveSessions(sessions);
  dim(`已登录 ${user} 并缓存令牌`);
  return `Bearer ${t}`;
}

async function tokenKeyOf(auth, name) {
  const cached = sessions[`token:${name}`];
  if (cached?.key) { dim(`复用缓存令牌明文 ${name}`); return { id: cached.id, key: cached.key }; }
  const list = await call(`${BASE}/api/token/`, { auth });
  const items = list.json?.data?.items ?? list.json?.data ?? [];
  const mine = (Array.isArray(items) ? items : []).find((t) => t?.name === name);
  if (!mine) return null;
  // 注意：/api/token/:id/key 也受 CriticalRateLimit 约束（默认 20 次/20 分钟），所以要缓存
  const got = await call(`${BASE}/api/token/${mine.id}/key`, { method: 'POST', auth });
  const key = got.json?.data?.key ?? null;
  if (key) { sessions[`token:${name}`] = { id: mine.id, key }; saveSessions(sessions); }
  return { id: mine.id, key };
}

async function stubReset() { await call(`${STUB}/_control/reset`, { method: 'POST' }); }
async function stubProfile(p) { await call(`${STUB}/_control/profile`, { method: 'POST', body: p }); }
async function stubRequests() { const r = await call(`${STUB}/_control/requests?limit=200`); return r.json ?? { requests: [], keyCounts: {} }; }

// 注意：改用户额度必须走 POST /api/user/manage（controller/user.go:1043-1047 + user_quota.go:14-47），
// PUT /api/user/ 不会改额度（实测无效，会静默不改）。
async function setUserQuota(userId, value, label) {
  const r = await call(`${BASE}/api/user/manage`, {
    method: 'POST', auth: dashAuth,
    body: { id: userId, action: 'add_quota', mode: 'override', value },
  });
  if (r.json?.success !== true) note('A3', 'FAIL', `设置用户额度失败(${label})：HTTP ${r.status} ${r.json?.message ?? r.text.slice(0, 120)}`);
  return r.json?.success === true;
}

async function ensureChannel(name, keys, model) {
  const list = await call(`${BASE}/api/channel/?page=1&page_size=100`, { auth: dashAuth });
  const items = list.json?.data?.items ?? list.json?.data ?? [];
  const found = (Array.isArray(items) ? items : []).find((c) => c?.name === name);
  if (found) return { id: found.id, created: false };
  const body = {
    mode: keys.length > 1 ? 'multi_to_single' : 'single',
    multi_key_mode: 'polling',
    channel: { type: 1, name, key: keys.join('\n'), base_url: STUB, models: model, group: 'default', status: 1 },
  };
  const r = await call(`${BASE}/api/channel/`, { method: 'POST', auth: dashAuth, body });
  if (r.json?.success !== true) throw new Error(`建渠道 ${name} 失败: ${r.json?.message ?? r.text.slice(0, 160)}`);
  // 取回 id
  const again = await call(`${BASE}/api/channel/?page=1&page_size=100`, { auth: dashAuth });
  const list2 = again.json?.data?.items ?? again.json?.data ?? [];
  const c = (Array.isArray(list2) ? list2 : []).find((x) => x?.name === name);
  return { id: c?.id, created: true };
}

// ---------------------------------------------------------------- A1
async function A1() {
  step('A1 两账号归属隔离');
  // 建用户 B
  const list = await call(`${BASE}/api/user/?page=1&page_size=100`, { auth: dashAuth });
  const users = list.json?.data?.items ?? list.json?.data ?? [];
  let userB = (Array.isArray(users) ? users : []).find((u) => u?.username === USER_B);
  if (!userB) {
    const c = await call(`${BASE}/api/user/`, {
      method: 'POST', auth: dashAuth,
      body: { username: USER_B, password: USER_B_PASS, display_name: 'Stage0 User B', role: 1, status: 1, quota: 1000000 },
    });
    dim(`POST /api/user/ -> ${c.status} ${c.json?.message ?? JSON.stringify(c.json)?.slice(0, 120)}`);
    const again = await call(`${BASE}/api/user/?page=1&page_size=100`, { auth: dashAuth });
    const u2 = again.json?.data?.items ?? again.json?.data ?? [];
    userB = (Array.isArray(u2) ? u2 : []).find((u) => u?.username === USER_B);
  }
  if (!userB) { note('A1', 'FAIL', '创建用户 B 失败，无法继续'); return; }
  dim(`用户 B id=${userB.id}`);

  const authB = await login(USER_B, USER_B_PASS);

  // B 读自己的令牌列表：只应看到自己的
  const bTokens = await call(`${BASE}/api/token/`, { auth: authB });
  const bItems = bTokens.json?.data?.items ?? bTokens.json?.data ?? [];
  const seesForeign = (Array.isArray(bItems) ? bItems : []).some((t) => t?.name === VERIFY_TOKEN);
  note('A1', seesForeign ? 'FAIL' : 'PASS', `B 的令牌列表${seesForeign ? '看得到' : '看不到'} root 的令牌（共 ${Array.isArray(bItems) ? bItems.length : 0} 条）`);

  // B 读管理面
  const bChannels = await call(`${BASE}/api/channel/?page=1&page_size=10`, { auth: authB });
  note('A1', bChannels.status === 403 || bChannels.json?.success === false ? 'PASS' : 'GAP',
    `B 访问渠道管理面 -> HTTP ${bChannels.status} ${bChannels.json?.message ?? ''}`);

  const bUsers = await call(`${BASE}/api/user/?page=1&page_size=10`, { auth: authB });
  note('A1', bUsers.status === 403 || bUsers.json?.success === false ? 'PASS' : 'FAIL',
    `B 访问用户管理面 -> HTTP ${bUsers.status} ${bUsers.json?.message ?? ''}`);

  // B 用不存在/他人令牌 id 取明文
  const foreign = await call(`${BASE}/api/token/1/key`, { method: 'POST', auth: authB });
  note('A1', foreign.json?.success === false ? 'PASS' : 'FAIL',
    `B 尝试读取他人令牌明文(id=1) -> ${foreign.json?.message ?? JSON.stringify(foreign.json)?.slice(0, 80)}`);

  note('A1', 'GAP', '底座渠道是**全局资源、没有归属字段**（model/channel.go:23-60 字段清单内无 owner）→ 分享者隔离必须由我们新增');
}

// ---------------------------------------------------------------- A2
async function A2() {
  step('A2 定向渠道 + 三把 Key 不被二次抽选');
  const a = await ensureChannel(CH_A, KEYS_A, PIN_MODEL);
  const b = await ensureChannel(CH_B, KEYS_B, PIN_MODEL);
  dim(`渠道 ${CH_A} id=${a.id} keys=${KEYS_A.length} | 渠道 ${CH_B} id=${b.id} keys=${KEYS_B.length}`);
  if (a.created || b.created) {
    if (WAIT_S > 0) { dim(`等待 ${WAIT_S}s 让底座同步渠道缓存…`); await sleep(WAIT_S); }
    else dim('提示：新渠道需要底座缓存同步（默认 60s），若下面 503 请加 --wait 65 重跑');
  }

  const tk = await tokenKeyOf(dashAuth, VERIFY_TOKEN);
  if (!tk?.key) { note('A2', 'FAIL', '拿不到平台令牌明文'); return; }

  // 步骤 1：不指定渠道，打 6 次，看 Key 池是否被使用
  await stubReset();
  const N = 6;
  let okCount = 0;
  for (let i = 0; i < N; i++) {
    const r = await call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: `Bearer ${tk.key}`, jar: false,
      body: { model: PIN_MODEL, messages: [{ role: 'user', content: `pool ${i}` }] },
    });
    if (r.status === 200) okCount++;
    else if (i === 0) dim(`首次请求 -> ${r.status} ${r.text.slice(0, 160)}`);
  }
  let st = await stubRequests();
  const poolKeys = Object.keys(st.keyCounts || {});
  note('A2', okCount > 0 ? 'PASS' : 'FAIL', `自动选路 ${okCount}/${N} 次成功；替身看到 ${st.requests.length} 次请求`);
  if (st.requests.length) {
    note('A2', 'INFO', `Key 池指纹分布：${JSON.stringify(st.keyCounts)}`);
    note('A2', poolKeys.length > 1 ? 'PASS' : 'INFO',
      `渠道内 Key 池被${poolKeys.length > 1 ? '实际轮换使用' : '只用了一把（可能样本少或策略未轮换）'}`);
  }

  // 步骤 2：定向到渠道 A（令牌后缀 -<channelId>），验证不会落到渠道 B 的 Key
  await stubReset();
  const pinned = await (async () => {
    let success = 0, statuses = [];
    for (let i = 0; i < 6; i++) {
      const r = await call(`${BASE}/v1/chat/completions`, {
        method: 'POST', auth: `Bearer ${tk.key}-${a.id}`, jar: false,
        body: { model: PIN_MODEL, messages: [{ role: 'user', content: `pin ${i}` }] },
      });
      statuses.push(r.status);
      if (r.status === 200) success++;
      if (i === 0 && r.status !== 200) dim(`定向请求 -> ${r.status} ${r.text.slice(0, 200)}`);
    }
    return { success, statuses };
  })();

  st = await stubRequests();
  const seenKeys = Object.keys(st.keyCounts || {});
  const leakedToB = seenKeys.some((k) => k.endsWith('PIN-B1'));
  note('A2', pinned.success > 0 ? 'PASS' : 'FAIL',
    `定向渠道 ${CH_A} 调用 ${pinned.success}/6 次成功（status=${pinned.statuses.join(',')}）`);
  note('A2', leakedToB ? 'FAIL' : 'PASS',
    `定向后替身收到的 Key：${JSON.stringify(st.keyCounts)}；${leakedToB ? '出现渠道 B 的 Key → 被二次抽选！' : '未出现渠道 B 的 Key'}`);
  note('A2', 'INFO', '注意：底座 pin 语法是「令牌明文 + "-" + 渠道ID」，且源码只允许管理员使用（middleware/auth.go:536-551）');
}

// ---------------------------------------------------------------- A3
async function A3() {
  step('A3 无余额但正成本估算');
  // 用普通用户 B，避免 root 特权污染结论；并用一个**可计价**的模型，
  // 否则「成本估算为正」根本无从谈起（未定价模型算出来就是 0）。
  const users = await call(`${BASE}/api/user/?page=1&page_size=100`, { auth: dashAuth });
  const uItems = users.json?.data?.items ?? users.json?.data ?? [];
  const userB = (Array.isArray(uItems) ? uItems : []).find((u) => u?.username === USER_B);
  if (!userB) { note('A3', 'FAIL', '找不到用户 B，先跑 A1'); return; }

  const authB = await login(USER_B, USER_B_PASS);
  let bTok = await tokenKeyOf(authB, 'apigx-stage0-userb');
  if (!bTok) {
    await call(`${BASE}/api/token/`, {
      method: 'POST', auth: authB,
      // tokenRequest 是内嵌 model.Token（controller/token.go:35-38），字段必须放顶层，
      // 不能包成 {token:{...}} —— 包了会被静默建成空名令牌。
      body: { name: 'apigx-stage0-userb', remain_quota: 0, unlimited_quota: true, expired_time: -1, model_limits_enabled: false },
    });
    bTok = await tokenKeyOf(authB, 'apigx-stage0-userb');
  }
  if (!bTok?.key) { note('A3', 'FAIL', '用户 B 没有可用令牌'); return; }

  const selfBefore = await call(`${BASE}/api/user/self`, { auth: authB });
  const qBefore = selfBefore.json?.data?.quota;
  dim(`用户 B 当前 quota=${qBefore}`);

  // 第一步：给 B 足够额度，验证「成本估算为正」确实会发生（额度应减少）
  const GRANT = 100000;
  await setUserQuota(userB.id, GRANT, 'grant');
  const q0 = (await call(`${BASE}/api/user/self`, { auth: authB })).json?.data?.quota;
  await stubReset();
  const r1 = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: `Bearer ${bTok.key}`, jar: false,
    body: { model: LIVE_MODEL, messages: [{ role: 'user', content: 'positive cost probe' }] },
  });
  const q1 = (await call(`${BASE}/api/user/self`, { auth: authB })).json?.data?.quota;
  const cost = (typeof q0 === 'number' && typeof q1 === 'number') ? q0 - q1 : null;
  const st1 = await stubRequests();
  dim(`有额度调用 -> HTTP ${r1.status}，额度 ${q0} -> ${q1}（消耗 ${cost}），到达上游 ${st1.requests.length} 次`);
  note('A3', r1.status === 200 && cost > 0 ? 'PASS' : (r1.status === 200 ? 'GAP' : 'FAIL'),
    r1.status === 200
      ? (cost > 0 ? `正成本估算成立：单次请求扣减 ${cost}（约 ＄${(cost / 500000).toFixed(6)}，QuotaPerUnit=500000）`
                  : '请求成功但额度未减少 → 成本估算为 0，无法支撑「正成本估算」')
      : `有额度却被拒：HTTP ${r1.status}`);

  // 第二步：把额度置 0，验证「无余额」会被拦（这正是 usage_only 要移除的前置条件）
  await setUserQuota(userB.id, 0, 'zero');
  const qAfter = (await call(`${BASE}/api/user/self`, { auth: authB })).json?.data?.quota;
  note('A3', qAfter === 0 ? 'PASS' : 'FAIL',
    `用户 B 额度已置零并复核：${q0} -> ${qAfter}（必须真的置零，否则本实验无效）`);

  await stubReset();
  const r = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: `Bearer ${bTok.key}`, jar: false,
    body: { model: LIVE_MODEL, messages: [{ role: 'user', content: 'zero balance probe' }] },
  });
  dim(`用户 B 零余额调用（model=${LIVE_MODEL}）-> HTTP ${r.status} ${r.text.slice(0, 200)}`);
  const st = await stubRequests();
  note('A3', r.status === 200 ? 'INFO' : 'GAP',
    r.status === 200
      ? '零余额用户调用成功（未被充值余额前置拦住）'
      : `零余额用户被拒（HTTP ${r.status}）→ 底座存在硬性「充值余额前置」（insufficient_user_quota），usage_only 必须移除这一前置检查`);
  note('A3', 'INFO', `零余额请求${st.requests.length ? '已' : '未'}到达上游（${st.requests.length} 次）`);

  await setUserQuota(userB.id, qBefore ?? 1000000, 'restore');
  dim(`已复原用户 B quota=${qBefore}`);
}

// ---------------------------------------------------------------- A4
async function A4() {
  step('A4 预算与并发准入');
  const tk = await tokenKeyOf(dashAuth, VERIFY_TOKEN);
  if (!tk?.key) { note('A4', 'FAIL', '拿不到平台令牌明文'); return; }

  // 并发：让替身把每个请求握 1.5s，然后并发发 8 个，看是否全部同时在飞
  await stubReset();
  await stubProfile({ delayMs: 1500, ttftMs: 0, status: 0, hang: false });
  const N = 8;
  const t0 = Date.now();
  const results = await Promise.all(Array.from({ length: N }, (_, i) =>
    call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: `Bearer ${tk.key}`, jar: false,
      body: { model: LIVE_MODEL, messages: [{ role: 'user', content: `conc ${i}` }] },
    }).then((r) => r.status).catch(() => 'ERR')));
  const elapsed = Date.now() - t0;
  await stubProfile({ delayMs: 0 });
  const st = await stubRequests();
  const ats = (st.requests || []).map((q) => Date.parse(q.at)).sort((x, y) => x - y);
  const span = ats.length > 1 ? ats[ats.length - 1] - ats[0] : 0;
  const okN = results.filter((s) => s === 200).length;
  note('A4', okN > 0 ? 'INFO' : 'FAIL', `并发 ${N} 个请求：${okN} 个 200，总耗时 ${elapsed}ms，status=${results.join(',')}`);
  note('A4', span < 1500 && st.requests.length > 1 ? 'GAP' : 'INFO',
    `替身收到 ${st.requests.length} 个请求，到达时间跨度 ${span}ms（<1500ms 说明多个请求同时在飞 → 无渠道级并发上限）`);
  note('A4', 'GAP', '底座没有渠道/Key 级并发上限（model/channel.go:637-650 的锁只保护选择过程；middleware/model-rate-limit.go 是用户级窗口限流）');

  // 预算：把令牌额度设成一个「只够一两次请求」的小值，再并发冲击，看是否超支。
  // 底座用 Redis Lua 原子预留（model/quota_reserve.go:203-240）→ 预期不超支（这是正向结论）。
  const before = await tokenKeyOf(dashAuth, VERIFY_TOKEN);
  const listInfo = await call(`${BASE}/api/token/`, { auth: dashAuth });
  const items = listInfo.json?.data?.items ?? listInfo.json?.data ?? [];
  const mine = (Array.isArray(items) ? items : []).find((t) => t?.name === VERIFY_TOKEN);
  const SMALL_QUOTA = 20000;   // 单次请求约消耗 7.7k → 只够一两次，用来看「部分准入 + 不超支」
  await call(`${BASE}/api/token/`, { method: 'PUT', auth: dashAuth, body: { id: mine.id, name: VERIFY_TOKEN, unlimited_quota: false, remain_quota: SMALL_QUOTA, expired_time: -1 } });
  const q0 = (await call(`${BASE}/api/token/${mine.id}`, { auth: dashAuth })).json?.data?.remain_quota;
  dim(`令牌 ${mine.id} 额度已设为 ${SMALL_QUOTA}（读回 ${q0}），非无限`);

  await stubReset();
  const M = 6;
  const res2 = await Promise.all(Array.from({ length: M }, (_, i) =>
    call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: `Bearer ${before.key}`, jar: false,
      body: { model: LIVE_MODEL, messages: [{ role: 'user', content: `budget ${i}` }] },
    }).then((r) => r.status).catch(() => 'ERR')));
  const st2 = await stubRequests();
  const tokAfter = (await call(`${BASE}/api/token/${mine.id}`, { auth: dashAuth })).json?.data;
  const q1 = tokAfter?.remain_quota;
  const used = tokAfter?.used_quota;
  note('A4', 'INFO', `额度受限下并发 ${M} 个：status=${res2.join(',')}，到达上游 ${st2.requests.length} 次`);
  note('A4', 'INFO', `令牌额度：初始 ${SMALL_QUOTA} → 剩余 ${q1}，used_quota=${used}`);
  if (typeof q1 === 'number' && q1 < 0) {
    note('A4', 'FAIL', `并发下额度被击穿为负数（${q1}）→ 原子预留未生效`);
  } else if (st2.requests.length > 0 && used === 0) {
    note('A4', 'INFO', '消耗为 0：该模型未定价，因此本轮无法用金额口径检验预算击穿（需要给供给配价后再测）');
  } else {
    note('A4', 'PASS', `并发下额度未被击穿（剩余 ${q1} ≥ 0），与底座的原子预留设计一致`);
  }

  // 复原为无限额度
  await call(`${BASE}/api/token/`, { method: 'PUT', auth: dashAuth, body: { id: mine.id, name: VERIFY_TOKEN, unlimited_quota: true, remain_quota: 0, expired_time: -1 } });
  dim('令牌已复原为无限额度');
}

// ---------------------------------------------------------------- A5
async function A5() {
  step('A5 任务 / 文件对象隔离');
  const tk = await tokenKeyOf(dashAuth, VERIFY_TOKEN);
  if (!tk?.key) { note('A5', 'FAIL', '拿不到平台令牌明文'); return; }

  const probes = [
    ['GET', '/v1/tasks/stage0-nonexistent'],
    ['GET', '/v1/tasks/stage0-nonexistent/artifacts'],
    ['GET', '/v1/tasks/stage0-nonexistent/artifacts/any/content'],
  ];
  for (const [method, path] of probes) {
    const withAuth = await call(`${BASE}${path}`, { method, auth: `Bearer ${tk.key}`, jar: false });
    const noAuth = await call(`${BASE}${path}`, { method, jar: false });
    note('A5', noAuth.status === 401 || noAuth.status === 404 ? 'PASS' : 'GAP',
      `${path}：无凭据 -> ${noAuth.status}；有凭据 -> ${withAuth.status}（不存在对象应 404，无凭据应 401）`);
  }
  note('A5', 'INFO', '底座的产物访问提供「令牌或签名能力」二选一（middleware/task_artifact_access.go:174），默认策略需要我们收紧为鉴权下载');
  note('A5', 'GAP', '本轮没有真实异步任务，**归属隔离未做端到端取证**（需要真实可用的视频/图片上游，属阶段 3）');
}

// ---------------------------------------------------------------- main
(async () => {
  console.log(`目标底座 ${BASE} ；替身 ${STUB}`);
  dashAuth = await login(ROOT_USER, ROOT_PASS);
  dim('root 登录 OK');
  const run = (id, fn) => (!ONLY.length || ONLY.includes(id)) ? fn() : Promise.resolve();

  await run('A1', A1);
  await run('A2', A2);
  await run('A3', A3);
  await run('A4', A4);
  await run('A5', A5);

  step('结果汇总');
  const by = (v) => findings.filter((f) => f.verdict === v).length;
  console.log(`  共 ${findings.length} 条结论：PASS ${by('PASS')}，GAP ${by('GAP')}，FAIL ${by('FAIL')}，INFO ${by('INFO')}`);
  if (by('GAP')) {
    console.log('\n\x1b[33m  缺口（阶段 0 的核心产出：这些正是我们要在扩展点上补齐的能力）\x1b[0m');
    for (const f of findings.filter((x) => x.verdict === 'GAP')) console.log(`    [${f.id}] ${f.detail}`);
  }
  if (by('FAIL')) {
    console.log('\n\x1b[31m  失败项\x1b[0m');
    for (const f of findings.filter((x) => x.verdict === 'FAIL')) console.log(`    [${f.id}] ${f.detail}`);
  }
})().catch((e) => { console.error('\x1b[31m异常终止：' + e.message + '\x1b[0m'); process.exitCode = 2; });
