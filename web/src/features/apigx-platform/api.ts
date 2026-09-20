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
import { api } from '@/lib/api'

import type {
  AliasCandidateListResponse,
  ApplyRouteRankingPayload,
  ConfirmAliasPayload,
  ManualPricePayload,
  PriceRuleResponse,
  ReviewOfferingListResponse,
} from './types'

// 平台侧数据访问：价格规则与路由排名应用。均为**平台侧动作**（后端 requirePlatformReviewer）。
export async function getPriceRules(
  scopeType: number,
  scopeId: number
): Promise<PriceRuleResponse> {
  const res = await api.get('/api/apigx/price-rules', {
    params: { scope_type: scopeType, scope_id: scopeId },
  })
  return res.data
}

export async function setManualPrice(
  payload: ManualPricePayload
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.put('/api/apigx/price-rules', payload)
  return res.data
}

export async function applyRouteRanking(
  payload: ApplyRouteRankingPayload
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post('/api/apigx/route-apply', payload)
  return res.data
}
// 供给审核（平台侧动作：发布 / 驳回）。
// 与分享者的 `submit` 分开：这两个动作在**平台侧**页面出现，接口也要求审核者身份。
export async function publishOffering(
  offeringId: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/offerings/${offeringId}/publish`)
  return res.data
}

export async function rejectOffering(
  offeringId: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/offerings/${offeringId}/reject`)
  return res.data
}

export async function getReviewOfferings(
  options: {
    page?: number
    pageSize?: number
    all?: boolean
  } = {}
): Promise<ReviewOfferingListResponse> {
  const res = await api.get('/api/apigx/reviews/offerings', {
    params: {
      page: options.page ?? 1,
      page_size: options.pageSize ?? 100,
      status: options.all ? 'all' : undefined,
    },
  })
  return res.data
}
// 模型目录（平台侧）：候选列表与确认动作。
export async function getAliasCandidates(
  state = 0,
  page = 1,
  pageSize = 50
): Promise<AliasCandidateListResponse> {
  const res = await api.get('/api/apigx/model-catalog/candidates', {
    params: { state, page, page_size: pageSize },
  })
  return res.data
}

export async function confirmAliasCandidate(
  candidateId: number,
  payload: ConfirmAliasPayload
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(
    `/api/apigx/model-catalog/candidates/${candidateId}/confirm`,
    payload
  )
  return res.data
}

export async function rejectAliasCandidate(
  candidateId: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(
    `/api/apigx/model-catalog/candidates/${candidateId}/reject`
  )
  return res.data
}

// 批量匹配：把归一化后同名的待确认候选一次性指到同一个标准模型。
export async function bulkConfirmAliases(
  normalizedModelID: string,
  payload: ConfirmAliasPayload
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post('/api/apigx/model-catalog/bulk-confirm', {
    normalized_model_id: normalizedModelID,
    ...payload,
  })
  return res.data
}
