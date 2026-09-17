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

import type {
  CapacityPolicyPayload,
  ChannelDetailResponse,
  OfferingListResponse,
  ShareChannelListResponse,
  ShareGroupListResponse,
  UpstreamKeyListResponse,
} from './types'

// 「我的分享」数据访问层：只做请求与类型，错误交给 Query 失败回调（规范 3.9）。
export async function getShareGroups(): Promise<ShareGroupListResponse> {
  const res = await api.get('/api/apigx/share-groups')
  return res.data
}

export async function getShareChannels(): Promise<ShareChannelListResponse> {
  const res = await api.get('/api/apigx/channels')
  return res.data
}
// 渠道详情的数据访问：渠道本身、它的 Key 池、它的供给。
// 说明：Key 池接口**只返回掩码**（凭据永不出后端），因此界面无法也不应展示明文。
export async function getChannel(channelId: number): Promise<ChannelDetailResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}`)
  return res.data
}

export async function getChannelKeys(channelId: number): Promise<UpstreamKeyListResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}/keys`)
  return res.data
}

export async function getChannelOfferings(channelId: number): Promise<OfferingListResponse> {
  const res = await api.get(`/api/apigx/channels/${channelId}/offerings`)
  return res.data
}
/**
 * 一键获取该渠道的上游模型；`import` 为 true 时**批量导入为草稿供给**。
 *
 * 复用底座的取模型能力（40+ 家适配器），这里只是触发与落库。
 */
export async function fetchUpstreamModels(channelId: number, importAsDraft: boolean): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/channels/${channelId}/fetch-models`, { import: importAsDraft })
  return res.data
}

/** 设置容量策略（写入四态原始值，解释由后端统一负责）。 */
export async function setChannelCapacityPolicy(
  channelId: number,
  payload: CapacityPolicyPayload
): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.put(`/api/apigx/channels/${channelId}/capacity-policy`, payload)
  return res.data
}

/** 提交供给审核（发布/驳回是平台侧动作，分享者只能提交）。 */
export async function submitOffering(offeringId: number): Promise<{ success: boolean; data?: unknown }> {
  const res = await api.post(`/api/apigx/offerings/${offeringId}/submit`)
  return res.data
}