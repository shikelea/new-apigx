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