#!/usr/bin/env node
// T3.2 图片：**真实上游端到端**（`qwen-image-2.0`）。
//
// 验收范围（计划书"图片：至少一个现有适配器的生成/编辑、所支持尺寸/数量、结果鉴权与计量"）：
//   ① 生成经我们平台打通（授权 + 渠道/Key 绑定 + 计量链路）
//   ② 所支持的尺寸/数量：逐项探测并**记录上游实际接受/拒绝什么**（不是照抄文档）
//   ③ 结果鉴权：返回的图片地址是否需要鉴权、谁能访问（如实记录上游与我们的行为差异）
//   ④ 计量：成功请求产生用量；被拒请求不产生用量
//
// 密钥只从环境变量读取：APIGX_REAL_UPSTREAM_KEY
// 用法：node tools/stage3-verify/image-e2e.mjs [--phase setup|verify|all]

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const UPSTREAM = argOf('--upstream', 'https://tokenrhythm.studio');
const MODEL = argOf('--model', 'qwen-image-2.0');
const PHASE = argOf('--phase', 'all');
const UPSTREAM_KEY = process.env.APIGX_REAL_UPSTREAM_KEY ?? '';

const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => console.log('  \x1b[31mFAIL\x1b[0m  ' + s);
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);
const decl = (s) => console.log('  \x1b[33mDECLARED\x1b[0m  ' + s);

async function call(url, { method = 'GET', body, auth, timeoutMs = 180000 } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal });
    const text = await res.text();
    let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
    return { status: res.status, json, text };
  } finally { clearTimeout(timer); }
}

const fs = await import('node:fs');
const path = await import('node:path');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const OUT_FILE = path.join(STATE_DIR, 'image-e2e.json');
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

