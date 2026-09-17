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

import { aggregateUsageByModel, describeUsageSource } from '../lib/aggregate'
import type { UsageEntry } from '../types'

function entry(overrides: Partial<UsageEntry> = {}): UsageEntry {
  return {
    created_at: 1789640000,
    model: 'glm-5.3-flash',
    prompt_tokens: 10,
    completion_tokens: 5,
    total_tokens: 15,
    quota: 100,
    cost_micro_usd: 200,
    elapsed_seconds: 1,
    is_stream: false,
    request_id: 'req_1',
    ...overrides,
  }
}

describe('用量按模型汇总', () => {
  it('同一模型的多次请求合并，请求数与成本累加', () => {
    const rows = aggregateUsageByModel([
      entry({ total_tokens: 15, cost_micro_usd: 200 }),
      entry({ total_tokens: 5, cost_micro_usd: 100 }),
    ])

    expect(rows).toHaveLength(1)
    expect(rows[0].requests).toBe(2)
    expect(rows[0].total_tokens).toBe(20)
    expect(rows[0].cost_micro_usd).toBe(300)
  })

  it('按成本从高到低排序（先看花钱多的）', () => {
    const rows = aggregateUsageByModel([
      entry({ model: 'cheap', cost_micro_usd: 1 }),
      entry({ model: 'pricey', cost_micro_usd: 1000 }),
    ])

    expect(rows.map((row) => row.model)).toEqual(['pricey', 'cheap'])
  })

  it('没有记录时返回空列表（不伪造 0 行）', () => {
    expect(aggregateUsageByModel([])).toEqual([])
  })
})

describe('用量来源描述', () => {
  it('分享来源显示渠道对外 ID', () => {
    expect(describeUsageSource(entry({ source: { kind: 'share', channel_public_id: 'ch_9' } }))).toBe('ch_9')
  })

  it('平台自有显示 platform', () => {
    expect(describeUsageSource(entry({ source: { kind: 'platform' } }))).toBe('platform')
  })

  it('没有来源信息时不编造（返回空串）', () => {
    expect(describeUsageSource(entry())).toBe('')
  })
})