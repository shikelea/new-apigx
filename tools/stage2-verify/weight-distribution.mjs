#!/usr/bin/env node
// T2.10 验收：**权重真的影响底座选路**（路由的端到端流量分布）
//
// 做法：同一模型下两条健康供给（各自一把不同的上游 Key），
//   ① setup：建渠道/供给/Key，记录两项供给 ID（写到 .toolchain/_state/weight-dist.json）
//   ② 由外部（PowerShell + psql）给两条供给灌入**质量差异**（A 明显更好）
//   ③ apply：调用 route-apply 按"可靠性"策略把名次写成底座渠道权重，然后发 N 次请求
//   ④ 外部再读两条供给的样本数增量，验证流量按权重倾斜（A 明显多于 B）
//
// 为什么要把质量样本交给外部灌：node 在本环境不能可靠地抓子进程输出（沙箱限制），
// 而 psql 直接写库最省事；脚本只负责 HTTP 部分，职责边界清楚。
//
// 用法：
//   node tools/stage2-verify/weight-distribution.mjs --phase=setup
//   node tools/stage2-verify/weight-distribution.mjs --phase=apply --rounds=18

const argv = process.argv.slice(2);
const argOf = (n, d) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : d; };
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const PHASE = argOf('--phase', 'setup');
const ROUNDS = Number(argOf('--rounds', '18'));
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
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const DIST_FILE = path.join(STATE_DIR, 'weight-dist.json');
const sessions = (() => { try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; } })();
function saveSessions() { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2)); }
function saveDist(data) { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(DIST_FILE, JSON.stringify(data, null, 2)); }
function loadDist() { return JSON.parse(fs.readFileSync(DIST_FILE, 'utf8')); }

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

async function platformKey(root) {
  const tokens = await call(`${BASE}/api/token/`, { auth: root });
  const items0 = tokens.json?.data?.items ?? [];
  const vt = (Array.isArray(items0) ? items0 : []).find((t) => t?.name === 'apigx-stage0-verify') ?? (Array.isArray(items0) ? items0 : [])[0];
  const keyRes = await call(`${BASE}/api/token/${vt.id}/key`, { method: 'POST', auth: root });
  return keyRes.json?.data?.key;
}

async function main() {
  if (SELF_TEST_FAILURE) {
    fail('受控失败自测：验证脚本必须返回退出码 1');
    return;
  }
  const root = await login('apigxroot', 'ApigxDev#2026');
  const stamp = Date.now().toString().slice(-6);

  if (PHASE === 'setup') {
    const MODEL = 'stub/weight-' + stamp;
    const mkBase = async (tag) => {
      const name = 'WGT-' + tag + '-' + stamp;
      await call(`${BASE}/api/channel/`, {
        method: 'POST', auth: root,
        body: { mode: 'single', channel: { name, type: 1, key: 'sk-' + tag + '-' + stamp, base_url: 'http://127.0.0.1:8100', models: MODEL, groups: ['default'], status: 1, priority: 0, weight: 1 } },
      });
      for (let i = 0; i < 10; i++) {
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
        const hit = (list.json?.data?.items ?? []).find((c) => c?.name === name);
        if (hit) return hit;
        await new Promise((r) => setTimeout(r, 2000));
      }
      throw new Error('查不到底座渠道 ' + name);
    };
    const baseA = await mkBase('a');
    const baseB = await mkBase('b');

    const g = await call(`${BASE}/api/apigx/share-groups`, { method: 'POST', auth: root, body: { name: 'WGT组-' + stamp, default_visibility: 2 } });
    const seed = async (tag, baseId) => {
      const ch = await call(`${BASE}/api/apigx/channels`, {
        method: 'POST', auth: root,
        body: { share_group_id: g.json.data.id, base_channel_id: baseId, name: 'WGT渠道-' + tag + '-' + stamp, protocol: 'openai', base_url: 'http://127.0.0.1:8100' },
      });
      const channelId = ch.json.data.id;
      await call(`${BASE}/api/apigx/channels/${channelId}/keys`, {
        method: 'POST', auth: root, body: { stable_key_id: 'wgt-' + tag + '-' + stamp, name: 'k', key: 'sk-' + tag + '-up-' + stamp },
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
    const a = await seed('a', baseA.id);
    const b = await seed('b', baseB.id);

    saveDist({ model: MODEL, stamp, a, b });
    info(`已建立两条供给：model=${MODEL}`);
    info(`  A: offering=${a.offeringId} channel=${a.channelId}`);
    info(`  B: offering=${b.offeringId} channel=${b.channelId}`);
    info('下一步：灌入质量差异 → 再执行 --phase=apply');
    return;
  }

  if (PHASE === 'apply') {
    const dist = loadDist();
    const key = await platformKey(root);
    const chat = () => call(`${BASE}/v1/chat/completions`, {
      method: 'POST', auth: 'Bearer ' + key,
      body: { model: dist.model, messages: [{ role: 'user', content: 'weight probe' }] },
    });

    // 等模型可路由
    let routable = false;
    for (let i = 0; i < 24 && !routable; i++) {
      const probe = await chat();
      if (!probe.text.includes('model_not_found')) { routable = true; break; }
      await new Promise((r) => setTimeout(r, 5000));
    }
    if (!routable) throw new Error('模型不可路由');

    // 查询应用前的排名（作为对照证据）
    const before = await call(`${BASE}/api/apigx/route-explain?model=${encodeURIComponent(dist.model)}&strategy=reliability`, { auth: root });
    const beforeCandidates = before.json?.data?.candidates ?? [];
    info('应用前排名：' + beforeCandidates.map((x) => `rank=${x.rank} score=${Math.round(x.score)} ppm=${x.reliability_lower_bound_ppm} samples=${x.sample_count}`).join(' | '));

    // 应用排名：把名次写成底座渠道权重（平台侧动作，显式 dry_run=false）
    const applied = await call(`${BASE}/api/apigx/route-apply`, {
      method: 'POST', auth: root,
      body: { model: dist.model, strategy: 'reliability', dry_run: false },
    });
    const data = applied.json?.data ?? {};
    if (applied.status !== 200) throw new Error('应用排名失败：' + applied.text.slice(0, 160));
    info(`应用排名：updated=${data.updated} dry_run=${data.dry_run}`);

    // 发请求，观察流量
    const statuses = [];
    for (let i = 0; i < ROUNDS; i++) {
      const r = await chat();
      statuses.push(r.status);
      await new Promise((res) => setTimeout(res, 120));
    }
    const okCount = statuses.filter((s) => s === 200).length;
    (okCount === ROUNDS ? pass : fail)(`应用权重后请求全部成功 — ${okCount}/${ROUNDS}`);

    // 质量样本增量由外部（psql）读取并断言倾斜；这里只报告给外部用
    saveDist({ ...dist, appliedUpdated: data.updated, requests: ROUNDS, statuses });
    info('流量增量与倾斜断言由外部 psql 读取（见 PowerShell 步骤）');
    return;
  }

  throw new Error('未知阶段：' + PHASE);
}

main()
  .then(() => { if (failureCount > 0) process.exitCode = 1; })
  .catch((e) => { console.error('\x1b[31m异常：' + e.message + '\x1b[0m'); process.exitCode = 2; });