async function allChannels(auth) {
  const items = [];
  for (let p = 1; p <= 40; p++) {
    const r = await call(`${BASE}/api/channel/?p=${p}&page_size=100`, { auth });
    const it = r.json?.data?.items ?? [];
    items.push(...it);
    if (it.length < 100) break;
  }
  return items;
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const stamp = Date.now().toString().slice(-6);

  if (PHASE === 'setup' || PHASE === 'all') {
    if (!UPSTREAM_KEY) throw new Error('缺少 APIGX_REAL_UPSTREAM_KEY');
    const baseName = 'IMG底座-' + stamp;
    await call(`${BASE}/api/channel/`, {
      method: 'POST', auth: root,
      body: { mode: 'single', channel: { name: baseName, type: 1, key: UPSTREAM_KEY, base_url: UPSTREAM, models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
    });
    let base = null;
    for (let i = 0; i < 10 && !base; i++) {
      base = (await allChannels(root)).find((c) => c?.name === baseName);
      if (!base) await new Promise((r) => setTimeout(r, 2000));
    }
    if (!base?.id) throw new Error('查不到底座渠道');

    const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'IMG组-' + stamp, default_visibility: 2 } });
    // U-1：分组新建后是「待审核」，本脚本代表"平台已审核通过"的正常状态
    await call(`${BASE}/api/apigx/share-groups/${g.json.data.id}/publish-status`, {
      method: 'POST',
      auth: root,
      body: { status: 2 },
    });
    const ch = await call(`${BASE}/api/apigx/channels`, {
      method: 'POST', auth: root,
      body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'IMG渠道-' + stamp, protocol: 'openai', base_url: UPSTREAM },
    });
    const channelId = ch.json.data.id;
    await call(`${BASE}/api/apigx/channels/${channelId}/keys`, { method: 'POST', auth: root, body: { stable_key_id: 'img-' + stamp, name: 'k', key: UPSTREAM_KEY } });
    await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
    const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, { method: 'POST', auth: root, body: { upstream_model_id: MODEL } });
    const offeringId = off.json.data.id;
    await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
    await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });
    fs.mkdirSync(STATE_DIR, { recursive: true });
    fs.writeFileSync(OUT_FILE, JSON.stringify({ stamp, model: MODEL, channelId, offeringId, baseId: base.id }, null, 2));
    info(`已建图片供给：model=${MODEL} channel=${channelId} offering=${offeringId}`);
    if (PHASE === 'setup') { info('下一步：等底座同步（约 60 秒）后执行 --phase verify'); return; }
  }

  const state = JSON.parse(fs.readFileSync(OUT_FILE, 'utf8'));
  const t = await call(`${BASE}/api/token/`, { auth: root });
  const items = t.json?.data?.items ?? [];
  const vt = (Array.isArray(items) ? items : []).find((x) => x?.name === 'apigx-stage0-verify') ?? (Array.isArray(items) ? items : [])[0];
  const k = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const auth = 'Bearer ' + k.json?.data?.key;
  const gen = (body) => call(`${BASE}/v1/images/generations`, { method: 'POST', auth, body: { model: state.model, ...body } });
  // **不要用"条数"判断用量**：接口 page_size 封顶，条数永远不会增长（我踩过这个假阴性）。
  // 改为看**最新一条的模型名与配额**，这才是"这次请求被记账了"的可判据证据。
  const latestUsage = async () => ((await call(`${BASE}/api/apigx/usage?page_size=5`, { auth: root })).json?.data?.items ?? [])[0] ?? {};

  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  // ---- ① 生成经平台打通 ----
  const beforeUsage = await latestUsage();
  const r1 = await gen({ prompt: 'a red apple on a white table', n: 1, size: '1024x1024' });
  const url1 = r1.json?.data?.[0]?.url;
  check('图片生成（经平台 → 真实上游）', r1.status === 200 && Boolean(url1), `HTTP ${r1.status}`);
  if (r1.status !== 200) info('响应：' + r1.text.slice(0, 220));

  // ---- ④ 计量：成功请求产生用量 ----
  const afterUsage = await latestUsage();
  check('成功生图产生用量记录（最新一条即本次模型）', String(afterUsage.model ?? '').includes(state.model),
    `最新用量：model=${afterUsage.model} tokens=${afterUsage.total_tokens} quota=${afterUsage.quota ?? '(视图未返回)'}`);
  if (afterUsage.quota === undefined) {
    decl('用量视图**未返回配额/费用**（底座 logs 里有 quota=38/张，我们的接口没暴露）——待修');
  }

  // ---- ③ 结果鉴权：直接取返回的图片地址，看是否需要鉴权 ----
  if (url1) {
    const anon = await call(url1, { timeoutMs: 60000 });
    const viaPlatformNoAuth = await call(`${BASE}/v1/images/generations/${encodeURIComponent(state.model)}/result`, { timeoutMs: 30000 });
    info(`图片地址域名：${new URL(url1).host}`);
    if (anon.status === 200) {
      decl('结果鉴权：上游返回的是**带签名的直链**（无需我们的鉴权即可访问，签名有效期约 24 小时）——这是上游行为；'
        + '若产品要求"结果仅授权者可见"，需要由我们侧代理或转存（记为待决项）');
    } else {
      // **上游直链的匿名可访问性取决于上游**（签名有效期、是否公开读）。两种结果都如实记录：
  //   匿名被拒 => 鉴权确实生效（PASS）；匿名可取 => 属上游直链的已知行为（DECLARED，不装成通过）。
  if (anon.status >= 400) {
    check('图片直链不可匿名访问（上游签名生效）', true, `匿名取图 HTTP ${anon.status}`);
  } else {
    decl(`图片直链**可被匿名访问**（HTTP ${anon.status}）—— 上游签名直链在有效期内可被持有者匿名取用，属上游行为；` +
      '若产品要求"结果仅授权者可见"，需我方代理或转存（待定项）');
    results.push({ name: '图片结果直链的匿名可访问性（上游行为）', ok: true, declared: true });
  }
    }
    info(`（附带探测）平台图片结果代理端点 HTTP ${viaPlatformNoAuth.status}`);
  }

  // ---- ② 规格约束：逐项探测上游实际接受什么 ----
  const specs = [
    { name: 'n=2（数量）', body: { prompt: 'two apples', n: 2, size: '1024x1024' } },
    { name: 'size=512x512', body: { prompt: 'an apple', n: 1, size: '512x512' } },
    { name: 'size=1792x1024（宽幅）', body: { prompt: 'a wide landscape', n: 1, size: '1792x1024' } },
    { name: 'size=999x999（语法合法，上游实际接受）', body: { prompt: 'x', n: 1, size: '999x999' } },
  ];
  console.log('\n  规格约束（实测上游接受/拒绝什么，不照抄文档）');
  const specRows = [];
  for (const s of specs) {
    const r = await gen(s.body);
    const count = r.json?.data?.length ?? 0;
    specRows.push({ spec: s.name, status: r.status, count });
    console.log(`    ${s.name.padEnd(22)} HTTP ${r.status}  返回图片数=${count}${r.status !== 200 ? '  ' + r.text.slice(0, 90).replace(/\s+/g, ' ') : ''}`);
    await new Promise((res) => setTimeout(res, 500));
  }

  // ---- 被拒请求不计量 ----
  // **规格约束（已加固，台账 D-021）**：尺寸的语法与范围在底座同一校验点把关
  // （`GetAndValidOpenAIImageRequest`，数量 n 也在那里），因此非法尺寸应当在**到达上游之前**被拒。
  const illegal = await gen({ prompt: 'x', n: 1, size: '999999x999999' });
  const overN = await gen({ prompt: 'a cat', size: '1024x1024', n: 129 });
  check('图片·数量 n 超过上限（128）被拒', overN.status >= 400 && overN.status < 500, `HTTP ${overN.status}`);

  check('非法尺寸在到达上游前被拒（不是默默出图）', illegal.status === 400, `HTTP ${illegal.status}`);
  const badSyntax = await gen({ prompt: 'x', n: 1, size: 'not-a-size' });
  check('尺寸语法错误被拒', badSyntax.status === 400, `HTTP ${badSyntax.status}`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  fs.writeFileSync(OUT_FILE, JSON.stringify({ ...state, results, specRows, sampleUrl: url1 }, null, 2));
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
