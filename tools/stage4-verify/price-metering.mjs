#!/usr/bin/env node
// Stage D acceptance: New API-local price matching plus six-capability metering.
//
// The upstream is the repository's local stub. Each request pins the dedicated
// offering created by this run, so existing channels cannot affect attribution.

import fs from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
const argOf = (name, fallback) => {
  const index = argv.indexOf(name);
  return index >= 0 && argv[index + 1] ? argv[index + 1] : fallback;
};
const BASE = argOf('--base', 'http://127.0.0.1:3000');
const STUB = argOf('--stub', 'http://127.0.0.1:8100');
const ROOT_USER = argOf('--root-user', 'apigxroot');
const ROOT_PASSWORD = argOf('--root-password', 'ApigxDev#2026');
const STATE_DIR = path.join(process.cwd(), '.toolchain', '_state');
const SESSION_FILE = path.join(STATE_DIR, 'sessions.json');
const OUT_FILE = path.join(STATE_DIR, 'price-metering.json');

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const pass = (message) => console.log(`  \x1b[32mPASS\x1b[0m  ${message}`);
const fail = (message) => console.log(`  \x1b[31mFAIL\x1b[0m  ${message}`);
const info = (message) => console.log(`  \x1b[36mINFO\x1b[0m  ${message}`);

