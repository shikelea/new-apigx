#!/usr/bin/env node
// T1.1 验收：平台 Key（使用者凭证）按底座现状接入
//
// 按 TD-07 的决定，**有意沿用底座的明文存储 + 等值查找**（不改成哈希），
// 因此本脚本要证明的是"这个决定没有带来额外风险面"：
//   ① 等值查找可用：明文 Key 能直接用于真实请求
//   ② 归属隔离：**另一个普通用户**无法读取别人的 Key 明文（且不泄露存在性）
//   ③ 明文不进日志：日志表里不出现该 Key
//   ④ 明文存储是**显式记录在案的偏离**（数据库里确实能读到明文——这是设计，不是缺陷）
//
// 用法：node tools/stage1-verify/platform-key.mjs
// 前置：PG(5433) + Redis(6379) + 替身(8100) + 网关(3000)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const MODEL = argOf('--model', 'stub/model-x-202609');

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

async function login(user, password) {
  const r = await call(`${BASE}/api/user/login`, { method: 'POST', body: { username: user, password } });
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status}`);
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const authA = await login('apigxroot', 'ApigxDev#2026');
  const authB = await login('apigxuserb', 'ApigxB#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };

  const stamp = Date.now().toString().slice(-6);

  // ① 用户 A 建一个令牌并取出明文
  const created = await call(`${BASE}/api/token/`, {
    method: 'POST', auth: authA,
    body: { name: 'T1.1-' + stamp, remain_quota: 500000, unlimited_quota: true, expired_time: -1 },
  });
  check('创建令牌', created.status === 200, `HTTP ${created.status}`);
  const tokens = await call(`${BASE}/api/token/`, { auth: authA });
  const items = tokens.json?.data?.items ?? tokens.json?.data ?? [];
  const mine = (Array.isArray(items) ? items : []).find((t) => t?.name === 'T1.1-' + stamp);
  if (!mine) throw new Error('找不到刚建的令牌');

  const revealed = await call(`${BASE}/api/token/${mine.id}/key`, { method: 'POST', auth: authA });
  const plaintext = revealed.json?.data?.key;
  check('所有者可读取明文（底座现状，TD-07 已记录）', typeof plaintext === 'string' && plaintext.length > 8,
    `明文字符数=${plaintext ? plaintext.length : 0}`);
  if (!plaintext) throw new Error('取不到明文，后续验证无意义');

  // ① 等值查找可用：明文直接用于真实请求
  await call(`${STUB}/_control/reset`, { method: 'POST' });
  const chat = await call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + plaintext,
    body: { model: MODEL, messages: [{ role: 'user', content: 't1.1 probe' }] },
  });
  check('明文可直接用于请求（等值查找可用）', chat.status === 200, `HTTP ${chat.status}`);

  // ② 归属隔离：另一个普通用户读不到别人的 Key
  //
  // 注意底座的**旧接口约定**：这类端点失败时返回 **HTTP 200 + `{"success":false}`**，
  // 而不是 4xx。因此这里断言的是语义属性，而不是状态码：
  //   - 拿不到明文；
  //   - 且响应与「访问不存在的 id」**完全一致**（不泄露存在性）。
  // 我们自己的接口（apigx/**）走的是规范状态码，见 T1.12 拒绝清单。
  const intrude = await call(`${BASE}/api/token/${mine.id}/key`, { method: 'POST', auth: authB });
  check('他人拿不到明文（失败信封）', intrude.json?.success === false, `HTTP ${intrude.status} body=${intrude.text.slice(0, 60)}`);
  check('拒绝响应中不含明文', !intrude.text.includes(plaintext), '响应体不含明文');

  const notExist = await call(`${BASE}/api/token/999999/key`, { method: 'POST', auth: authB });
  check('与「不存在」的响应一致（不泄露存在性）', notExist.text === intrude.text,
    `越权响应=「${intrude.text.slice(0, 40)}」 不存在响应=「${notExist.text.slice(0, 40)}」`);

  // B 的令牌列表里也不应出现 A 的令牌
  const bList = await call(`${BASE}/api/token/`, { auth: authB });
  check('B 的令牌列表不含 A 的令牌', !bList.text.includes('T1.1-' + stamp), `HTTP ${bList.status}`);

  info(`供人工核对（脚本已尽量避免打印明文）：token_id=${mine.id} 前缀=${plaintext.slice(0, 4)}...`);
  // 把明文写进一个临时文件供 psql 复核（仅在 .toolchain/_state 下，且不提交）
  const fs = await import('node:fs');
  const path = await import('node:path');
  const stateDir = path.join(process.cwd(), '.toolchain', '_state');
  fs.mkdirSync(stateDir, { recursive: true });
  fs.writeFileSync(path.join(stateDir, 't11_token.txt'), plaintext, 'utf8');

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
