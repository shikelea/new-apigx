#!/usr/bin/env node
// APIGX 可控上游替身（T0.9 的核心工具）
//
// 作用：模拟一个 OpenAI 兼容的上游，用来在**不调用任何真实上游、不花任何钱**的前提下，
// 验证计划书 §11 阶段 0 的 5 项验收：
//   ① 归属隔离 ② 定向渠道/Key 不被二次选择 ③ 没有充值余额但成本估算为正
//   ④ 预算与并发准入生效 ⑤ 任务/文件对象跨用户不可访问
//
// 关键设计：它会把每一次到达的请求**完整记录下来**（含认证头、平台扩展头、请求体），
// 因此可以事后取证：
//   - 到达的是哪把上游 Key（证明调度选定的 Key 就是实际使用的 Key，没有被二次抽选）
//   - 是否收到了 X-APIGX-* 平台扩展头（证明出站前剥离）
//   - 上游看到了什么模型名（证明标准模型 → 上游模型 ID 的转换）
//
// 用法：
//   node tools/stub-upstream/server.mjs [--port 8100] [--host 127.0.0.1]
//
// 控制面（仅本机开发用，绝不用于任何真实环境）：
//   GET  /_control/state                查看当前故障注入配置与统计
//   POST /_control/profile              设置注入：{delayMs,ttftMs,status,hang,usage,chunkDelayMs}
//   GET  /_control/requests?limit=50    查看已记录的请求
//   POST /_control/reset                清空记录与配置
//
// 例：
//   curl -X POST localhost:8100/_control/profile -d '{"ttftMs":800,"delayMs":1200}'
//   curl localhost:8100/_control/requests?limit=5

import http from 'node:http';
import crypto from 'node:crypto';
import { URL } from 'node:url';

const argv = process.argv.slice(2);
const getArg = (name, fallback) => {
  const i = argv.indexOf(name);
  return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback;
};
const PORT = Number(getArg('--port', process.env.STUB_PORT || 8100));
const HOST = getArg('--host', process.env.STUB_HOST || '127.0.0.1');
const STUB_BASE = 'http://127.0.0.1:' + (process.env.PORT || 8100);
const MAX_RECORDS = 1000;

const state = {
  // 故障与延迟注入
  profile: {
    delayMs: 0,        // 响应前总延迟
    ttftMs: 0,         // 流式首字节延迟
    chunkDelayMs: 0,   // 流式每个分片间隔
    status: 0,         // 非 0 时直接返回该 HTTP 状态码（用于注入 5xx / 401 / 429）
    statusBody: null,  // 自定义错误体
    hang: false,       // 挂起不返回（用于超时与租约测试）
    usage: { prompt_tokens: 12, completion_tokens: 34, total_tokens: 46 },
    modelEcho: true,   // 回显请求里的模型名（便于确认上游收到的模型 ID）
  },
  // 取证记录
  requests: [],
  tasks: {},
  taskSeq: 0,
  taskPlan: ['IN_PROGRESS', 'SUCCESS'],
  taskFailReason: '',
  taskResultUrl: '',
  keyCounts: {},       // 上游 Key 指纹 -> 次数
  startedAt: new Date().toISOString(),
};

function fingerprintKey(raw) {
  if (!raw) return 'none';
  const m = /^Bearer\s+(.+)$/i.exec(raw.trim());
  const key = m ? m[1] : raw.trim();
  const h = crypto.createHash('sha256').update(key).digest('hex').slice(0, 10);
  const tail = key.length > 4 ? key.slice(-4) : key;
  return `sha256:${h}/…${tail}`;
}

