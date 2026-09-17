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
*/import { describe, expect, it } from 'vitest'

import { buildScopePayload, parseIdList, parseNumberIdList } from '../lib/apigx-scope'

describe('ID 列表解析', () => {
  it('逗号与空格都能分隔', () => {
    expect(parseIdList('glm-5.3-flash, qwen3.8-max  deepseek-v3')).toEqual([
      'glm-5.3-flash',
      'qwen3.8-max',
      'deepseek-v3',
    ])
  })

  it('空串得到空列表（= 不限制），且忽略多余分隔符', () => {
    expect(parseIdList('  ,, ')).toEqual([])
  })

  it('数字列表丢弃非数字与零/负数（不猜、不补 0）', () => {
    expect(parseNumberIdList('1, abc, 0, -2, 7')).toEqual([1, 7])
  })
})

describe('偏好请求体构造', () => {
  const base = {
    scopeMode: '0',
    pinnedOfferingId: '',
    allowedModelIds: '',
    allowedOwnerUserIds: '',
    allowFallback: true,
    defaultRouteMode: 'auto',
  }

  it('整体提交全部字段（表单已预填当前值，因此是幂等往返）', () => {
    const payload = buildScopePayload(base)

    expect(payload.scope_mode).toBe(0)
    expect(payload.pinned_offering_id).toBe(0)
    expect(payload.allowed_model_ids).toEqual([])
    expect(payload.allowed_owner_user_ids).toEqual([])
    expect(payload.allow_fallback).toBe(true)
    expect(payload.default_route_mode).toBe('auto')
  })

  it('绑定来源为非法输入时提交 0（= 未绑定），而不是猜一个 ID', () => {
    expect(buildScopePayload({ ...base, pinnedOfferingId: 'abc' }).pinned_offering_id).toBe(0)
  })

  it('填写的模型与分享者被解析进请求体', () => {
    const payload = buildScopePayload({ ...base, allowedModelIds: 'a,b', allowedOwnerUserIds: '3 4' })

    expect(payload.allowed_model_ids).toEqual(['a', 'b'])
    expect(payload.allowed_owner_user_ids).toEqual([3, 4])
  })
})