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
// 平台侧（管理后台）相关类型。
//
// 价格作用域沿用阶段 2 已核实的三态：0=供给 / 1=上游 Key / 2=渠道。

export const PRICE_SCOPE_OFFERING = 0
export const PRICE_SCOPE_UPSTREAM_KEY = 1
export const PRICE_SCOPE_CHANNEL = 2

/** 一个计价维度（后端按维度返回，未配置时 `price_known=false`）。 */
export interface PriceDimension {
  dimension: string
  unit_scale: number
  usd_unit_price_micro: number
  price_known: boolean
}

export interface PriceRuleResponse {
  success: boolean
  data?: {
    price_known: boolean
    dimensions: PriceDimension[] | null
  }
}

export interface ReviewOffering {
  id: number
  public_id: string
  owner_user_id: number
  owner_display_name?: string
  share_group_id: number
  share_group_public_id: string
  share_group_name: string
  group_visibility: number
  channel_id: number
  channel_public_id: string
  channel_name: string
  upstream_model_id: string
  canonical_model_id?: number
  canonical_api_model_id?: string
  mapping_confirmed?: boolean
  capabilities?: Record<string, boolean>
  status: number
  revision: number
  price_configured: boolean
  created_at: number
  updated_at: number
}

export interface ReviewOfferingListResponse {
  success: boolean
  data?: {
    items: ReviewOffering[]
    total: number
    page: number
    page_size: number
  }
}

/** 人工覆盖价格（平台侧动作）。 */
export interface ManualPricePayload {
  scope_type: number
  scope_id: number
  input_usd_per_million: number
  output_usd_per_million: number
  lock?: boolean
}

/** 应用路由排名（平台侧动作；支持 dry-run）。 */
export interface ApplyRouteRankingPayload {
  model: string
  strategy?: string
  dry_run?: boolean
}
// 模型目录：别名候选（上游模型名 → 标准模型）。
export interface AliasCandidate {
  id: number
  external_model_id: string
  normalized_model_id: string
  scope_type: number
  scope_id: number
  source: number
  source_ref: string
  /** 0=待确认 / 1=已确认 / 2=已拒绝 */
  confirmation_state: number
  target_canonical_model_id: number
  target_api_model_id?: string
  offering_id: number
  offering_public_id: string
  offering_status: number
  owner_user_id: number
  share_group_name: string
  channel_name: string
}

export interface CanonicalModelOption {
  id: number
  api_model_id: string
  description: string
  modality_type: number
}

export interface CanonicalModelListResponse {
  success: boolean
  data?: { items: CanonicalModelOption[] }
}

export interface AliasCandidateListResponse {
  success: boolean
  data?: {
    items: AliasCandidate[] | null
    total: number
    page: number
    page_size: number
  }
}

/** 确认候选：二选一给目标（现成的标准模型 ID，或对外标准 ID —— 后者不存在时后端会顺手创建）。 */
export interface ConfirmAliasPayload {
  target_canonical_model_id?: number
  api_model_id?: string
  description?: string
  modality_type?: number
}