function record(req, url, bodyText) {
  const platformHeaders = {};
  for (const [k, v] of Object.entries(req.headers)) {
    if (k.toLowerCase().startsWith('x-apigx-')) platformHeaders[k] = v;
  }
  const auth = req.headers['authorization'] || req.headers['x-api-key'] || '';
  const entry = {
    at: new Date().toISOString(),
    method: req.method,
    path: url.pathname,
    query: url.search,
    upstreamKey: fingerprintKey(auth),
    platformHeaders,                       // 应当为空：平台扩展头必须在出站前剥离
    contentType: req.headers['content-type'] || '',
    userAgent: req.headers['user-agent'] || '',
    bodyBytes: bodyText ? Buffer.byteLength(bodyText) : 0,
    body: bodyText && bodyText.length <= 4096 ? bodyText : (bodyText ? bodyText.slice(0, 4096) + '…(truncated)' : ''),
  };
  state.requests.push(entry);
  if (state.requests.length > MAX_RECORDS) state.requests.shift();
  state.keyCounts[entry.upstreamKey] = (state.keyCounts[entry.upstreamKey] || 0) + 1;
  return entry;
}

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', () => resolve(''));
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function sendJson(res, status, obj) {
  const buf = Buffer.from(JSON.stringify(obj), 'utf8');
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'content-length': buf.length });
  res.end(buf);
}

// ---------- 控制面 ----------
// 注意：请求体已在主处理器里读过一次（Node 的流只能消费一次），
// 因此这里必须接收已经读好的 bodyText，不能再次 readBody，否则请求会永远挂住。
async function handleControl(req, res, url, bodyText) {
  // ---- 视频任务模拟（照 Kling 的形状：底座 kling 插件按此解析）----
  // 控制面：/_control/task 设置后续状态序列，例如 {"states":["IN_PROGRESS","SUCCESS"]}
  if (url.pathname === '/_control/task' && req.method === 'POST') {
    let patch = {};
    try { patch = JSON.parse(bodyText || '{}'); } catch { sendJson(res, 400, { ok: false, error: 'invalid json' }); return true; }
    if (Array.isArray(patch.states)) state.taskPlan = patch.states.slice();
    if (typeof patch.failReason === 'string') state.taskFailReason = patch.failReason;
    if (typeof patch.resultUrl === 'string') state.taskResultUrl = patch.resultUrl;
    sendJson(res, 200, { ok: true, taskPlan: state.taskPlan });
    return true;
  }

  if (url.pathname === '/_control/profile' && req.method === 'POST') {
    let patch = {};
    try { patch = JSON.parse(bodyText || '{}'); } catch { sendJson(res, 400, { ok: false, error: 'invalid json' }); return true; }
    state.profile = { ...state.profile, ...patch };
    if (patch.usage) state.profile.usage = patch.usage;
    sendJson(res, 200, { ok: true, profile: state.profile });
    return true;
  }
  if (url.pathname === '/_control/requests' && req.method === 'GET') {
    const limit = Math.max(1, Math.min(MAX_RECORDS, Number(url.searchParams.get('limit') || 50)));
    sendJson(res, 200, {
      ok: true,
      total: state.requests.length,
      keyCounts: state.keyCounts,
      requests: state.requests.slice(-limit),
    });
    return true;
  }
  if (url.pathname === '/_control/reset' && req.method === 'POST') {
    state.requests = [];
    state.keyCounts = {};
    sendJson(res, 200, { ok: true });
    return true;
  }
  return false;
}

// ---------- 故障注入闸门 ----------
async function applyFaults(res) {
  const p = state.profile;
  if (p.hang) {
    // 故意不返回：用于超时、租约续期、进程中断恢复的验证
    await sleep(3600_000);
    return true;
  }
  if (p.status && p.status >= 400) {
    sendJson(res, p.status, p.statusBody || {
      error: { message: `stub injected status ${p.status}`, type: 'stub_error', code: `stub_${p.status}` },
    });
    return true;
  }
  if (p.delayMs > 0) await sleep(p.delayMs);
  return false;
}

// ---------- OpenAI 兼容端点 ----------
function modelList() {
  return {
    object: 'list',
    data: [
      { id: 'stub/model-x-202609', object: 'model', created: 1758000000, owned_by: 'apigx-stub' },
      { id: 'stub/model-y-202609', object: 'model', created: 1758000000, owned_by: 'apigx-stub' },
      { id: 'stub/embed-small', object: 'model', created: 1758000000, owned_by: 'apigx-stub' },
    ],
  };
}

function completionBody(model, prompt) {
  const p = state.profile;
  return {
    id: 'chatcmpl-stub-' + crypto.randomUUID().slice(0, 12),
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model: p.modelEcho ? model : 'stub/model-x-202609',
    choices: [{
      index: 0,
      message: { role: 'assistant', content: `stub reply to: ${String(prompt).slice(0, 120)}` },
      finish_reason: 'stop',
    }],
    usage: p.usage,
  };
}

