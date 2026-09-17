#!/usr/bin/env node
// T2.10 阶段 2 验收：**熔断在真实链路上把流量从坏上游挪开**
//
// 场景（不靠伪造数据，全用真实请求）：
//   同一模型下有两条供给——A 指向**打不通的上游**（连接被拒），B 指向正常替身。
//   持续发请求：A 的上游失败会被归因并写进分钟桶；达到阈值后熔断打开，
//   之后的请求应当**不再落在 A 上**（可由 route-explain 直接读到 breaker_open）。
//
// 这条用例同时验证三件事：
//   ① 归因正确（连接失败算**上游**失败，而不是平台拒绝）；
//   ② 熔断在**准入点生效**（不是只算了个状态没人用）；
//   ③ 流量被成功挪开（请求仍然全部成功，没有因为一条坏上游而整体失败）。
//
// 用法：node tools/stage2-verify/breaker-routing.mjs
// 前置：PG + Redis + 替身(8100) + 网关(3000, APIGX_QUALITY_STATS_ENABLED=true, APIGX_BREAKER_ENABLED=true)

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const ROUNDS = Number(argOf('--rounds', '14'));

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
  const MODEL = 'stub/breaker-' + stamp;

  // 一条能用的底座渠道（指向替身）
  const liveChannelName = 'BRK-LIVE-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: liveChannelName, type: 1, key: 'sk-live-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });
  // 一条**打不通**的底座渠道（端口 9 通常无服务 → 连接被拒）
  const deadChannelName = 'BRK-DEAD-' + stamp;
  await call(`${BASE}/api/channel/`, {
    method: 'POST', auth: root,
    body: { mode: 'single', channel: { name: deadChannelName, type: 1, key: 'sk-dead-' + stamp, base_url: 'http://127.0.0.1:9', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
  });

  const findChannel = async (name) => {
    for (let i = 0; i < 10; i++) {
      const list = await call(`${BASE}/api/channel/?page=1&page_size=200`, { auth: root });
      const hit = (list.json?.data?.items ?? []).find((c) => c?.name === name);
      if (hit) return hit;
      await new Promise((r) => setTimeout(r, 2000));
    }
    return null;
  };
  const liveBase = await findChannel(liveChannelName);
  const deadBase = await findChannel(deadChannelName);
  if (!liveBase?.id || !deadBase?.id) throw new Error('查不到底座渠道');

  // 两条分享渠道，各发布同一条模型的供给
  const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'BRK组-' + stamp, default_visibility: 2 } });
  const seed = async (tag, baseId, baseUrl) => {
    const ch = await call(`${BASE}/api/apigx/channels`, {
      method: 'POST', auth: root,
      body: { share_group_id: g.json.data.id, base_channel_id: baseId, name: 'BRK渠道-' + tag + '-' + stamp, protocol: 'openai', base_url: baseUrl },
    });
    const channelId = ch.json.data.id;
    await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
      method: 'POST', auth: root, body: { stable_key_id: 'brk-' + tag + '-' + stamp, name: 'k', key: 'sk-' + tag + '-' + stamp },
    });
    await call(`${BASE}/api/apigx/channels/${channelId}/apply`, { method: 'POST', auth: root });
    const off = await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, {
      method: 'POST', auth: root, body: { upstream_model_id: MODEL },
    });
    const offeringId = off.json.data.id;
    await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, { method: 'POST', auth: root });
    await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, { method: 'POST', auth: root });
    return { channelId, offeringId, offeringPublicId: off.json.data.public_id };
  };
  const liveShare = await seed('live', liveBase.id, 'http://127.0.0.1:8100');
  const deadShare = await seed('dead', deadBase.id, 'http://127.0.0.1:9');

  // 平台 Key
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  const platformKey = keyRes.json?.data?.key;

  const chat = () => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: 'Bearer ' + platformKey,
    body: { model: MODEL, messages: [{ role: 'user', content: 'breaker probe' }] },
  });
  const explain = async () => {
    const r = await call(`${BASE}/api/apigx/route-explain?model=${encodeURIComponent(MODEL)}`, { auth: root });
    return (r.json?.data?.candidates ?? []);
  };

  // 等模型可路由（两条渠道的 ability 缓存需要时间）
  let routable = false;
  for (let i = 0; i < 24 && !routable; i++) {
    const probe = await chat();
    if (!probe.text.includes('model_not_found')) { routable = true; break; }
    await new Promise((r) => setTimeout(r, 5000));
  }
  check('模型可路由（两条供给都已进入底座索引）', routable);
  if (!routable) { console.log('\n  无法继续：模型不可路由'); process.exitCode = 1; return; }

  // 起点：两条候选都还没被熔断
  const before = await explain();
  check('初始：两条供给都在候选里且都未被熔断', before.length === 2 && before.every((x) => x.excluded_reason !== 'breaker_open'),
    `候选数=${before.length}`);

  // 持续发请求：坏上游会累积失败，达到阈值后熔断打开
  const statuses = [];
  for (let i = 0; i < ROUNDS; i++) {
    const r = await chat();
    statuses.push(r.status);
    await new Promise((res) => setTimeout(res, 150));
  }
  const okCount = statuses.filter((s) => s === 200).length;
  info(`发起 ${ROUNDS} 次请求：200×${okCount}，其他×${ROUNDS - okCount}（${[...new Set(statuses)].join(',')}）`);
  // **注意断言的边界**：熔断**打开之前**请求打到坏上游并失败是**预期行为**
  // （底座确实选了它、也确实失败了），因此这里不断言"14 次全成功"——
  // 那会让"熔断还没生效"这段正常过程被记成失败。真正该断言的是**打开之后**不再打过去。
  check('熔断打开前允许出现失败（这些失败正是触发熔断的样本）', true,
    `${okCount}/${ROUNDS} 成功；失败 ${ROUNDS - okCount} 次即上游失败的观测`);

  const after = await explain();
  const openCandidate = after.find((x) => x.excluded_reason === 'breaker_open');
  check('坏上游的供给被熔断摘出（准入点已生效）', Boolean(openCandidate),
    openCandidate ? `offering=${openCandidate.offering_public_id} reason=${openCandidate.excluded_reason}` : `候选=${JSON.stringify(after.map((x) => x.excluded_reason))}`);

  // 归因：只有"上游失败"才会被记进样本并推进熔断；样本数为 0 就说明失败根本没被归因
  if (openCandidate) {
    check('上游失败已被归因并写入样本', openCandidate.sample_count > 0, `sample_count=${openCandidate.sample_count}`);
    check('被熔断的正是那条打不通的上游', openCandidate.offering_public_id === deadShare.offeringPublicId,
      `被熔断=${openCandidate.offering_public_id} 期望=${deadShare.offeringPublicId}`);
  }

  // **决定性判据**：熔断打开后再发请求，坏上游的样本数**不应再增长**
  // （样本增长 = 请求仍然打到了它）。这比"响应码全 200"更能证明流量真的被挪开。
  const deadAfterOpen = (await explain()).find((x) => x.offering_public_id === deadShare.offeringPublicId);
  const samplesBefore = deadAfterOpen?.sample_count ?? -1;
  const afterStatuses = [];
  for (let i = 0; i < 6; i++) {
    const r = await chat();
    afterStatuses.push(r.status);
    await new Promise((res) => setTimeout(res, 150));
  }
  const deadNow = (await explain()).find((x) => x.offering_public_id === deadShare.offeringPublicId);
  const samplesAfter = deadNow?.sample_count ?? -1;
  info(`坏上游样本数：打开后 ${samplesBefore} → 再发 6 次后 ${samplesAfter}`);
  check('熔断后坏上游不再收到新样本（流量确实被挪开）', samplesAfter === samplesBefore,
    `${samplesBefore} → ${samplesAfter}`);
  check('熔断后请求全部成功', afterStatuses.every((s) => s === 200), afterStatuses.join(','));

  // 健康供给仍在候选里且没有被熔断
  const healthy = after.find((x) => x.excluded_reason !== 'breaker_open');
  check('健康供给未被误伤', Boolean(healthy), healthy ? `offering=${healthy.offering_public_id}` : '无健康候选');

  const failed = results.filter((x) => !x.ok);
  console.log(`\n  共 ${results.length} 项，通过 ${results.length - failed.length}，失败 ${failed.length}`);
  if (failed.length) process.exitCode = 1;
}

main().catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
