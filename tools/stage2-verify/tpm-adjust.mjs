#!/usr/bin/env node
// T2.5 验收：TPM 预留按**实际用量**退回（结算期调整）
//
// 做一件事：建一条渠道并把 TPM 上限设得很大，发**一次**请求，然后由脚本直接读取
// Redis 的该渠道窗口计数并断言。任何请求、用量或 Redis 断言失败都会返回非零。
//
//   调整开启 → 计数应≈实际 token 数（本场景约几十）
//   调整关闭 → 计数是最坏情况预留（prompt + 输出上限 8192 ≈ 8200+）
//
// 用法：node tools/stage2-verify/tpm-adjust.mjs --expect adjusted|reserved
// 前置：网关需按期望值设置 APIGX_TPM_ADJUST_ENABLED

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const EXPECT = argOf('--expect', 'adjusted');
const REDIS_HOST = argOf('--redis-host', '127.0.0.1');
const REDIS_PORT = argOf('--redis-port', '6379');
const REDIS_DB = argOf('--redis-db', '0');
const SELF_TEST_FAILURE = argv.includes('--self-test-failure');

async function call(url, { method = 'GET', body, auth } = {}) {
  const h = { accept: 'application/json' };
  if (body !== undefined) h['content-type'] = 'application/json';
  if (auth) h.authorization = auth;
  const res = await fetch(url, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json = null; try { json = JSON.parse(text); } catch { /* 非 JSON */ }
  return { status: res.status, json, text };
}

let failureCount = 0;
const pass = (s) => console.log('  \x1b[32mPASS\x1b[0m  ' + s);
const fail = (s) => { failureCount++; console.log('  \x1b[31mFAIL\x1b[0m  ' + s); };
const info = (s) => console.log('  \x1b[36mINFO\x1b[0m  ' + s);

const fs = await import('node:fs');
const path = await import('node:path');
const { spawnSync } = await import('node:child_process');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const OUT_FILE = path.join(STATE_DIR, 'tpm-adjust.json');
const REDIS_CLI = argOf('--redis-cli', path.join(process.cwd(), '.toolchain', 'redis', 'redis-cli.exe'));
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }

function redisCli(args) {
  if (!fs.existsSync(REDIS_CLI)) throw new Error(`找不到 redis-cli：${REDIS_CLI}`);
  const result = spawnSync(REDIS_CLI, ['--raw', '-h', REDIS_HOST, '-p', REDIS_PORT, '-n', REDIS_DB, ...args], {
    encoding: 'utf8', windowsHide: true,
  });
  if (result.error) throw new Error(`redis-cli 启动失败：${result.error.message}`);
  if (result.status !== 0) throw new Error(`Redis 命令失败：${(result.stderr || result.stdout || '').trim()}`);
  return (result.stdout || '').trim();
}

