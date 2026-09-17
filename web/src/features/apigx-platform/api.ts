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
*/import { api } from '@/lib/api'

import type { ApplyRouteRankingPayload, ManualPricePayload, PriceRuleResponse } from './types'

// 平台侧数据访问：价格规则与路由排名应用。均为**平台侧动作**（后端 requirePlatformReviewer）。
export async function getPriceRules(scopeType: number, scopeId: number): Promise<PriceRuleResponse> {
  const res = await api.get('/api/apigx/price-rules', { params: { scope_type: scopeType, scope_id: scopeId } })
  return res.data
}

export async function setManualPrice(payload: ManualPricePayload): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.put('/api/apigx/price-rules', payload)
  return res.data
}

export async function applyRouteRanking(payload: ApplyRouteRankingPayload): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post('/api/apigx/route-apply', payload)
  return res.data
}