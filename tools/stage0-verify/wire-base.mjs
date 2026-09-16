#!/usr/bin/env node
// T0.8 最小可运行后端路径：把底座接上替身上游，跑通一条真实转发链路并取证。
//
// 做的事（全部对本地替身，不产生任何真实上游费用）：
//   1. 初始化底座（创建 root 用户）
//   2. 登录拿会话
//   3. 建一条指向替身的 OpenAI 兼容渠道
//   4. 建一个平台令牌（平台 API Key）
//   5. 用该令牌发一条 /v1/chat/completions，并故意带上 X-APIGX-* 平台扩展头
//   6. 回查替身的取证记录，验证：
//        - 请求确实到达替身（最小可运行路径成立）
//        - 到达的模型名是供给的上游模型 ID
//        - 替身收到的上游 Key 就是配置里那一把（指纹一致）
//        - X-APIGX-* 头**没有**到达上游（证明出站前剥离；底座现状若未剥离，这里会暴露）
//
// 用法：
//   node tools/stage0-verify/wire-base.mjs
//   node tools/stage0-verify/wire-base.mjs --base http://127.0.0.1:3000 --stub http://127.0.0.1:8100

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };

const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');

const ROOT_USER = argOf('--user', 'apigxroot');
const ROOT_PASS = argOf('--pass', 'ApigxDev#2026');
const UPSTREAM_KEY = argOf('--upstream-key', 'sk-stub-KEY-AAA1');
const UPSTREAM_MODEL = argOf('--upstream-model', 'stub/model-x-202609');

let cookies = [];
// 底座 dashboard 鉴权只认 Authorization 头（middleware/auth.go:159 → classifyDashboardCredential），
// 不看 cookie；登录响应里的 data.access_token 就是那个内部会话 JWT。
let dashToken = null;

function rememberCookies(res) {
  const raw = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
  for (const c of raw) {
    const pair = c.split(';')[0];
    const name = pair.split('=')[0];
    cookies = cookies.filter((x) => x.split('=')[0] !== name);
    cookies.push(pair);
  }
}

