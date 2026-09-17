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
  /** 简介（不可信文本，按纯文本渲染） */
  description: string
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
// 渠道详情相关类型（字段与后端 `getChannel` / `listUpstreamKeys` / `listOfferings` 一致）。

export interface ChannelDetail {
  id: number
  public_id: string
  name: string
  protocol: string
  base_url: string
  key_pool_mode: number
  health_state: number
  health_reason: string
}

/** 上游 Key。注意：**凭据本身永不返回**，`masked` 固定为掩码占位。 */
export interface UpstreamKey {
  id: number
  stable_key_id: string
  name: string
  /** 0=启用 / 1=停用 / 2=自动禁用（冷却中） */
  status: number
  /** 权重（= 流量占比；同池内归一） */
  weight: number
  cooldown_until: number
  expires_at: number
  credential_version: number
  /** 后端判定的"当前是否可用"（含停用、过期、冷却） */
  usable: boolean
  masked: string
}

/** 供给（某渠道发布的某上游模型）。 */
export interface Offering {
  id: number
  public_id: string
  upstream_model_id: string
  status: number
  revision: number
  capabilities?: string[]
  price_rule_id: number
}

export interface ChannelDetailResponse {
  success: boolean
  data?: ChannelDetail
}

export interface UpstreamKeyListResponse {
  success: boolean
  data?: { items: UpstreamKey[] }
}

export interface OfferingListResponse {
  success: boolean
  data?: { items: Offering[] }
}
/**
 * 容量策略请求体。
 *
 * 各维度都是**四态原始值**（0 未配置 / 1 禁用 / 2 继承 / 3 明确不限 / 4 明确值），
 * 解释由后端统一负责 —— 因此界面只需原样提交用户选择，不在这里做任何语义推断。
 */
export type LimitMode = 0 | 1 | 2 | 3 | 4

export interface CapacityPolicyPayload {
  concurrency_mode?: LimitMode
  concurrency_value?: number
  rpm_mode?: LimitMode
  rpm_limit?: number
  tpm_mode?: LimitMode
  tpm_limit?: number
}