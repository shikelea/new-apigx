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
  CanonicalModelListResponse,
  BulkSubmitOfferingsResponse,
  CapacityPolicyPayload,
  ChannelDetailResponse,
  ChannelRecipeListResponse,
  ManualPriceItemInput,
  ManualPriceResponse,
  OfferingListResponse,
  PriceMatchConfirmResponse,
  PriceMatchPreviewResponse,
  ShareChannelListResponse,
  ShareGroup,
  ShareGroupListResponse,
  UpstreamKeyListResponse,
} from './types'

// 「我的分享」数据访问层：只做请求与类型，错误交给 Query 失败回调（规范 3.9）。
export async function getShareGroups(): Promise<ShareGroupListResponse> {
  const res = await api.get('/api/apigx/share-groups')
  return res.data
}

export async function getChannelRecipes(): Promise<ChannelRecipeListResponse> {
  const res = await api.get('/api/apigx/channels/recipes')
  return res.data
}

/**
 * 新建分享渠道（§4「渠道 CRUD」）。
 * 只传服务端目录中的稳定配方 ID（以及名称/上游地址/自己的上游 Key）—— 平台会**代建底座渠道**并绑定，
 * 分享者不需要知道管理员侧的渠道 ID。
 */
export async function createShareChannel(input: {
  share_group_id: number
  name: string
  recipe_id: string
  base_url: string
  upstream_keys: string[]
  key_pool_mode?: number
}): Promise<{ success: boolean; data?: { id: number; public_id: string } }> {
  const res = await api.post('/api/apigx/channels', input)
  return res.data
}

export async function getShareChannels(): Promise<ShareChannelListResponse> {
  const res = await api.get('/api/apigx/channels')
  return res.data
}
// 渠道详情的数据访问：渠道本身、它的 Key 池、它的供给。
// 说明：Key 池接口**只返回掩码**（凭据永不出后端），因此界面无法也不应展示明文。
export async function getChannel(
  channelId: number
): Promise<ChannelDetailResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}`)
  return res.data
}

export interface ShareChannelTestResponse {
  success: boolean
  message?: string
  time?: number
  error_code?: string
}

export async function testShareChannel(
  channelId: number,
  params?: { model?: string; endpoint_type?: string; stream?: boolean }
): Promise<ShareChannelTestResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}/test`, {
    params,
  })
  return res.data
}

export async function getChannelKeys(
  channelId: number
): Promise<UpstreamKeyListResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}/keys`)
  return res.data
}

export async function createUpstreamKey(
  channelId: number,
  payload: { name: string; key: string; weight: number; expires_at?: number }
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/channels/${channelId}/keys`, payload)
  return res.data
}

export async function createUpstreamKeysBatch(
  channelId: number,
  payload: { keys: string[]; weight: number }
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(
    `/api/apigx/channels/${channelId}/keys/batch`,
    payload
  )
  return res.data
}

export async function rotateUpstreamKey(
  keyId: number,
  key: string
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/keys/${keyId}/rotate`, { key })
  return res.data
}

export async function updateUpstreamKeyStatus(
  keyId: number,
  status: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.patch(`/api/apigx/keys/${keyId}/status`, { status })
  return res.data
}

export async function updateUpstreamKeyWeight(
  keyId: number,
  weight: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.patch(`/api/apigx/keys/${keyId}/weight`, { weight })
  return res.data
}

export async function applyChannelKeyPool(
  channelId: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/channels/${channelId}/apply`)
  return res.data
}

export async function getChannelOfferings(
  channelId: number
): Promise<OfferingListResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}/offerings`)
  return res.data
}

export async function previewChannelPriceMatch(
  channelId: number
): Promise<PriceMatchPreviewResponse> {
  const res = await api.post(
    `/api/apigx/channels/${channelId}/price-match/preview`
  )
  return res.data
}

export async function confirmChannelPriceMatch(
  channelId: number,
  selections: { offering_id: number; candidate_token: string }[]
): Promise<PriceMatchConfirmResponse> {
  const res = await api.post(
    `/api/apigx/channels/${channelId}/price-match/confirm`,
    { selections }
  )
  return res.data
}

export async function setOfferingManualPrice(
  offeringId: number,
  items: ManualPriceItemInput[]
): Promise<ManualPriceResponse> {
  const res = await api.put(`/api/apigx/offerings/${offeringId}/manual-price`, {
    items,
  })
  return res.data
}
/**
 * 一键获取该渠道的上游模型；`import` 为 true 时**批量导入为草稿供给**。
 *
 * 复用底座的取模型能力（40+ 家适配器），这里只是触发与落库。
 */
/** 为**指定上游模型**建一条供给（草稿）—— 用于"勾选导入"，而不是一次性全量导入。 */
export async function createOffering(
  channelId: number,
  upstreamModelID: string,
  canonicalModelID?: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/channels/${channelId}/offerings`, {
    upstream_model_id: upstreamModelID,
    canonical_model_id: canonicalModelID,
  })
  return res.data
}