async function call(url, { method = 'GET', body, headers = {}, auth } = {}) {
  const h = { accept: 'application/json', ...headers };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (cookies.length) h.cookie = cookies.join('; ');
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  rememberCookies(res);
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

const ok = (s) => `\x1b[32m${s}\x1b[0m`;
const bad = (s) => `\x1b[31m${s}\x1b[0m`;
const dim = (s) => `\x1b[2m${s}\x1b[0m`;

function step(n, title) { console.log(`\n=== ${n}. ${title} ===`); }

const results = [];
function check(name, passed, detail) {
  results.push({ name, passed, detail });
  console.log(`  ${passed ? ok('PASS') : bad('FAIL')}  ${name}${detail ? dim('  — ' + detail) : ''}`);
}

async function main() {
  step(1, '初始化状态');
  const setupInfo = await call(`${BASE}/api/setup`);
  console.log('  GET /api/setup ->', setupInfo.status, JSON.stringify(setupInfo.json?.data ?? setupInfo.json));
  const rootInit = setupInfo.json?.data?.root_init === true;

  step(2, '创建 root 用户');
  const alreadyInit = setupInfo.json?.data?.status === true;
  if (rootInit || alreadyInit) {
    console.log(dim(`  已初始化（root_init=${rootInit} status=${alreadyInit}），跳过创建`));
    check('初始化状态可用（已初始化）', true, '跳过创建步骤');
  } else {
    const r = await call(`${BASE}/api/setup`, {
      method: 'POST',
      body: { username: ROOT_USER, password: ROOT_PASS, confirmPassword: ROOT_PASS, SelfUseModeEnabled: true, DemoSiteEnabled: false },
    });
    console.log('  POST /api/setup ->', r.status, r.json?.message ?? r.text.slice(0, 160));
    check('创建 root 用户', r.json?.success === true, r.json?.message);
  }

  step(3, '登录');
  const login = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: ROOT_USER, password: ROOT_PASS } });
  console.log('  POST /api/user/login ->', login.status, login.json?.message ?? '');
  dashToken = login.json?.data?.access_token ?? null;
  check('登录成功', login.json?.success === true, `cookies=${cookies.length}`);
  check('取得 dashboard 访问令牌', typeof dashToken === 'string' && dashToken.length > 20, dashToken ? `${dashToken.slice(0, 18)}…` : 'none');
  if (login.json?.success !== true || !dashToken) throw new Error('登录失败或未返回 access_token，无法继续');
  const dashAuth = `Bearer ${dashToken}`;

  step(4, '本地开发选项（磁盘监控阈值）');
  // 本机 D: 盘占用 98.9%，底座 relay 链路上的 SystemPerformanceCheck 中间件
  // （middleware/performance.go:64-67）会对超过阈值的请求返回 503，属于**环境问题而非功能缺陷**。
  // 阈值来自 performance_setting.monitor_disk_threshold（默认 95），这里只为本地替身联调临时放宽。
  const optKey = 'performance_setting.monitor_disk_threshold';
  const opt = await call(`${BASE}/api/option/`, { method: 'PUT', auth: dashAuth, body: { key: optKey, value: '99' } });
  console.log(`  PUT /api/option/ ${optKey}=99 ->`, opt.status, opt.json?.message ?? '');
  check('放宽磁盘监控阈值（本地开发）', opt.json?.success === true, opt.json?.message);

  step(5, `建渠道（OpenAI 兼容 -> ${STUB}）`);
  const chBody = {
    type: 1,                      // constant.ChannelTypeOpenAI
    name: 'stub-openai-primary',
    key: UPSTREAM_KEY,
    base_url: STUB,
    models: UPSTREAM_MODEL,
    group: 'default',
    status: 1,
  };
  // AddChannelRequest{Mode, Channel}：必须给 mode，单 Key 用 "single"（controller/channel.go:652,764）
  const ch = await call(`${BASE}/api/channel/`, { method: 'POST', body: { mode: 'single', channel: chBody }, auth: dashAuth });
  console.log('  POST /api/channel/ ->', ch.status, ch.json?.message ?? ch.text.slice(0, 160));
  check('建渠道', ch.json?.success === true, ch.json?.message);

  step(6, '建平台令牌');
  // 注意：AddToken 绑定的是 tokenRequest{ Token ... }，请求体必须包一层 "token"
  const tk = await call(`${BASE}/api/token/`, {
    method: 'POST',
    auth: dashAuth,
    // tokenRequest 内嵌 model.Token（controller/token.go:35-38）：字段必须在顶层
    body: { name: 'apigx-stage0-verify', remain_quota: 0, unlimited_quota: true, expired_time: -1, model_limits_enabled: false },
  });
  console.log('  POST /api/token/ ->', tk.status, tk.json?.message ?? '');
  check('建令牌', tk.json?.success === true, tk.json?.message);

  // 令牌明文只在创建时可见，且要单独取：GET /api/token/ 找 id -> POST /api/token/:id/key
  let tokenKey = null;
  const list = await call(`${BASE}/api/token/`, { auth: dashAuth });
  const items = list.json?.data?.items ?? list.json?.data ?? [];
  const mine = Array.isArray(items) ? items.find((t) => t?.name === 'apigx-stage0-verify') : null;
  if (mine?.id) {
    const got = await call(`${BASE}/api/token/${mine.id}/key`, { method: 'POST', auth: dashAuth });
    tokenKey = got.json?.data?.key ?? null;
    console.log(dim(`  POST /api/token/${mine.id}/key -> ${got.status}`));
  }
  check('取得令牌明文', typeof tokenKey === 'string' && tokenKey.length > 8, tokenKey ? `${tokenKey.slice(0, 6)}…` : 'none');
  if (!tokenKey) throw new Error('拿不到令牌明文，无法继续');

  step(7, '清空替身取证并发送一条对话请求');
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const chat = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST',
    auth: `Bearer ${tokenKey}`,
    headers: { 'x-apigx-offering': 'offering_should_be_stripped', 'x-apigx-route': 'latency' },
    body: { model: UPSTREAM_MODEL, messages: [{ role: 'user', content: 'stage0 ping' }] },
  });
  console.log('  POST /v1/chat/completions ->', chat.status);
  console.log(dim('  ' + (chat.text || '').slice(0, 260)));
  check('网关返回 200', chat.status === 200, `status=${chat.status}`);
  check('返回体来自替身', typeof chat.text === 'string' && chat.text.includes('stub reply to'), chat.json?.choices?.[0]?.message?.content?.slice(0, 60));

  step(8, '替身取证核对');
  const rec = await call(`${STUB}/_control/requests?limit=10`);
  const data = rec.json;
  const last = data?.requests?.[data.requests.length - 1];
  if (!last) {
    check('替身收到请求', false, '没有任何记录');
  } else {
    console.log(dim('  ' + JSON.stringify({ path: last.path, upstreamKey: last.upstreamKey, platformHeaders: last.platformHeaders, body: last.body?.slice(0, 120) }, null, 0)));
    check('替身收到请求', true, last.path);
    let sentModel = null;
    try { sentModel = JSON.parse(last.body || '{}').model; } catch { /* ignore */ }
    check('上游收到的模型名正确', sentModel === UPSTREAM_MODEL, `model=${sentModel}`);
    check('上游 Key 指纹是配置的那一把', last.upstreamKey.includes(UPSTREAM_KEY.slice(-4)), last.upstreamKey);
    const leaked = Object.keys(last.platformHeaders || {});
    check('X-APIGX-* 平台扩展头未泄漏到上游', leaked.length === 0, leaked.length ? `泄漏: ${leaked.join(',')}` : '已剥离（或在网关侧未透传）');
  }

  step('结果汇总');
  const failed = results.filter((r) => !r.passed);
  console.log(`  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) {
    console.log(bad('  失败项：'));
    for (const f of failed) console.log(`    - ${f.name}${f.detail ? ' — ' + f.detail : ''}`);
    process.exitCode = 1;
  } else {
    console.log(ok('  全部通过：最小可运行后端路径成立（底座 -> 替身上游）'));
  }
}

main().catch((e) => {
  console.error(bad(`\n异常终止：${e.message}`));
  process.exitCode = 2;
});
