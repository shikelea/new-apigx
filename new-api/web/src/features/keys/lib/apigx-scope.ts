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
import type {
  PlatformKeyPreferences,
  PlatformKeyPreferencesPayload,
} from '../types'

export type ResourceScope = 'all' | 'public' | 'private' | 'shared' | 'pinned'

const ALL_ACCESS_KINDS = ['public', 'private', 'shared'] as const

/** 路由策略选项（空串与 auto 同义：综合质量路由，由后端统一解释）。 */
export const ROUTE_MODE_OPTIONS: { value: string; labelKey: string }[] = [
  { value: 'auto', labelKey: 'apigxKey.scope.routeMode.auto' },
  { value: 'latency', labelKey: 'apigxKey.scope.routeMode.latency' },
  { value: 'throughput', labelKey: 'apigxKey.scope.routeMode.throughput' },
  { value: 'reliability', labelKey: 'apigxKey.scope.routeMode.reliability' },
]

/** 把后端范围配置转成列表与移动卡片共用的用户可见文案。 */
export function resourceScopeLabelKey(
  preferences: PlatformKeyPreferences | undefined
): string {
  if (preferences?.scope_mode === 2) {
    return 'apigxKey.resourceScope.pinned'
  }
  if (
    preferences?.scope_mode === 3 &&
    preferences.allowed_access_kinds.length === 1
  ) {
    return `apigxKey.resourceScope.${preferences.allowed_access_kinds[0]}`
  }
  return 'apigxKey.resourceScope.all'
}

/** 解析逗号/空格分隔的 ID 列表；空串得到**空列表**（= 不限制）。 */
export function parseIdList(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
}

/** 解析数字 ID 列表；非数字项被丢弃（不猜、不补 0）。 */
export function parseNumberIdList(text: string): number[] {
  const parsed: number[] = []
  for (const part of parseIdList(text)) {
    const value = Number.parseInt(part, 10)
    if (Number.isFinite(value) && value > 0) {
      parsed.push(value)
    }
  }
  return parsed
}

/** 平台 Key 设置页使用的用户语义，不暴露 owner/offering 内部数字 ID。 */
export interface ScopeFormState {
  resourceScope: ResourceScope
  pinnedOfferingPublicId: string
  allowedModelIds: string[]
  allowFallback: boolean
  defaultRouteMode: string
}

/**
 * 表单 → 请求体。
 *
 * 提交**全部**字段：表单已用 `GET` 的当前值预填，因此整体提交是**幂等的往返**
 * （不会误清空用户没动过的字段 ✓）。后端只接受非空指针字段，所以这里给出全部字段是安全的。
 */
export function buildScopePayload(
  form: ScopeFormState
): PlatformKeyPreferencesPayload {
  const isPinned = form.resourceScope === 'pinned'
  let allowedAccessKinds: Array<'public' | 'private' | 'shared'> = []
  if (!isPinned && form.resourceScope === 'all') {
    allowedAccessKinds = [...ALL_ACCESS_KINDS]
  } else if (
    form.resourceScope === 'public' ||
    form.resourceScope === 'private' ||
    form.resourceScope === 'shared'
  ) {
    allowedAccessKinds = [form.resourceScope]
  }
  return {
    scope_mode: isPinned ? 2 : 3,
    pinned_offering_public_id: isPinned
      ? form.pinnedOfferingPublicId.trim()
      : '',
    allowed_model_ids: form.allowedModelIds,
    allowed_owner_user_ids: [],
    allowed_access_kinds: allowedAccessKinds,
    allow_fallback: form.allowFallback,
    default_route_mode: form.defaultRouteMode,
  }
}
