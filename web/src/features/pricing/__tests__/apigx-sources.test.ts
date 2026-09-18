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

import { describe, expect, it } from 'vitest'

import type { PlazaOfferingItem } from '@/features/plaza/types'

import { shareGroupOptions, summarizeModelSources } from '../lib/apigx-sources'

const item = (over: Partial<PlazaOfferingItem> & { model: string }): PlazaOfferingItem =>
  ({ offering_public_id: over.model + '-o', ...over }) as PlazaOfferingItem

describe('模型广场：分享来源聚合', () => {
  it('同一模型的多个来源被汇总，分组名去重且排序', () => {
    const map = summarizeModelSources([
      item({ model: 'm1', group_name: 'B组' }),
      item({ model: 'm1', group_name: 'A组' }),
      item({ model: 'm1', group_name: 'B组' }),
      item({ model: 'm2', group_name: 'A组' }),
    ])
    expect(map.get('m1')?.sourceCount).toBe(3)
    expect(map.get('m1')?.groupNames).toEqual(['A组', 'B组'])
    expect(map.get('m2')?.sourceCount).toBe(1)
  })

  it('空模型名被忽略（不产生无意义的条目）', () => {
    const map = summarizeModelSources([item({ model: '' }), item({ model: 'm1' })])
    expect([...map.keys()]).toEqual(['m1'])
  })

  it('"全部样本不足"只在确实拿到质量字段时为真（不拿缺数据冒充质量差）', () => {
    const withQuality = summarizeModelSources([
      item({ model: 'm1', sample_count: 3, insufficient: true }),
      item({ model: 'm1', sample_count: 0, insufficient: true }),
    ])
    expect(withQuality.get('m1')?.allInsufficient).toBe(true)

    const withoutQuality = summarizeModelSources([item({ model: 'm1' })])
    expect(withoutQuality.get('m1')?.allInsufficient).toBe(false)
  })

  it('已确认的标准模型 ID 会被带出（§8.2 对外标准 ID）', () => {
    const map = summarizeModelSources([
      item({ model: 'm1' }),
      item({ model: 'm1', canonical_model_id: 'standard-1' }),
    ])
    expect(map.get('m1')?.canonicalModelID).toBe('standard-1')
  })

  it('分享分组选项去重排序，空名不进入选项', () => {
    const options = shareGroupOptions([
      item({ model: 'm1', group_name: 'B组' }),
      item({ model: 'm2', group_name: 'A组' }),
      item({ model: 'm3', group_name: 'B组' }),
      item({ model: 'm4' }),
    ])
    expect(options).toEqual(['A组', 'B组'])
  })
})
