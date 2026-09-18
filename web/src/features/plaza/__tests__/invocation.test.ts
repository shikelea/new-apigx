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

import { describe, expect, it } from 'vitest'

import { buildInvocationSnippets } from '../lib/invocation'

describe('调用配置片段', () => {
  it('地址末尾的斜杠不会产生双斜杠的 /v1', () => {
    const s = buildInvocationSnippets('https://api.example.com/', 'glm-5.3-flash')
    expect(s.baseURL).toBe('https://api.example.com/v1')
  })

  it('模型名进入两个片段（curl 与 Python）', () => {
    const s = buildInvocationSnippets('https://api.example.com', 'qwen3.8-max')
    expect(s.curl).toContain('"model":"qwen3.8-max"')
    expect(s.python).toContain('model="qwen3.8-max"')
  })

  it('片段使用 /v1 前缀（OpenAI 兼容路径）', () => {
    const s = buildInvocationSnippets('https://api.example.com', 'm')
    expect(s.curl).toContain('https://api.example.com/v1/chat/completions')
  })
})
