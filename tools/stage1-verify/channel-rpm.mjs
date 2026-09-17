#!/usr/bin/env node
// T1.7 验收：渠道 RPM（每分钟请求数）窗口限速
//
// 与并发维度的关键区别：**速率计数在窗口内不归还**——请求结束把它减回去的话，
// RPM 永远数不到 2，限速就形同虚设。本脚本用**串行**请求验证这一点：
// 串行意味着并发维度绝不会拦截，能被拦住的只可能是速率维度。
//
//   RPM=2，串行发 3 次 → 恰好 2 次 200 + 1 次 429（带 Retry-After）
//
// 用法：node tools/stage1-verify/channel-rpm.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000, APIGX_CAPACITY_ENABLED=true,
//       APIGX_CHANNEL_RPM_LIMIT=2)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const EXPECT_LIMIT = Number(argOf('--limit', '2'));

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
  if (!r.json?.data?.access_token) throw new Error(`${user} 登录失败：HTTP ${r.status}`);
  sessions[user] = { token: r.json.data.access_token };
  saveSessions();
  return 'Bearer ' + r.json.data.access_token;
}

async function main() {
  const root = await login('apigxroot', 'ApigxDev#2026');
  const results = [];
  const check = (name, ok, detail) => { results.push({ name, ok }); (ok ? pass : fail)(`${name}${detail ? ' — ' + detail : ''}`); };
  const stamp = Date.now().toString().slice(-6);
  const MODEL = 'stub/rpm-' + stamp;

  const channelName = 'RPM底座-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-rpm-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await call(`${BASE}/api/channel/?page=1&page_size=1000`, { auth: root });
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'RPM组-' + stamp } });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'RPM渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: root, body: { stable_key_id: 'rpm-' + stamp, name: 'rpm', key: 'sk-rpmkey-' + stamp },
  });
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });

  // 用**渠道策略**设置速率上限（而不是进程级环境变量）：
  // 并发维度明确设为「不限」，这样唯一能拦住请求的就只有速率维度，断言才有意义。
  // 同时这也顺带验证了「限额按渠道配置」这条路径——进程级开关无法表达"每个渠道不同上限"。
  const policyRes = await call(`${BASE}/api/apigx/channels/${shareChannelId}/capacity-policy`, {
    method: 'PUT', auth: root,
    body: { concurrency_mode: 3, rpm_mode: 4, rpm_limit: EXPECT_LIMIT },
  });
  if (policyRes.status !== 200) throw new Error(`设置渠道策略失败：${policyRes.status} ${policyRes.text.slice(0, 140)}`);
  info(`已设置渠道策略：并发=明确不限，RPM=${EXPECT_LIMIT}`);

  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'rpm probe' }] },
  });

  // 等模型可路由
  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('等待模型可路由超时');
  info('模型已可路由（注意：上面探测请求也会计入 RPM 窗口）');

  // 等待窗口翻页，保证计数从干净的一分钟开始
  // 策略带 30s 缓存，且窗口按分钟分桶：两者都要等，否则读到的是旧策略或旧窗口
  const nowSecond = new Date().getUTCSeconds();
  const waitMs = ((60 - nowSecond) % 60) * 1000 + 32000;
  info(`等待窗口翻页 + 策略缓存过期 ${Math.round(waitMs / 1000)}s ...`);
  await new Promise((r) => setTimeout(r, waitMs));

  // 串行发 limit+1 次：并发维度不可能拦截，能拦住的只可能是速率维度
  const statuses = [];
  for (let i = 0; i <= EXPECT_LIMIT; i++) {
    const r = await chat();
    statuses.push(r.status);
    if (r.status === 429 && i === EXPECT_LIMIT) {
      check('429 带 Retry-After', Boolean(r.headers.get('retry-after')), `Retry-After=${r.headers.get('retry-after')}`);
    }
  }
  const okCount = statuses.filter((s) => s === 200).length;
  const rejected = statuses.filter((s) => s === 429).length;
  info(`串行 ${statuses.length} 次 → ${statuses.join(',')}`);
  check(`串行请求恰好放行 ${EXPECT_LIMIT} 次（速率计数不归还）`, okCount === EXPECT_LIMIT, `通过 ${okCount} 次`);
  check('超出部分被 429 拒绝', rejected === 1, `被拒 ${rejected} 次`);

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
