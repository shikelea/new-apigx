/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { api } from '@/lib/api'

import type {
  ShareCenterConfig,
  ShareCenterConfigResponse,
  SharePost,
  SharePostPageResponse,
  SharePostResponse,
} from './types'

export async function getShareCenterPosts(
  keyword = ''
): Promise<SharePostPageResponse> {
  const res = await api.get('/api/share-center', {
    params: keyword.trim() ? { keyword: keyword.trim() } : undefined,
  })
  return res.data
}

export async function getShareCenterPost(
  publicID: string
): Promise<SharePostResponse> {
  const res = await api.get(`/api/share-center/${encodeURIComponent(publicID)}`)
  return res.data
}

export async function getMyShareCenterPosts(): Promise<SharePostPageResponse> {
  const res = await api.get('/api/share-center/mine')
  return res.data
}

export async function createShareCenterPost(
  input: Pick<SharePost, 'title' | 'summary' | 'content' | 'visibility'>
): Promise<SharePostResponse> {
  const res = await api.post('/api/share-center', input)
  return res.data
}

export async function updateShareCenterPost(
  publicID: string,
  input: Pick<SharePost, 'title' | 'summary' | 'content' | 'visibility'>
): Promise<SharePostResponse> {
  const res = await api.put(
    `/api/share-center/${encodeURIComponent(publicID)}`,
    input
  )
  return res.data
}

export async function submitShareCenterPost(
  publicID: string
): Promise<SharePostResponse> {
  const res = await api.post(
    `/api/share-center/${encodeURIComponent(publicID)}/submit`
  )
  return res.data
}

export async function archiveShareCenterPost(
  publicID: string
): Promise<{ success: boolean }> {
  const res = await api.delete(
    `/api/share-center/${encodeURIComponent(publicID)}`
  )
  return res.data
}

export async function voteShareCenterPost(
  publicID: string,
  value: 1 | -1
): Promise<SharePostResponse> {
  const res = await api.post(
    `/api/share-center/${encodeURIComponent(publicID)}/vote`,
    { value }
  )
  return res.data
}

export async function getShareCenterReviews(): Promise<SharePostPageResponse> {
  const res = await api.get('/api/share-center/admin/posts')
  return res.data
}

export async function reviewShareCenterPost(
  publicID: string,
  action: 'approve' | 'reject',
  reason = ''
): Promise<SharePostResponse> {
  const res = await api.post(
    `/api/share-center/admin/posts/${encodeURIComponent(publicID)}/${action}`,
    action === 'reject' ? { reason } : {}
  )
  return res.data
}

export async function getShareCenterConfig(): Promise<ShareCenterConfigResponse> {
  const res = await api.get('/api/share-center/admin/config')
  return res.data
}

export async function updateShareCenterConfig(
  config: ShareCenterConfig
): Promise<ShareCenterConfigResponse> {
  const res = await api.put('/api/share-center/admin/config', config)
  return res.data
}
