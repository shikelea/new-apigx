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
*/ import { describe, expect, it } from 'vitest'

import {
  buildScopePayload,
  parseIdList,
  parseNumberIdList,
  resourceScopeLabelKey,
  ROUTE_MODE_OPTIONS,
} from '../lib/apigx-scope'
import type { PlatformKeyPreferences } from '../types'

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
    resourceScope: 'all' as const,
    pinnedOfferingPublicId: '',
    allowedModelIds: [] as string[],
    allowFallback: true,
    defaultRouteMode: 'auto',
  }

  it('整体提交全部字段（表单已预填当前值，因此是幂等往返）', () => {
    const payload = buildScopePayload(base)

    expect(payload.scope_mode).toBe(3)
    expect(payload.pinned_offering_public_id).toBe('')
    expect(payload.allowed_model_ids).toEqual([])
    expect(payload.allowed_owner_user_ids).toEqual([])
    expect(payload.allowed_access_kinds).toEqual([
      'public',
      'private',
      'shared',
    ])
    expect(payload.allow_fallback).toBe(true)
    expect(payload.default_route_mode).toBe('auto')
  })

  it('固定来源只提交对外 offering ID，不暴露内部数字 ID', () => {
    const payload = buildScopePayload({
      ...base,
      resourceScope: 'pinned',
      pinnedOfferingPublicId: 'off_public_123',
    })
    expect(payload.scope_mode).toBe(2)
    expect(payload.pinned_offering_public_id).toBe('off_public_123')
    expect(payload.allowed_access_kinds).toEqual([])
  })

  it('单一资源范围直接映射为后端 access kind', () => {
    const payload = buildScopePayload({ ...base, resourceScope: 'shared' })
    expect(payload.allowed_access_kinds).toEqual(['shared'])
  })
})

describe('资源范围展示', () => {
  const preferences = {
    token_id: 7,
    configured: true,
    scope_mode: 3,
    pinned_offering_id: 0,
    allowed_model_ids: [],
    allowed_owner_user_ids: [],
    allowed_access_kinds: ['shared'],
    allow_fallback: true,
    default_route_mode: 'auto',
  } satisfies PlatformKeyPreferences

  it('把单一共享范围显示为分享给我', () => {
    expect(resourceScopeLabelKey(preferences)).toBe(
      'apigxKey.resourceScope.shared'
    )
  })

  it('路由策略使用实际存在的翻译键命名空间', () => {
    expect(ROUTE_MODE_OPTIONS.map((option) => option.labelKey)).toEqual([
      'apigxKey.scope.routeMode.auto',
      'apigxKey.scope.routeMode.latency',
      'apigxKey.scope.routeMode.throughput',
      'apigxKey.scope.routeMode.reliability',
    ])
  })
})
