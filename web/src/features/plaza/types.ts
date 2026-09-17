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
// 模型广场（分享侧）的对外数据结构。
//
// 字段与后端一致：`GET /api/apigx/plaza` 返回的是**按供给（offering）**的一组条目，
// 「同一模型的多个来源」由页面自行聚合（计划书：模型卡片显示可访问来源数）。

/** 供给条目：某个分享渠道向广场发布的一条可用模型供给。 */
export interface PlazaOfferingItem {
  /** 供给对外 ID（详情页与授权都以此为准） */
  offering_public_id: string
  /** 对外标准模型 ID */
  model: string
  /** 分享渠道对外 ID */
  channel_public_id: string
  /** 分享者用户 ID */
  owner_user_id: number
  /** 该供给声明的能力（如 chat / embedding / image） */
  capabilities?: string[]
  /** 尚未验证的能力（后端仅在存在时返回） */
  unverified_capabilities?: string[]
  /** 更新时间（UTC epoch 秒） */
  updated_at: number
}

/** 广场列表响应。 */
export interface PlazaListResponse {
  success: boolean
  message?: string
  data?: {
    items: PlazaOfferingItem[]
    total: number
    page: number
    page_size: number
  }
}

/** 广场查询参数。 */
export interface GetPlazaParams {
  page?: number
  page_size?: number
}