async function call(url, { method = 'GET', body, auth, form, binary = false, timeoutMs = 120000 } = {}) {
  const headers = { accept: 'application/json' };
  let payload;
  if (form) {
    payload = form;
  } else if (body !== undefined) {
    headers['content-type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  if (auth) headers.authorization = auth;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, { method, headers, body: payload, signal: controller.signal });
    if (binary) {
      const bytes = new Uint8Array(await response.arrayBuffer());
      return { status: response.status, bytes, text: `[binary ${bytes.length} bytes]`, json: null };
    }
    const text = await response.text();
    let json = null;
    try { json = JSON.parse(text); } catch { /* non-JSON response */ }
    return { status: response.status, text, json };
  } finally {
    clearTimeout(timer);
  }
}

const sessions = (() => {
  try { return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8')); } catch { return {}; }
})();

function saveSessions() {
  fs.mkdirSync(STATE_DIR, { recursive: true });
  fs.writeFileSync(SESSION_FILE, JSON.stringify(sessions, null, 2));
}

async function login() {
  const cached = sessions[ROOT_USER];
  if (cached?.token) {
    const probe = await call(`${BASE}/api/user/self`, { auth: `Bearer ${cached.token}` });
    if (probe.status === 200) return `Bearer ${cached.token}`;
  }
  const response = await call(`${BASE}/api/user/login`, {
    method: 'POST', body: { username: ROOT_USER, password: ROOT_PASSWORD },
  });
  const token = response.json?.data?.access_token;
  if (!token) throw new Error(`root login failed: HTTP ${response.status}`);
  sessions[ROOT_USER] = { token };
  saveSessions();
  return `Bearer ${token}`;
}

function expectData(response, label) {
  if (response.status !== 200 || response.json?.success === false || response.json?.data == null) {
    throw new Error(`${label}: HTTP ${response.status} ${response.text.slice(0, 240)}`);
  }
  return response.json.data;
}

async function allBaseChannels(auth) {
  const result = [];
  for (let page = 1; page <= 60; page++) {
    const response = await call(`${BASE}/api/channel/?p=${page}&page_size=100`, { auth });
    const items = expectData(response, 'list base channels').items ?? [];
    result.push(...items);
    if (items.length < 100) break;
  }
  return result;
}

async function createBaseChannel(auth, channel) {
  const response = await call(`${BASE}/api/channel/`, {
    method: 'POST', auth, body: { mode: 'single', channel },
  });
  if (response.status !== 200 || response.json?.success === false) {
    throw new Error(`create base channel: HTTP ${response.status} ${response.text.slice(0, 240)}`);
  }
  for (let attempt = 0; attempt < 12; attempt++) {
    const found = (await allBaseChannels(auth)).find((item) => item?.name === channel.name);
    if (found?.id) return found;
    await sleep(1000);
  }
  throw new Error(`base channel ${channel.name} was not visible after creation`);
}

async function createOffering(auth, channelId, model) {
  const created = expectData(await call(`${BASE}/api/apigx/channels/${channelId}/offerings`, {
    method: 'POST', auth, body: { upstream_model_id: model },
  }), `create offering ${model}`);
  return created.id;
}

async function publishOffering(auth, offeringId, model) {
  expectData(await call(`${BASE}/api/apigx/offerings/${offeringId}/submit`, {
    method: 'POST', auth,
  }), `submit offering ${model}`);
  expectData(await call(`${BASE}/api/apigx/offerings/${offeringId}/publish`, {
    method: 'POST', auth, body: { reason_code: 'stage_d_price_metering_acceptance' },
  }), `publish offering ${model}`);
}

async function setManualTokenPrice(auth, offeringId, model) {
  expectData(await call(`${BASE}/api/apigx/price-rules`, {
    method: 'PUT', auth,
    body: {
      scope_type: 0,
      scope_id: offeringId,
      currency: 'USD',
      input_usd_per_million: 1,
      output_usd_per_million: 2,
      lock: false,
    },
  }), `set manual fallback price for ${model}`);
}

async function createPlatformToken(auth, name) {
  const created = await call(`${BASE}/api/token/`, {
    method: 'POST', auth,
    body: { name, remain_quota: 0, unlimited_quota: true, expired_time: -1, model_limits_enabled: false },
  });
  if (created.status !== 200 || created.json?.success === false) {
    throw new Error(`create platform token: HTTP ${created.status} ${created.text.slice(0, 200)}`);
  }
  const listed = expectData(await call(`${BASE}/api/token/?p=1&page_size=100`, { auth }), 'list platform tokens');
  const token = (listed.items ?? listed ?? []).find((item) => item?.name === name);
  if (!token?.id) throw new Error('created platform token was not found');
  const revealed = expectData(await call(`${BASE}/api/token/${token.id}/key`, { method: 'POST', auth }), 'reveal platform token');
  if (!revealed.key) throw new Error('platform token key was not returned');
  return { id: token.id, key: revealed.key };
}

async function pinOffering(auth, tokenId, offeringId) {
  expectData(await call(`${BASE}/api/apigx/platform-keys/${tokenId}/preferences`, {
    method: 'PUT', auth,
    body: {
      scope_mode: 2,
      pinned_offering_id: offeringId,
      allowed_model_ids: [],
      allowed_owner_user_ids: [],
      allow_fallback: false,
      default_route_mode: 'auto',
    },
  }), `pin offering ${offeringId}`);
}

async function retryRoutable(operation, label) {
  let latest;
  for (let attempt = 0; attempt < 20; attempt++) {
    latest = await operation();
    if (latest.status === 200) return latest;
    if (latest.status !== 503 && latest.status !== 429) return latest;
    await sleep(1500);
  }
  info(`${label} did not become routable: ${latest?.text?.slice(0, 180) ?? ''}`);
  return latest;
}

function wavBlob(seconds = 1, sampleRate = 8000) {
  const sampleCount = Math.max(1, Math.floor(seconds * sampleRate));
  const dataLength = sampleCount * 2;
  const bytes = new Uint8Array(44 + dataLength);
  const view = new DataView(bytes.buffer);
  const text = (offset, value) => [...value].forEach((char, index) => view.setUint8(offset + index, char.charCodeAt(0)));
  text(0, 'RIFF');
  view.setUint32(4, 36 + dataLength, true);
  text(8, 'WAVE');
  text(12, 'fmt ');
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  text(36, 'data');
  view.setUint32(40, dataLength, true);
  return new Blob([bytes], { type: 'audio/wav' });
}

function hasUsage(item, capability, dimension, minimum = 1) {
  return (item?.usage ?? []).some((usage) =>
    Number(usage.capability) === capability &&
    Number(usage.dimension) === dimension &&
    Number(usage.quantity_milli) >= minimum);
}

async function main() {
  const root = await login();
  const stubState = await call(`${STUB}/_control/state`);
  if (stubState.status !== 200) throw new Error(`stub is unavailable: HTTP ${stubState.status}`);
  await call(`${STUB}/_control/reset`, { method: 'POST', body: {} });
  await call(`${STUB}/_control/profile`, {
    method: 'POST', body: { delayMs: 0, ttftMs: 0, chunkDelayMs: 0, status: 0, hang: false,
      usage: { prompt_tokens: 12, completion_tokens: 34, total_tokens: 46 } },
  });

  const stamp = Date.now().toString().slice(-9);
  const models = {
    chat: 'glm-5.3-flash',
    image: 'dall-e-3',
    embedding: 'Qwen3-Embedding-0.6B',
    rerank: `stage-d-rerank-${stamp}`,
    stt: 'whisper-1',
    tts: 'tts-1',
    expression: 'gpt-6-astra',
    video: 'kling-v1',
  };
  const startedAt = Math.floor(Date.now() / 1000) - 10;

  const base = await createBaseChannel(root, {
    name: `D5-METER-${stamp}`,
    type: 1,
    key: `sk-d5-base-${stamp}`,
    base_url: STUB,
    models: Object.values(models).filter((model) => model !== models.video).join(','),
    groups: ['default'], status: 1, priority: 0, weight: 1,
  });
  const group = expectData(await call(`${BASE}/api/apigx/share-groups`, {
    method: 'POST', auth: root,
    body: { name: `D5 price and metering ${stamp}`, default_visibility: 2 },
  }), 'create share group');
  expectData(await call(`${BASE}/api/apigx/share-groups/${group.id}/publish-status`, {
    method: 'POST', auth: root, body: { status: 2 },
  }), 'publish share group');
  const channel = expectData(await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: group.id, base_channel_id: base.id, name: `D5 sync ${stamp}`, protocol: 'openai', base_url: STUB },
  }), 'create shared channel');
  expectData(await call(`${BASE}/api/apigx/channels/${channel.id}/keys`, {
    method: 'POST', auth: root,
    body: { stable_key_id: `d5-sync-${stamp}`, name: 'D5 sync key', key: `sk-d5-upstream-${stamp}` },
  }), 'create shared channel key');
  expectData(await call(`${BASE}/api/apigx/channels/${channel.id}/apply`, {
    method: 'POST', auth: root,
  }), 'apply shared channel key pool');

  const offerings = {};
  for (const [kind, model] of Object.entries(models)) {
    if (kind === 'video') continue;
    offerings[kind] = await createOffering(root, channel.id, model);
  }

  const preview = expectData(await call(`${BASE}/api/apigx/channels/${channel.id}/price-match/preview`, {
    method: 'POST', auth: root,
  }), 'preview local prices');
  const candidates = preview.candidates ?? [];
  const locallySourced = candidates.filter((candidate) =>
    ['matched', 'expression_requires_manual'].includes(candidate.status));
  const localSourcesOK = locallySourced.length > 0 && locallySourced.every((candidate) =>
    String(candidate.source_ref ?? '').startsWith('new-api/'));
  (localSourcesOK ? pass : fail)(`price preview uses only New API local sources (${locallySourced.length} candidates)`);

  const expression = candidates.find((candidate) => Number(candidate.offering_id) === Number(offerings.expression));
  const expressionOK = expression?.status === 'expression_requires_manual' &&
    expression?.confirmable === false && Boolean(expression?.expression_hash);
  (expressionOK ? pass : fail)('dynamic billing expression requires manual confirmation');

  const selections = candidates
    .filter((candidate) => candidate.status === 'matched' && candidate.confirmable && candidate.candidate_token)
    .map((candidate) => ({ offering_id: candidate.offering_id, candidate_token: candidate.candidate_token }));
  if (selections.length === 0) throw new Error('local price preview returned no confirmable candidates');
  const confirmed = expectData(await call(`${BASE}/api/apigx/channels/${channel.id}/price-match/confirm`, {
    method: 'POST', auth: root, body: { selections },
  }), 'confirm local prices');
  const priceConfirmOK = Number(confirmed.applied) === selections.length;
  (priceConfirmOK ? pass : fail)(`confirmed ${confirmed.applied}/${selections.length} immutable local price snapshots`);

  // Publishing correctly requires an accepted price snapshot. Models whose
  // dynamic expression cannot be safely flattened receive an explicit manual
  // price only after the preview assertion above; the expression fixture stays
  // unpublished so it remains available as the manual-required proof.
  const confirmedOfferings = new Set(selections.map((selection) => Number(selection.offering_id)));
  for (const kind of ['chat', 'image', 'embedding', 'rerank', 'stt', 'tts']) {
    if (!confirmedOfferings.has(Number(offerings[kind]))) {
      await setManualTokenPrice(root, offerings[kind], models[kind]);
    }
    await publishOffering(root, offerings[kind], models[kind]);
  }

  const videoBase = await createBaseChannel(root, {
    name: `D5-VIDEO-${stamp}`,
    type: 61,
    key: `sk-d5-video-base-${stamp}`,
    base_url: STUB,
    models: models.video,
    groups: ['default'], status: 1, priority: 0, weight: 1,
    setting: JSON.stringify({ task_plugin_key: 'kling' }),
  });
  const videoChannel = expectData(await call(`${BASE}/api/apigx/channels`, {
    method: 'POST', auth: root,
    body: { share_group_id: group.id, base_channel_id: videoBase.id, name: `D5 video ${stamp}`, protocol: 'openai', base_url: STUB },
  }), 'create video shared channel');
  expectData(await call(`${BASE}/api/apigx/channels/${videoChannel.id}/keys`, {
    method: 'POST', auth: root,
    body: { stable_key_id: `d5-video-${stamp}`, name: 'D5 video key', key: `sk-d5-video-${stamp}` },
  }), 'create video key');
  expectData(await call(`${BASE}/api/apigx/channels/${videoChannel.id}/capacity-policy`, {
    method: 'PUT', auth: root, body: { concurrency_mode: 4, concurrency_value: 4 },
  }), 'configure video capacity');
  expectData(await call(`${BASE}/api/apigx/channels/${videoChannel.id}/apply`, {
    method: 'POST', auth: root,
  }), 'apply video key pool');
  offerings.video = await createOffering(root, videoChannel.id, models.video);
  await setManualTokenPrice(root, offerings.video, models.video);
  await publishOffering(root, offerings.video, models.video);

  const platformToken = await createPlatformToken(root, `apigx-stage-d-${stamp}`);
  const apiAuth = `Bearer ${platformToken.key}`;
  const checks = [];
  const check = (name, ok, detail = '') => {
    checks.push({ name, ok, detail });
    (ok ? pass : fail)(`${name}${detail ? ` — ${detail}` : ''}`);
  };

  await pinOffering(root, platformToken.id, offerings.chat);
  const chat = await retryRoutable(() => call(`${BASE}/v1/chat/completions`, {
    method: 'POST', auth: apiAuth,
    body: { model: models.chat, messages: [{ role: 'user', content: 'stage D metering probe' }], max_tokens: 16 },
  }), 'chat');
  check('chat request completed', chat.status === 200 && Array.isArray(chat.json?.choices), `HTTP ${chat.status}`);

  await pinOffering(root, platformToken.id, offerings.image);
  const image = await retryRoutable(() => call(`${BASE}/v1/images/generations`, {
    method: 'POST', auth: apiAuth, body: { model: models.image, prompt: 'a blue square', n: 2, size: '1024x1024' },
  }), 'image');
  check('image request completed', image.status === 200 && (image.json?.data?.length ?? 0) === 1,
    `HTTP ${image.status}, actual images=${image.json?.data?.length ?? 0}`);

  await pinOffering(root, platformToken.id, offerings.embedding);
  const embedding = await retryRoutable(() => call(`${BASE}/v1/embeddings`, {
    method: 'POST', auth: apiAuth, body: { model: models.embedding, input: ['alpha', 'beta'] },
  }), 'embedding');
  check('embedding request completed', embedding.status === 200 && (embedding.json?.data?.length ?? 0) === 2,
    `HTTP ${embedding.status}`);

  await pinOffering(root, platformToken.id, offerings.rerank);
  const rerank = await retryRoutable(() => call(`${BASE}/v1/rerank`, {
    method: 'POST', auth: apiAuth,
    body: { model: models.rerank, query: 'alpha', documents: ['alpha one', 'beta two', 'gamma three'] },
  }), 'rerank');
  check('rerank request completed', rerank.status === 200 && (rerank.json?.results?.length ?? 0) === 3,
    `HTTP ${rerank.status}`);

  await pinOffering(root, platformToken.id, offerings.stt);
  const form = new FormData();
  form.append('model', models.stt);
  form.append('file', wavBlob(1), 'stage-d.wav');
  const stt = await retryRoutable(() => call(`${BASE}/v1/audio/transcriptions`, {
    method: 'POST', auth: apiAuth, form,
  }), 'speech to text');
  check('speech recognition request completed', stt.status === 200 && stt.json?.text === 'stub transcript',
    `HTTP ${stt.status}`);

  await pinOffering(root, platformToken.id, offerings.tts);
  const tts = await retryRoutable(() => call(`${BASE}/v1/audio/speech`, {
    method: 'POST', auth: apiAuth, binary: true,
    body: { model: models.tts, input: 'stage D speech', voice: 'alloy', response_format: 'pcm' },
  }), 'text to speech');
  check('speech generation request completed', tts.status === 200 && (tts.bytes?.length ?? 0) > 0,
    `HTTP ${tts.status}, bytes=${tts.bytes?.length ?? 0}`);

  await pinOffering(root, platformToken.id, offerings.video);
  await call(`${STUB}/_control/task`, { method: 'POST', body: { states: ['IN_PROGRESS', 'SUCCESS'], resultUrl: '' } });
  const video = await retryRoutable(() => call(`${BASE}/v1/videos`, {
    method: 'POST', auth: apiAuth, body: { model: models.video, prompt: 'stage D video' },
  }), 'video submit');
  const taskId = video.json?.id ?? video.json?.task_id ?? video.json?.data?.task_id;
  check('video task submitted', video.status === 200 && Boolean(taskId), `HTTP ${video.status}`);
  let videoStatus = '';
  if (taskId) {
    for (let attempt = 0; attempt < 24; attempt++) {
      const polled = await call(`${BASE}/v1/videos/${encodeURIComponent(taskId)}`, { auth: apiAuth });
      videoStatus = String(polled.json?.status ?? polled.json?.data?.task_status ?? '');
      if (/succeed|success|completed|fail/i.test(videoStatus)) break;
      await sleep(5000);
    }
  }
  check('video task reached success', /succeed|success|completed/i.test(videoStatus), `status=${videoStatus || '(empty)'}`);

  const end = Math.floor(Date.now() / 1000) + 60;
  let items = [];
  for (let attempt = 0; attempt < 25; attempt++) {
    const summary = expectData(await call(`${BASE}/api/apigx/usage/summary?group_by=model&start=${startedAt}&end=${end}&page=1&page_size=100`, {
      auth: root,
    }), 'read owner usage summary');
    items = summary.items ?? [];
    const byModel = new Map(items.map((item) => [item.group_key, item]));
    const complete =
      hasUsage(byModel.get(models.chat), 1, 0, 1000) &&
      hasUsage(byModel.get(models.image), 3, 5, 1000) &&
      hasUsage(byModel.get(models.embedding), 2, 0, 1000) &&
      (byModel.get(models.rerank)?.usage ?? []).some((usage) => Number(usage.capability) === 7) &&
      hasUsage(byModel.get(models.stt), 5, 6, 1000) &&
      hasUsage(byModel.get(models.tts), 6, 7, 1) &&
      hasUsage(byModel.get(models.video), 8, 8, 5000);
    if (complete) break;
    await sleep(2000);
  }

  const byModel = new Map(items.map((item) => [item.group_key, item]));
  check('chat metering records input tokens', hasUsage(byModel.get(models.chat), 1, 0, 1000));
  check('image metering records actual response count', hasUsage(byModel.get(models.image), 3, 5, 1000),
    'request asked for 2; stub returned 1');
  check('embedding metering records input tokens', hasUsage(byModel.get(models.embedding), 2, 0, 1000));
  check('rerank metering is classified separately', (byModel.get(models.rerank)?.usage ?? []).some((usage) => Number(usage.capability) === 7));
  check('speech recognition records measured input seconds', hasUsage(byModel.get(models.stt), 5, 6, 1000));
  check('speech generation records measured output seconds', hasUsage(byModel.get(models.tts), 6, 7, 1));
  check('video metering records final output duration', hasUsage(byModel.get(models.video), 8, 8, 5000));

  const pricedOffering = selections[0]?.offering_id;
  const pricedModel = Object.entries(offerings).find(([, id]) => Number(id) === Number(pricedOffering))?.[0];
  const pricedSummary = pricedModel ? byModel.get(models[pricedModel]) : null;
  check('local matched price produces estimated-cost events', Number(pricedSummary?.estimated_cost_event_count ?? 0) > 0,
    `events=${pricedSummary?.estimated_cost_event_count ?? 0}`);
  check('unmatched dimensions remain visibly unpriced', items.some((item) => Number(item.unpriced_event_count ?? 0) > 0));

  await call(`${BASE}/api/apigx/platform-keys/${platformToken.id}/preferences`, {
    method: 'PUT', auth: root,
    body: { scope_mode: 0, allowed_model_ids: [], allowed_owner_user_ids: [], allow_fallback: true, default_route_mode: 'auto' },
  });

  const priceChecks = { localSourcesOK, expressionOK, priceConfirmOK };
  const failed = checks.filter((item) => !item.ok);
  fs.mkdirSync(STATE_DIR, { recursive: true });
  fs.writeFileSync(OUT_FILE, JSON.stringify({
    stamp,
    models,
    ids: {
      groupId: group.id,
      sharedChannelId: channel.id,
      videoChannelId: videoChannel.id,
      offerings,
      platformTokenId: platformToken.id,
    },
    priceChecks,
    checks,
  }, null, 2));
  console.log(`\n  ${checks.length + 3} checks, ${failed.length + Object.values(priceChecks).filter((ok) => !ok).length} failed`);
  if (failed.length || Object.values(priceChecks).some((ok) => !ok)) process.exitCode = 1;
}

main().catch((error) => {
  console.error(`\x1b[31mFAILED\x1b[0m  ${error.stack ?? error.message}`);
  process.exitCode = 2;
});
