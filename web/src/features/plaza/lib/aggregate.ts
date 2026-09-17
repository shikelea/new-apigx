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
import type { PlazaOfferingItem } from '../types'

/** 广场上的一张模型卡片：同一模型的全部来源。 */
export interface PlazaModelCard {
  model: string
  /** 提供该模型的供给（可访问来源） */
  offerings: PlazaOfferingItem[]
  /** 各来源声明能力的并集，去重且保持稳定顺序 */
  capabilities: string[]
}

/**
 * 把后端返回的**按供给**列表聚合成**按模型**的卡片。
 *
 * 计划书（§4 页面结构）要求模型卡片显示「**可访问来源数**」，因此来源计数的口径
 * 必须与"用户实际可选的供给"一致 —— 这里只做分组与去重，不猜测可访问性
 * （可访问性由后端在列表阶段就筛好）。
 */
export function aggregatePlazaItems(items: PlazaOfferingItem[]): PlazaModelCard[] {
  const byModel = new Map<string, PlazaModelCard>()
  for (const item of items) {
    const existing = byModel.get(item.model)
    if (existing) {
      existing.offerings.push(item)
      for (const capability of item.capabilities ?? []) {
        if (!existing.capabilities.includes(capability)) {
          existing.capabilities.push(capability)
        }
      }
      continue
    }
    byModel.set(item.model, {
      model: item.model,
      offerings: [item],
      capabilities: [...(item.capabilities ?? [])],
    })
  }
  return [...byModel.values()]
}

/** 按模型名过滤（广场搜索框用的纯函数，便于测试）。 */
export function filterPlazaCards(cards: PlazaModelCard[], keyword: string): PlazaModelCard[] {
  const needle = keyword.trim().toLowerCase()
  if (needle === '') {
    return cards
  }
  return cards.filter((card) => card.model.toLowerCase().includes(needle))
}


/**
 * 按**分享分组**把卡片分组（我们的模式：广场按"谁在分享"组织，而不是平铺一堆）。
 *
 * 没有分组信息的条目统一归到「未分组」，**不丢数据**；分组内的顺序沿用传入顺序，
 * 保证同一份数据每次渲染的顺序一致。
 */
export interface PlazaGroupBucket {
  key: string
  name: string
  cards: PlazaModelCard[]
}

export function groupPlazaCards(cards: PlazaModelCard[]): PlazaGroupBucket[] {
  const buckets = new Map<string, PlazaGroupBucket>()
  for (const card of cards) {
    const key = card.offerings[0]?.group_public_id ?? ''
    const name = card.offerings[0]?.group_name ?? ''
    const existing = buckets.get(key)
    if (existing) {
      existing.cards.push(card)
      continue
    }
    buckets.set(key, { key, name, cards: [card] })
  }
  return [...buckets.values()]
}
