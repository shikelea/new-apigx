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

import { aggregatePlazaItems, filterPlazaCards } from '../lib/aggregate'
import { makeItem } from './fixtures'

describe('广场聚合', () => {
  it('同一模型的多个来源合并成一张卡片，并保留全部来源', () => {
    const cards = aggregatePlazaItems([
      makeItem({ offering_public_id: 'o1', model: 'glm-5.3-flash' }),
      makeItem({ offering_public_id: 'o2', model: 'glm-5.3-flash' }),
      makeItem({ offering_public_id: 'o3', model: 'qwen3.8-max' }),
    ])

    expect(cards).toHaveLength(2)
    const flash = cards.find((card) => card.model === 'glm-5.3-flash')
    expect(flash?.offerings).toHaveLength(2)
    expect(cards.find((card) => card.model === 'qwen3.8-max')?.offerings).toHaveLength(1)
  })

  it('多个来源声明同一能力时只保留一份', () => {
    const cards = aggregatePlazaItems([
      makeItem({ capabilities: ['chat', 'image'] }),
      makeItem({ capabilities: ['chat', 'embedding'] }),
    ])

    expect(cards[0].capabilities).toEqual(['chat', 'image', 'embedding'])
  })

  it('没有来源时返回空列表（不伪造卡片）', () => {
    expect(aggregatePlazaItems([])).toEqual([])
  })
})

describe('广场搜索过滤', () => {
  const cards = aggregatePlazaItems([
    makeItem({ model: 'glm-5.3-flash' }),
    makeItem({ model: 'qwen3.8-max' }),
  ])

  it('关键字为空时原样返回', () => {
    expect(filterPlazaCards(cards, '   ')).toHaveLength(2)
  })

  it('按模型名做不区分大小写的包含匹配', () => {
    expect(filterPlazaCards(cards, 'QWEN').map((card) => card.model)).toEqual(['qwen3.8-max'])
  })

  it('没有匹配时返回空列表', () => {
    expect(filterPlazaCards(cards, 'gpt')).toEqual([])
  })
})
