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
import { z } from 'zod'

// ============================================================================
// API Key Schema & Types
// ============================================================================

export const apiKeySchema = z.object({
  id: z.number(),
  name: z.string(),
  key: z.string(),
  status: z.number(), // 1: enabled, 2: disabled, 3: expired, 4: exhausted
  remain_quota: z.number(),
  used_quota: z.number(),
  unlimited_quota: z.boolean(),
  expired_time: z.number(), // -1 for never expires
  created_time: z.number(),
  accessed_time: z.number(),
  group: z.string().nullish().default(''),
  auto_groups: z.array(z.string()).nullish().default(null),
  cross_group_retry: z
    .preprocess((v) => {
      if (v === 1) return true
      if (v === 0) return false
      return v
    }, z.boolean())
    .optional()
    .default(false),
  model_limits_enabled: z.boolean(),
  model_limits: z.string().nullish().default(''),
  allow_ips: z.string().nullish().default(''),
})

export type ApiKey = z.infer<typeof apiKeySchema>

// ============================================================================
// API Request/Response Types
// ============================================================================

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export interface GetApiKeysParams {
  p?: number
  size?: number
}

export interface GetApiKeysResponse {
  success: boolean
  message?: string
  data?: {
    items: ApiKey[]
    total: number
    page: number
    page_size: number
  }
}

export interface SearchApiKeysParams {
  keyword?: string
  token?: string
  p?: number
  size?: number
}

export interface ApiKeyFormData {
  name: string
  remain_quota: number
  expired_time: number
  unlimited_quota: boolean
  model_limits_enabled: boolean
  model_limits: string
  allow_ips: string
  group: string
  auto_groups: string[]
  cross_group_retry: boolean
}

export interface TokenAutoGroupsConfig {
  groups: string[]
  max_count: number
}

// ============================================================================
// Dialog Types
// ============================================================================

export type ApiKeysDialogType =
  | 'create'
  | 'update'
  | 'delete'
  | 'batch-delete'
  | 'cc-switch'
  | 'apigx-scope'

// ============================================================================
// APIGX：平台 Key 的来源限制与路由策略（计划书 §9.1：偏好保存到平台 Key）
// ============================================================================

/** 平台 Key 偏好（字段与后端 `getPlatformKeyPreferences` 一致）。 */
export interface PlatformKeyPreferences {
  token_id: number
  /** 是否已配置过（false 时其余字段是后端默认值） */
  configured: boolean
  scope_mode: number
  pinned_offering_id: number
  pinned_offering_public_id?: string
  allowed_model_ids: string[]
  allowed_owner_user_ids: number[]
  allow_fallback: boolean
  /** 空串与 `auto` 都表示综合自动质量路由 */
  default_route_mode: string
}

export interface PlatformKeyPreferencesResponse {
  success: boolean
  data?: PlatformKeyPreferences
}

/** 偏好更新请求。后端按**非空指针字段**做局部更新。 */
export interface PlatformKeyPreferencesPayload {
  scope_mode?: number
  pinned_offering_id?: number
  pinned_offering_public_id?: string
  allowed_model_ids?: string[]
  allowed_owner_user_ids?: number[]
  allow_fallback?: boolean
  default_route_mode?: string
}
