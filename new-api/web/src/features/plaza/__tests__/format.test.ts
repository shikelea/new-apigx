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
*/import { describe, expect, it } from 'vitest'

import { describeCandidateQuality, describePlazaQuality, formatMicroUSD, formatPpm } from '../lib/format'
import type { RouteExplainCandidate } from '../types'

function candidate(overrides: Partial<RouteExplainCandidate> = {}): RouteExplainCandidate {
  return {
    offering_public_id: 'off_1',
    channel_public_id: 'ch_1',
    owner_user_id: 1,
    model_name: 'glm-5.3-flash',
    rank: 0,
    selected: true,
    score: 1,
    sample_count: 100,
    success_rate_ppm: 990000,
    reliability_lower_bound_ppm: 980000,
    latency_ms_p50: 120,
    insufficient: false,
    stale: false,
    price_known: true,
    input_price_micro: 2000000,
    output_price_micro: 8000000,
    ...overrides,
  }
}

describe('广场数值格式化', () => {
  it('ppm 按百分数展示（两位小数）', () => {
    expect(formatPpm(981246, 100)).toBe('98.12%')
  })

  it('没有样本时不显示 0%，而是留空（避免把"没有数据"当成"成功率 0"）', () => {
    expect(formatPpm(0, 0)).toBe('')
  })

  it('微美元按 6 位小数展示', () => {
    expect(formatMicroUSD(2000000)).toBe('2.000000')
  })
})

describe('候选质量描述', () => {
  it('样本不足时明确写"样本不足"并给出样本数', () => {
    expect(describeCandidateQuality(candidate({ insufficient: true, sample_count: 3 }))).toBe('insufficient(3)')
  })

  it('数据陈旧时标记为 stale', () => {
    expect(describeCandidateQuality(candidate({ stale: true }))).toBe('stale')
  })

  it('正常时给出可靠性下界百分数', () => {
    expect(describeCandidateQuality(candidate())).toBe('98.00%')
  })
})

describe('广场卡片的质量摘要', () => {
  it('没有样本时显示"样本不足"，而不是编一个百分比', () => {
    expect(describePlazaQuality({ sample_count: 0, insufficient: true })).toBe('insufficient')
  })

  it('数据陈旧时显示 stale', () => {
    expect(describePlazaQuality({ sample_count: 100, insufficient: false, stale: true })).toBe('stale')
  })

  it('正常时显示可靠性下界百分比（与路由候选同一口径）', () => {
    expect(describePlazaQuality({ sample_count: 100, insufficient: false, reliability_lower_bound_ppm: 980000 })).toBe('98.00%')
  })
})
