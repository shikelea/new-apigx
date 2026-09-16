#!/usr/bin/env node
// APIGX 出网助手
//
// 背景：本机在 DSH 沙箱进程内，Windows Schannel 无法取得凭证
// （curl / PowerShell / .NET 的 HTTPS 全部报 `schannel: AcquireCredentialsHandle failed:
// SEC_E_NO_CREDENTIALS (0x8009030e)`），所以这些工具的 HTTPS 一律不可用。
// Node.js 自带 OpenSSL，不受该问题影响；本脚本用它统一走 HTTPS，并在需要时经本地代理
// （CONNECT 隧道）访问境外站点。
//
// 用法：
//   node tools/net/fetch.mjs <url>              # 只探测状态码
//   node tools/net/fetch.mjs <url> <outFile>    # 下载到文件
//   node tools/net/fetch.mjs <url> --direct     # 直连（不走代理）
//
// 代理取 APIGX_PROXY，默认 http://127.0.0.1:7897
import http from 'node:http';
import https from 'node:https';
import tls from 'node:tls';
import fs from 'node:fs';
import path from 'node:path';
import { URL } from 'node:url';

const rawArgs = process.argv.slice(2);
const direct = rawArgs.includes('--direct');
const args = rawArgs.filter((a) => a !== '--direct');
const startUrl = args[0];
const outFile = args[1];
if (!startUrl) {
  console.error('usage: node tools/net/fetch.mjs <url> [outFile] [--direct]');
  process.exit(2);
}

const PROXY = process.env.APIGX_PROXY || 'http://127.0.0.1:7897';
const UA = 'apigx-fetch/0.1 (+https://github.com/QuantumNous/new-api)';

// 让 https 模块经由本地 HTTP 代理建立 CONNECT 隧道，再由 Node 的 OpenSSL 完成 TLS。
// 注意：https.Agent 会直接使用 createConnection 返回的 socket，不会替我们做 TLS 握手，
// 因此这里必须在隧道建立后自己完成 tls.connect，否则会把明文请求写进隧道并收到 ECONNRESET。
class ProxyAgent extends https.Agent {
  createConnection(options, cb) {
    const proxy = new URL(PROXY);
    const req = http.request({
      host: proxy.hostname,
      port: proxy.port || 80,
      method: 'CONNECT',
      path: `${options.host}:${options.port}`,
      headers: { host: `${options.host}:${options.port}` },
    });
    req.once('connect', (res, socket) => {
      if (res.statusCode !== 200) {
        socket.destroy();
        cb(new Error(`proxy CONNECT failed: HTTP ${res.statusCode}`));
        return;
      }
      let settled = false;
      const done = (err, s) => {
        if (settled) return;
        settled = true;
        cb(err, s);
      };
      const tlsSocket = tls.connect(
        {
          socket,
          servername: options.servername || options.host,
          ALPNProtocols: options.ALPNProtocols,
          rejectUnauthorized: true,
        },
        () => done(null, tlsSocket),
      );
      tlsSocket.once('error', (err) => done(err));
    });
    req.once('error', (err) => cb(err));
    req.end();
  }
}

const agent = direct ? undefined : new ProxyAgent({ keepAlive: false });

function get(url, redirectsLeft = 5) {
  return new Promise((resolve, reject) => {
    const u = new URL(url);
    const req = https.request(
      {
        host: u.hostname,
        port: u.port || 443,
        path: u.pathname + u.search,
        method: 'GET',
        agent,
        headers: { 'user-agent': UA, accept: '*/*' },
      },
      (res) => {
        if ([301, 302, 303, 307, 308].includes(res.statusCode) && res.headers.location) {
          res.resume();
          const next = new URL(res.headers.location, url).toString();
          if (redirectsLeft <= 0) reject(new Error('too many redirects'));
          else get(next, redirectsLeft - 1).then(resolve, reject);
          return;
        }
        resolve({ res, finalUrl: url });
      },
    );
    req.setTimeout(30000, () => req.destroy(new Error('timeout')));
    req.on('error', reject);
    req.end();
  });
}

try {
  const { res, finalUrl } = await get(startUrl);
  const len = res.headers['content-length'];
  console.log(`HTTP ${res.statusCode} ${finalUrl}${len ? ` bytes=${len}` : ''}`);
  if (res.statusCode >= 400) {
    res.resume();
    process.exit(1);
  }
  if (outFile) {
    fs.mkdirSync(path.dirname(path.resolve(outFile)), { recursive: true });
    await new Promise((resolve, reject) => {
      const ws = fs.createWriteStream(outFile);
      res.pipe(ws);
      ws.on('finish', resolve);
      ws.on('error', reject);
    });
    console.log(`saved -> ${outFile} (${fs.statSync(outFile).size} bytes)`);
  } else {
    res.resume();
  }
} catch (err) {
  console.error(`FAIL ${startUrl}: ${err.code || ''} ${err.message}`);
  process.exit(1);
}