async function streamCompletion(res, model, prompt) {
  const p = state.profile;
  res.writeHead(200, {
    'content-type': 'text/event-stream; charset=utf-8',
    'cache-control': 'no-cache',
    connection: 'keep-alive',
  });
  const id = 'chatcmpl-stub-' + crypto.randomUUID().slice(0, 12);
  const created = Math.floor(Date.now() / 1000);
  const sendChunk = (delta, finish = null) => {
    const payload = {
      id, object: 'chat.completion.chunk', created,
      model: p.modelEcho ? model : 'stub/model-x-202609',
      choices: [{ index: 0, delta, finish_reason: finish }],
    };
    res.write(`data: ${JSON.stringify(payload)}\n\n`);
  };

  if (p.ttftMs > 0) await sleep(p.ttftMs);
  sendChunk({ role: 'assistant', content: '' });
  const words = `stub reply to: ${String(prompt).slice(0, 120)}`.split(' ');
  for (const w of words) {
    if (p.chunkDelayMs > 0) await sleep(p.chunkDelayMs);
    sendChunk({ content: w + ' ' });
  }
  sendChunk({}, 'stop');
  // 末尾附带 usage（OpenAI 的 stream_options.include_usage 语义）
  res.write(`data: ${JSON.stringify({ id, object: 'chat.completion.chunk', created, model, choices: [], usage: p.usage })}\n\n`);
  res.write('data: [DONE]\n\n');
  res.end();
}

