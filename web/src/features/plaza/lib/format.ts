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
*/import type { RouteExplainCandidate } from '../types'

/**
 * ppm（百万分比）转百分数文本；样本量为 0 时返回空串，避免把"没有数据"显示成 "0%"。
 *
 * 计划书要求「显示样本不足及陈旧数据」——因此这里不把缺失值伪装成 0，
 * 由调用方配合 `insufficient` / `stale` 标记展示。
 */
export function formatPpm(ppm: number, sampleCount: number): string {
  if (sampleCount <= 0) {
    return ''
  }
  return `${(ppm / 10000).toFixed(2)}%`
}

/** 微美元转美元文本（保留 6 位，价格常小到 1e-6 量级）。 */
export function formatMicroUSD(micro: number): string {
  return (micro / 1_000_000).toFixed(6)
}

/** 候选的质量文本：样本不足时明确写出"样本不足"，而不是给一个看起来可信的数字。 */
export function describeCandidateQuality(candidate: RouteExplainCandidate): string {
  if (candidate.insufficient) {
    return `insufficient(${candidate.sample_count})`
  }
  if (candidate.stale) {
    return 'stale'
  }
  return formatPpm(candidate.reliability_lower_bound_ppm, candidate.sample_count)
}