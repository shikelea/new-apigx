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
  /** 对外**标准模型 ID** 与介绍：只有"上游名 → 标准模型"的映射**已确认**时才有值 */
  canonical_model_id?: string
  model_description?: string
  /** 分享分组对外 ID 与名称（广场按"分享分组"组织） */
  group_public_id?: string
  group_name?: string
  /** 分享渠道对外 ID */
  channel_public_id: string
  /** 分享者用户 ID */
  owner_user_id: number
  /** 该供给声明的能力（如 chat / embedding / image） */
  capabilities?: string[]
  /** 尚未验证的能力（后端仅在存在时返回） */
  unverified_capabilities?: string[]
  /** 质量摘要（最近 6 小时窗口）：样本不足时 insufficient 为 true，界面必须显示"样本不足" */
  sample_count?: number
  reliability_lower_bound_ppm?: number
  latency_ms_p50?: number
  insufficient?: boolean
  stale?: boolean
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

/**
 * 「这个模型为什么选了/没选某条来源」的单个候选。
 *
 * 字段与后端 `routing.CandidateExplanation` 一致；比例类字段单位为 **ppm（百万分比）**，
 * 价格类字段单位为 **微美元**，展示时由 `lib/format.ts` 统一换算。
 */
export interface RouteExplainCandidate {
  offering_public_id: string
  channel_public_id: string
  owner_user_id: number
  model_name: string
  rank: number
  selected: boolean
  score: number
  reasons?: string[]
  sample_count: number
  success_rate_ppm: number
  reliability_lower_bound_ppm: number
  latency_ms_p50: number
  insufficient: boolean
  stale: boolean
  price_known: boolean
  input_price_micro: number
  output_price_micro: number
  /** 被排除的原因（仅在被排除时返回） */
  excluded_reason?: string
}

/** `GET /api/apigx/route-explain` 的响应。 */
export interface RouteExplainResponse {
  success: boolean
  message?: string
  data?: {
    strategy: string
    candidates: RouteExplainCandidate[] | null
    cold_start: boolean
    reason?: string
  }
}