// ---------- 主处理 ----------
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || '127.0.0.1'}`);
  const bodyText = await readBody(req);

  // 控制面不记录，避免污染取证数据
  if (url.pathname.startsWith('/_control/')) {
    const handled = await handleControl(req, res, url, bodyText);
    if (!handled) sendJson(res, 404, { ok: false, error: 'unknown control endpoint' });
    return;
  }

  const entry = record(req, url, bodyText);
  const fail = await applyFaults(res);
  if (fail) return;

  let body = {};
  try { body = bodyText ? JSON.parse(bodyText) : {}; } catch { /* 非 JSON（如 multipart）保持空对象 */ }

  if (url.pathname === '/v1/models' || url.pathname === '/api/models') {
    sendJson(res, 200, modelList());
    return;
  }

  if (url.pathname === '/v1/chat/completions' || url.pathname === '/v1/completions') {
    const model = body.model || 'stub/model-x-202609';
    const prompt = Array.isArray(body.messages)
      ? body.messages.map((m) => m.content).join(' ')
      : (body.prompt || '');
    if (body.stream === true) { await streamCompletion(res, model, prompt); return; }
    sendJson(res, 200, completionBody(model, prompt));
    return;
  }

  if (url.pathname === '/v1/embeddings') {
    const input = Array.isArray(body.input) ? body.input : [body.input ?? ''];
    sendJson(res, 200, {
      object: 'list',
      model: body.model || 'stub/embed-small',
      data: input.map((_, i) => ({ object: 'embedding', index: i, embedding: Array.from({ length: 8 }, (_, k) => Math.sin(i + k) / 8) })),
      usage: { prompt_tokens: input.length * 5, total_tokens: input.length * 5 },
    });
    return;
  }

  if (url.pathname === '/v1/images/generations') {
    sendJson(res, 200, {
      created: Math.floor(Date.now() / 1000),
      data: [{ url: `http://${HOST}:${PORT}/_stub/artifact/stub-image-${crypto.randomUUID().slice(0, 8)}.png`, revised_prompt: body.prompt || '' }],
    });
    return;
  }

  if (url.pathname === '/v1/audio/transcriptions') {
    sendJson(res, 200, { text: 'stub transcript', duration: 3.5 });
    return;
  }

  if (url.pathname === '/v1/rerank') {
    const docs = body.documents || [];
    sendJson(res, 200, {
      id: 'rerank-stub-' + crypto.randomUUID().slice(0, 8),
      model: body.model || 'stub/rerank',
      results: docs.map((_, i) => ({ index: i, relevance_score: 1 - i * 0.1 })),
      usage: { total_tokens: docs.length * 7 },
    });
    return;
  }

  // 异步任务（视频等）最小形态，阶段 3 再补细节
  // 同时接受带与不带 `/kling` 前缀（kling 插件在"中继型"密钥下会加 `/kling` 前缀，
  // 实测它就是这样出站的）。两种都收，避免因密钥形态不同而"看起来没接上"。
  // 视频结果媒体：签名直链指向这里，替身要真的能取到字节，
  // 否则平台的"结果访问"会被上游 404 带成 502（第一版就踩了）。
  if (/^\/media\/[^/]+$/.test(url.pathname) && req.method === 'GET') {
    const payload = Buffer.from('stub-media-bytes-for-' + url.pathname);
    res.writeHead(200, { 'content-type': 'video/mp4', 'content-length': payload.length });
    res.end(payload);
    return true;
  }
  const videoSubmit = (url.pathname === '/v1/videos/text2video' || url.pathname === '/kling/v1/videos/text2video') && req.method === 'POST';
  const videoQuery = /^(?:\/kling)?\/v1\/videos\/(text2video|image2video)\/([^/]+)$/.exec(url.pathname);
  if (videoSubmit || videoQuery) {
    // 不调用 recordRequest：它的签名与入参由主入口持有，这里只需模拟任务生命周期即可
    if (state.profile.status) { res.writeHead(state.profile.status, { 'content-type': 'application/json' }); res.end(JSON.stringify({ code: 1, message: 'injected status' })); return true; }
    if (videoSubmit) {
      const taskId = 'task_stub_' + (state.taskSeq = (state.taskSeq || 0) + 1);
      state.tasks[taskId] = { id: taskId, step: 0, createdAt: Date.now() };
      state.lastTaskId = taskId;
      sendJson(res, 200, { code: 0, message: 'SUCCEED', data: { task_id: taskId, task_status: 'submitted', created_at: Date.now() } });
      return true;
    }
    const taskId = decodeURIComponent(videoQuery[2]);
    const task = state.tasks[taskId];
    if (!task) { sendJson(res, 404, { code: 1, message: 'task not found' }); return true; }
    const plan = state.taskPlan && state.taskPlan.length ? state.taskPlan : ['IN_PROGRESS', 'SUCCESS'];
    const status = plan[Math.min(task.step, plan.length - 1)];
    task.step += 1;
    if (status === 'SUCCESS') {
      sendJson(res, 200, {
        code: 0, message: 'SUCCEED',
        data: {
          task_id: taskId, task_status: 'SUCCESS', task_status_msg: '',
          task_result: { videos: [{ id: taskId + '_v', url: state.taskResultUrl || (STUB_BASE + '/media/' + taskId + '.mp4'), duration: '5' }] },
        },
      });
      return true;
    }
    if (status === 'FAILURE') {
      sendJson(res, 200, { code: 0, message: 'SUCCEED', data: { task_id: taskId, task_status: 'FAILURE', task_status_msg: state.taskFailReason || 'stub injected failure' } });
      return;
    }
    sendJson(res, 200, { code: 0, message: 'SUCCEED', data: { task_id: taskId, task_status: status, task_status_msg: '' } });
    return;
  }
  if (url.pathname === '/_control/state' && req.method === 'GET') {
    sendJson(res, 200, {
      ok: true,
      startedAt: state.startedAt,
      profile: state.profile,
      requestCount: state.requests.length,
      keyCounts: state.keyCounts,
    });
    return true;
  }

  if (url.pathname === '/v1/video/generations') {
    sendJson(res, 200, { id: 'task-stub-' + crypto.randomUUID().slice(0, 8), task_id: 'task-stub', status: 'queued' });
    return;
  }

  sendJson(res, 404, {
    error: { message: `stub upstream has no handler for ${req.method} ${url.pathname}`, type: 'invalid_request_error', code: 'stub_no_route' },
    recorded: entry.at,
  });
});

server.listen(PORT, HOST, () => {
  console.log(`[stub-upstream] listening on http://${HOST}:${PORT}`);
  console.log('[stub-upstream] control plane: /_control/state | /_control/profile | /_control/requests | /_control/reset');
  console.log('[stub-upstream] 记录每次请求的认证头指纹与 X-APIGX-* 头，用于「Key 不被二次抽选」与「扩展头剥离」取证');
});
