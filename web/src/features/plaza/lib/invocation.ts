/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/**
 * 生成「调用配置」片段（计划书 §4 模型详情要求）。
 *
 * 纯函数：地址与模型名进来，片段出去 —— 便于测试，也避免把这段字符串拼装散在组件里。
 * 地址按项目的既有约定读取（`status.server_address`，缺省用当前站点），
 * 这里只负责拼装，**不做任何网络请求**。
 */
export interface InvocationSnippets {
  baseURL: string
  model: string
  curl: string
  python: string
}

export function buildInvocationSnippets(baseURL: string, model: string): InvocationSnippets {
  const root = baseURL.replace(/\/+$/, '')
  const openaiBase = `${root}/v1`
  const curl = [
    `curl ${openaiBase}/chat/completions \\`,
    `  -H "Authorization: Bearer $APIGX_KEY" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"model":"${model}","messages":[{"role":"user","content":"hello"}]}'`,
  ].join('\n')
  const python = [
    'from openai import OpenAI',
    '',
    `client = OpenAI(base_url="${openaiBase}", api_key="<your APIGX key>")`,
    `resp = client.chat.completions.create(model="${model}", messages=[{"role": "user", "content": "hello"}])`,
    'print(resp.choices[0].message.content)',
  ].join('\n')
  return { baseURL: openaiBase, model, curl, python }
}

/** 读取服务地址：与项目既有做法一致（后端 status 优先，缺省用当前站点）。 */
export function resolveServerAddress(): string {
  try {
    const raw = localStorage.getItem('status')
    if (raw) {
      const status = JSON.parse(raw) as { server_address?: string }
      if (status.server_address) {
        return status.server_address
      }
    }
  } catch {
    /* 读不到就用当前站点 */
  }
  return window.location.origin
}