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

export type SharePostStatus =
  | 'draft'
  | 'pending'
  | 'published'
  | 'rejected'
  | 'archived'

export type SharePostVisibility = 'public' | 'login' | 'private'

export interface SharePost {
  id: number
  public_id: string
  author_id: number
  author_name?: string
  title: string
  summary: string
  content: string
  visibility: SharePostVisibility
  status: SharePostStatus
  reject_reason?: string
  usable_votes: number
  invalid_votes: number
  published_at: number
  created_at: number
  updated_at: number
}

export interface SharePostPage {
  items: SharePost[]
  total: number
  page: number
  page_size: number
}

export interface SharePostPageResponse {
  success: boolean
  message?: string
  data?: SharePostPage
}

export interface SharePostResponse {
  success: boolean
  message?: string
  data?: SharePost
}

export interface ShareCenterConfig {
  require_login: boolean
}

export interface ShareCenterConfigResponse {
  success: boolean
  data?: ShareCenterConfig
}
