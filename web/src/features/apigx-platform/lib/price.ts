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
*/import { PRICE_SCOPE_CHANNEL, PRICE_SCOPE_OFFERING, PRICE_SCOPE_UPSTREAM_KEY } from '../types'

/** 价格作用域选项（与后端一致：0=供给 / 1=上游 Key / 2=渠道）。 */
export const PRICE_SCOPE_OPTIONS: { value: number; labelKey: string }[] = [
  { value: PRICE_SCOPE_OFFERING, labelKey: 'apigxPlatform.priceScope.offering' },
  { value: PRICE_SCOPE_UPSTREAM_KEY, labelKey: 'apigxPlatform.priceScope.upstreamKey' },
  { value: PRICE_SCOPE_CHANNEL, labelKey: 'apigxPlatform.priceScope.channel' },
]

/**
 * 解析用户填写的「每百万 token 美元价」。
 *
 * 与"未知价格"的既有取向一致：**空串或非法输入一律视为无效（返回 null）**，
 * 绝不回退成 0 —— 0 是一个真实价格，会让"价格缺失"看起来像"免费"（阶段 3 讨论过这一点）。
 */
export function parseUsdPerMillion(text: string): number | null {
  const trimmed = text.trim()
  if (trimmed === '') {
    return null
  }
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) {
    return null
  }
  return value
}

/** 人工覆盖价格是否可提交：作用域合法 + 输入与输出都是有效价格。 */
export function canSubmitManualPrice(form: {
  scopeType: number
  scopeId: string
  inputPrice: string
  outputPrice: string
}): boolean {
  const scopeId = Number.parseInt(form.scopeId.trim(), 10)
  if (!Number.isFinite(scopeId) || scopeId <= 0) {
    return false
  }
  return parseUsdPerMillion(form.inputPrice) !== null && parseUsdPerMillion(form.outputPrice) !== null
}
/**
 * 供给是否处于"可审核"状态。
 *
 * 只有**已提交**的供给能被发布或驳回：对草稿直接发布会让分享者跳过提交这一步，
 * 对已发布/已驳回的再审核会覆盖既有结论 —— 因此这里把规则固化下来（与后端状态机一致）。
 */
export const OFFERING_STATUS_DRAFT = 0
export const OFFERING_STATUS_SUBMITTED = 1
export const OFFERING_STATUS_PUBLISHED = 2
export const OFFERING_STATUS_REJECTED = 3

export function isOfferingReviewable(status: number): boolean {
  return status === OFFERING_STATUS_SUBMITTED
}