function tpmKeys(baseChannelId) {
  const pattern = `apigx:cap:channel:${baseChannelId}:tpm:*`;
  // 仓库自带的 Redis 3.0 redis-cli 对 --scan 静默返回空；本用例的前缀只会
  // 命中单个测试渠道的分钟桶，因此用 KEYS 做受限查询，不能把空结果误判成无计数。
  const output = redisCli(['KEYS', pattern]);
  return output.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

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
  if (SELF_TEST_FAILURE) {
    fail('受控失败自测：验证脚本必须返回退出码 1');
    return;
  }
  if (!['adjusted', 'reserved'].includes(EXPECT)) throw new Error(`--expect 只支持 adjusted|reserved，收到 ${EXPECT}`);
  const root = await login('apigxroot', 'ApigxDev#2026');
  const stamp = Date.now().toString().slice(-6);
  const MODEL = 'stub/tpmadj-' + stamp;

  const channelName = 'TPMADJ底座-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: channelName, type: 1, key: 'sk-tpmadj-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  let base = null;
  for (let i = 0; i < 10 && !base; i++) {
    const list = await (async () => {
        // **翻页合并**：底座的列表接口把 page_size 封顶在 100，而开发库里测试渠道已 112+ 条，
        // 只查第一页会"查不到刚建的渠道"（实测踩过）。这里翻完所有页再合并，
        // 返回**同样的形状**，因此下游代码无需改动。
        const merged = { json: { data: { items: [] } }, status: 200, text: '' };
        for (let p = 1; p <= 40; p++) {
          const r = await call(`${BASE}/api/channel/?p=${p}&page_size=100`, { auth: root });
          const items = r.json?.data?.items ?? [];
          merged.json.data.items.push(...items);
          if (items.length < 100) break;
        }
        return merged;
      })();
    base = (list.json?.data?.items ?? []).find((c) => c?.name === channelName);
    if (!base) await new Promise((r) => setTimeout(r, 2000));
  }
  if (!base?.id) throw new Error('查不到底座渠道');

  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'TPMADJ组-' + stamp } });
  const ch = await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: g.json.data.id, base_channel_id: base.id, name: 'TPMADJ渠道-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
  });
  const shareChannelId = ch.json.data.id;
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/keys`, {
    method: 'POST', auth: root, body: { stable_key_id: 'tpmadj-' + stamp, name: 'k', key: 'sk-tpmadjup-' + stamp },
  });
  await call(`${BASE}/api/apigx/channels/${shareChannelId}/apply`, { method: 'POST', auth: root });
  // TPM 上限设得足够大：本用例只想看计数，不想被限流
  const policy = await call(`${BASE}/api/apigx/channels/${shareChannelId}/capacity-policy`, {
    method: 'PUT', auth: root, body: { tpm_mode: 4, tpm_limit: 1000000 },
  });
  if (policy.status !== 200) throw new Error('设置 TPM 策略失败：' + policy.text.slice(0, 140));

  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'tpm adjust probe' }] },
  });

  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  if (!routable) throw new Error('模型不可路由');

  // 等策略缓存过期（30s），确保请求时 TPM 上限已生效
  info('等待策略缓存过期（32s）...');
  await new Promise((r) => setTimeout(r, 32000));

  // 清掉该测试渠道尚未过期的旧分钟桶，确保下面的计数只属于这一笔请求。
  const oldKeys = tpmKeys(base.id);
  for (const key of oldKeys) redisCli(['DEL', key]);
  info(`已清理本测试渠道的旧 TPM 窗口键 ${oldKeys.length} 个`);

  const usageStart = Math.floor(Date.now() / 1000) - 1;
  const request = await chat();
  const okStatus = request.status === 200;
  (okStatus ? pass : fail)(`请求成功（用于产生一次 TPM 预留） — HTTP ${request.status}`);
  if (!okStatus) {
    info('响应：' + request.text.slice(0, 160));
    return;
  }

  // 用唯一模型 + 本次开始时间筛选，避免把相邻请求的“最新一条”误当成当前请求。
  let latest = null;
  for (let i = 0; i < 10 && !latest; i++) {
    const usage = await call(`${BASE}/api/apigx/usage?page_size=10&start=${usageStart}&model=${encodeURIComponent(MODEL)}`, { auth: root });
    latest = (usage.json?.data?.items ?? []).find((item) => item?.model === MODEL) ?? null;
    if (!latest) await new Promise((r) => setTimeout(r, 200));
  }
  const actualTokens = Number(latest?.total_tokens ?? 0);
  if (!latest || !Number.isFinite(actualTokens) || actualTokens <= 0) {
    fail(`找不到本次模型 ${MODEL} 的正数 token 用量，不能判断 TPM 调整`);
    return;
  }
  info(`本次用量：model=${latest.model} total_tokens=${actualTokens}`);

  // TPM 预留和结算都在请求返回前完成；稍等片刻后读取唯一的当前分钟桶。
  await new Promise((r) => setTimeout(r, 250));
  const keys = tpmKeys(base.id).sort((a, b) => Number(b.split(':').at(-1)) - Number(a.split(':').at(-1)));
  if (!keys.length) {
    fail(`Redis 中不存在 apigx:cap:channel:${base.id}:tpm:*，TPM 链路未形成闭环`);
    return;
  }
  const redisValue = Number(redisCli(['GET', keys[0]]));
  if (!Number.isSafeInteger(redisValue) || redisValue < 0) {
    fail(`Redis TPM 计数不是有效非负整数：key=${keys[0]} value=${redisValue}`);
    return;
  }

  if (EXPECT === 'adjusted') {
    (redisValue === actualTokens ? pass : fail)(`调整后 TPM 计数等于实际用量 — Redis=${redisValue} actual=${actualTokens}`);
  } else {
    (redisValue >= 8192 && redisValue > actualTokens ? pass : fail)(`未调整时保留最坏情况预留 — Redis=${redisValue} actual=${actualTokens}`);
  }

  fs.mkdirSync(STATE_DIR, { recursive: true });
  fs.writeFileSync(OUT_FILE, JSON.stringify({
    baseChannelId: base.id, shareChannelId, model: MODEL, expect: EXPECT,
    actualTokens, redisKey: keys[0], redisValue,
  }, null, 2));
  info(`已写出机器核对结果：${OUT_FILE}`);
}

main()
  .then(() => { if (failureCount > 0) process.exitCode = 1; })
  .catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
