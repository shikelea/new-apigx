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

import type { ShareChannelListResponse, ShareGroupListResponse } from './types'

// 「我的分享」数据访问层：只做请求与类型，错误交给 Query 失败回调（规范 3.9）。
export async function getShareGroups(): Promise<ShareGroupListResponse> {
  const res = await api.get('/api/apigx/share-groups')
  return res.data
}

export async function getShareChannels(): Promise<ShareChannelListResponse> {
  const res = await api.get('/api/apigx/channels')
  return res.data
}