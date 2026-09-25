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
*/import type { CapacityPolicyPayload, LimitMode } from '../types'

/** 某一维度的界面状态：模式 + 数值（仅"明确值"使用数值）。 */
export interface LimitDimensionInput {
  mode: LimitMode
  value: string
}

/**
 * 把三个维度的界面输入组装成请求体。
 *
 * **只在"明确值"模式带上数值**：其它模式下数值无意义，带上会误导后端与审计
 * （例如"明确不限"却带一个 limit=100，看起来像设置了一个上限）。
 * 空串或非数字一律不带 —— 由后端按"明确值但缺数值"拒绝，而不是我们猜一个 0。
 */
export function buildCapacityPolicyPayload(input: {
  concurrency: LimitDimensionInput
  rpm: LimitDimensionInput
  tpm: LimitDimensionInput
}): CapacityPolicyPayload {
  const payload: CapacityPolicyPayload = {
    concurrency_mode: input.concurrency.mode,
    rpm_mode: input.rpm.mode,
    tpm_mode: input.tpm.mode,
  }
  applyValue(payload, 'concurrency_value', input.concurrency)
  applyValue(payload, 'rpm_limit', input.rpm)
  applyValue(payload, 'tpm_limit', input.tpm)
  return payload
}

function applyValue(
  payload: CapacityPolicyPayload,
  field: 'concurrency_value' | 'rpm_limit' | 'tpm_limit',
  input: LimitDimensionInput
): void {
  if (input.mode !== 4) {
    return
  }
  const parsed = Number.parseInt(input.value, 10)
  if (!Number.isFinite(parsed) || parsed < 0) {
    return
  }
  payload[field] = parsed
}