export async function getCanonicalModels(): Promise<CanonicalModelListResponse> {
  const res = await api.get('/api/apigx/model-catalog/models')
  return res.data
}

export async function setOfferingModelMapping(
  offeringId: number,
  targetCanonicalModelID: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.put(
    `/api/apigx/offerings/${offeringId}/model-mapping`,
    { target_canonical_model_id: targetCanonicalModelID }
  )
  return res.data
}

export async function fetchUpstreamModels(
  channelId: number,
  importAsDraft: boolean
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/channels/${channelId}/fetch-models`, {
    import: importAsDraft,
  })
  return res.data
}

/** 设置容量策略（写入四态原始值，解释由后端统一负责）。 */
export async function setChannelCapacityPolicy(
  channelId: number,
  payload: CapacityPolicyPayload
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.put(
    `/api/apigx/channels/${channelId}/capacity-policy`,
    payload
  )
  return res.data
}

/** 提交供给审核（发布/驳回是平台侧动作，分享者只能提交）。 */
export async function submitOffering(
  offeringId: number
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/offerings/${offeringId}/submit`)
  return res.data
}

export async function submitChannelOfferings(
  channelId: number
): Promise<BulkSubmitOfferingsResponse> {
  const res = await api.post(
    `/api/apigx/channels/${channelId}/offerings/submit`
  )
  return res.data
}

// 更新分组的展示名与简介（计划书 §4"分享名称、介绍"）。
/** 创建分享分组（§4：分组是分享的组织单位，可见性在此设定）。 */
export async function createShareGroup(input: {
  name: string
  description?: string
  default_visibility?: number
}): Promise<{ success: boolean; data?: ShareGroup }> {
  const res = await api.post('/api/apigx/share-groups', input)
  return res.data
}

export async function updateShareGroup(
  groupId: number,
  input: { name: string; description: string }
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.patch(`/api/apigx/share-groups/${groupId}`, input)
  return res.data
}

// ── 删除（§4/§5 的 CRUD 含 Delete；后端接口早已提供，此前界面没有入口）────────────────

export async function deleteShareGroup(
  groupId: number
): Promise<{ success: boolean }> {
  const res = await api.delete(`/api/apigx/share-groups/${groupId}`)
  return res.data
}

export async function deleteShareChannel(
  channelId: number
): Promise<{ success: boolean }> {
  const res = await api.delete(`/api/apigx/channels/${channelId}`)
  return res.data
}

export async function deleteOffering(
  offeringId: number
): Promise<{ success: boolean }> {
  const res = await api.delete(`/api/apigx/offerings/${offeringId}`)
  return res.data
}

export async function deleteUpstreamKey(
  keyId: number
): Promise<{ success: boolean }> {
  const res = await api.delete(`/api/apigx/keys/${keyId}`)
  return res.data
}

// U-3：分享者视角的消耗汇总（按渠道 / 按 Key / 按模型）。
// Attempt 只提供请求质量；实际数量与成本来自 UsageEvent，不把未知价格伪装成零成本。
export interface UsageSummaryQuantity {
  capability: number
  dimension: number
  unit:
    | 'token'
    | 'image'
    | 'second'
    | 'request'
    | 'character'
    | 'document'
    | 'unknown'
  quantity_milli: number
  upstream_reported_milli: number
  local_estimated_milli: number
  unknown_event_count: number
  unavailable_event_count: number
}

export interface UsageSummaryItem {
  group_id?: number
  group_key: string
  name: string
  request_count: number
  attempt_count: number
  retry_count: number
  succeeded_attempt_count: number
  failed_attempt_count: number
  unfinished_attempt_count: number
  ttft_ms_sum: number
  ttft_samples: number
  estimated_cost_usd_micro: number
  estimated_cost_event_count: number
  pending_fx_event_count: number
  unpriced_event_count: number
  explicit_zero_event_count: number
  usage: UsageSummaryQuantity[]
}

export interface UsageSummaryData {
  group_by: 'channel' | 'key' | 'model'
  start: number
  end: number
  page: number
  page_size: number
  has_more: boolean
  items: UsageSummaryItem[]
}

export async function getUsageSummary(
  groupBy: 'channel' | 'key' | 'model',
  options: { start: number; end: number; page: number; pageSize: number }
): Promise<{ success: boolean; data?: UsageSummaryData }> {
  const res = await api.get('/api/apigx/usage/summary', {
    params: {
      group_by: groupBy,
      start: options.start,
      end: options.end,
      page: options.page,
      page_size: options.pageSize,
    },
  })
  return res.data
}
