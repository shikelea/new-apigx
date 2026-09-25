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
*/import { describe, expect, it, vi, beforeEach } from 'vitest'

import { api } from '@/lib/api'

import { getPlaza } from '../api'

// 只替换**外部边界**（HTTP 客户端），被测模块本身照常执行。
vi.mock('@/lib/api', () => ({
  api: { get: vi.fn() },
}))

const mockedGet = vi.mocked(api.get)

describe('模型广场数据层', () => {
  beforeEach(() => {
    mockedGet.mockReset()
  })

  it('请求 /api/apigx/plaza 并原样返回后端数据', async () => {
    const payload = {
      success: true,
      data: {
        items: [
          {
            offering_public_id: 'off_1',
            model: 'glm-5.3-flash',
            channel_public_id: 'ch_1',
            owner_user_id: 7,
            capabilities: ['chat'],
            updated_at: 1789640000,
          },
        ],
        total: 1,
        page: 1,
        page_size: 20,
      },
    }
    mockedGet.mockResolvedValue({ data: payload })

    const result = await getPlaza({ page: 1, page_size: 20 })

    expect(mockedGet).toHaveBeenCalledWith('/api/apigx/plaza', { params: { page: 1, page_size: 20 } })
    expect(result).toBe(payload)
  })

  it('不吞掉请求失败（交给上层按项目约定处理）', async () => {
    mockedGet.mockRejectedValue(new Error('network down'))

    await expect(getPlaza()).rejects.toThrow('network down')
  })
})