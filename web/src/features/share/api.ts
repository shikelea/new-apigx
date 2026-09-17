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