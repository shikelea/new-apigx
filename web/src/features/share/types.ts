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
// 「我的分享」的对外数据结构。字段与后端一致（`GET /api/apigx/share-groups`、`GET /api/apigx/channels`）。

/** 分享分组。 */
export interface ShareGroup {
  id: number
  public_id: string
  name: string
  /** 默认可见性（0=公开 / 1=邀请 / 2=指定用户 / 3=仅自己） */
  default_visibility: number
  /** 发布状态 */
  publish_status: number
  updated_at: number
}

/** 分享渠道。 */
export interface ShareChannel {
  id: number
  public_id: string
  name: string
  /** 客户端协议族（如 openai） */
  protocol: string
  /** Key 池模式（0=轮询 / 1=随机） */
  key_pool_mode: number
  /** 健康状态（由逐 Key 结果推导） */
  health_state: number
  /** 上游 Base URL */
  base_url: string
}

export interface ShareGroupListResponse {
  success: boolean
  data?: { items: ShareGroup[] }
}

export interface ShareChannelListResponse {
  success: boolean
  data?: { items: ShareChannel[] }
}