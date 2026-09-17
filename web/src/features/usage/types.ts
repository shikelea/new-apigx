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
// 「我的用量」的对外数据结构（字段与后端 `GET /api/apigx/usage` 一致）。

/** 本次请求的来源：分享渠道或平台自有。 */
export interface UsageSource {
  kind: 'share' | 'platform'
  channel_public_id?: string
  owner_user_id?: number
}

/** 一条用量记录（一次请求）。 */
export interface UsageEntry {
  created_at: number
  model: string
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  /** 底座额度（参考价） */
  quota: number
  /** 换算后的成本（微美元） */
  cost_micro_usd: number
  elapsed_seconds: number
  is_stream: boolean
  request_id: string
  /** **路由结果**：这次实际用了哪条来源 */
  source?: UsageSource
}

export interface UsageListResponse {
  success: boolean
  data?: {
    items: UsageEntry[]
    total: number
    page: number
    page_size: number
  }
}

export interface GetUsageParams {
  page?: number
  page_size?: number
  model?: string
  start?: number
  end?: number
}