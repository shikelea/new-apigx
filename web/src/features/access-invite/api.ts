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

// 资源邀请兑换入口的数据访问层（§4 第 3 条：资源邀请通过**独立兑换入口**处理）。
// 只做请求与类型，错误交给调用方处理（规范 3.9）。

export interface RedeemGrant {
  grant_id: number
  resource_type: number
  resource_id: number
  scope_kind: number
  source_invite_id: number
  expires_at: number
}

export interface RedeemResponse {
  success: boolean
  message?: string
  data?: RedeemGrant
}

export async function redeemAccessInvite(token: string): Promise<RedeemResponse> {
  const res = await api.post('/api/apigx/access/invites/redeem', {
    token: token.trim(),
  })
  return res.data
}
