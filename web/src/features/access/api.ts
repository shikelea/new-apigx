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

// 访问控制数据层（§4「访问授权」/「邀请兑换」）：发放、查看、撤销邀请与授权。

export interface AccessInvite {
  id: number
  token_prefix: string
  target_scope_type: number
  target_scope_id: number
  max_uses: number
  used_count: number
  status: number
  expires_at: number
  created_at: number
}

export interface AccessGrant {
  id: number
  subject_type: number
  subject_id: number
  subject_display_name?: string
  scope_kind: number
  source_invite_id: number
  expires_at: number
  created_at: number
}

/** 发放资源访问邀请；**明文邀请码只在响应里出现这一次**（库里只有哈希）。 */
export async function createAccessInvite(input: {
  target_scope_type: number
  target_scope_id: number
  max_uses?: number
  expires_at?: number
  revoke_grants?: boolean
}): Promise<{
  success: boolean
  data?: { token: string; token_prefix: string; id: number }
}> {
  const res = await api.post('/api/apigx/access/invites', input)
  return res.data
}

export async function listAccessInvites(): Promise<{
  success: boolean
  data?: { items: AccessInvite[] }
}> {
  const res = await api.get('/api/apigx/access/invites')
  return res.data
}

export async function revokeAccessInvite(
  inviteId: number,
  revokeGrants = true
): Promise<{ success: boolean }> {
  const res = await api.post(
    `/api/apigx/access/invites/${inviteId}/revoke?revoke_grants=${String(revokeGrants)}`
  )
  return res.data
}

export async function listGrants(
  resourceType: number,
  resourceId: number
): Promise<{ success: boolean; data?: { items: AccessGrant[] } }> {
  const res = await api.get('/api/apigx/access/grants', {
    params: { resource_type: resourceType, resource_id: resourceId },
  })
  return res.data
}

export async function createGrant(input: {
  resource_type: number
  resource_id: number
  target_username: string
  expires_at?: number
}): Promise<{ success: boolean; data?: AccessGrant }> {
  const res = await api.post('/api/apigx/access/grants', input)
  return res.data
}

export async function deleteGrant(
  grantId: number
): Promise<{ success: boolean }> {
  const res = await api.delete(`/api/apigx/access/grants/${grantId}`)
  return res.data
}
