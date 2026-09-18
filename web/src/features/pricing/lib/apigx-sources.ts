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

import type { PlazaOfferingItem } from '@/features/plaza/types'

/**
 * 广场（分享来源）聚合：把 `/api/apigx/plaza` 的条目按**模型名**汇总，
 * 供底座的模型广场（`/pricing`）展示"这个模型有几处分享来源、属于哪些分享分组、质量如何"。
 *
 * 纯函数，便于单测；不做任何请求。
 */
export interface ModelSourcesSummary {
  /** 分享来源条数（有几条已发布供给在提供这个模型） */
  sourceCount: number
  /** 提供该模型的分享分组名（去重、排序） */
  groupNames: string[]
  /** 是否全部样本不足（质量不可判定）—— 只有真拿到质量字段时才表达 */
  allInsufficient: boolean
  /** 是否有已确认的标准模型 ID（§8.2：对外标准 ID） */
  canonicalModelID?: string
}

export function summarizeModelSources(items: PlazaOfferingItem[]): Map<string, ModelSourcesSummary> {
  const byModel = new Map<string, PlazaOfferingItem[]>()
  for (const item of items) {
    if (!item.model) {
      continue
    }
    const list = byModel.get(item.model)
    if (list) {
      list.push(item)
    } else {
      byModel.set(item.model, [item])
    }
  }

  const out = new Map<string, ModelSourcesSummary>()
  for (const [model, list] of byModel) {
    const groupNames = [...new Set(list.map((item) => item.group_name ?? '').filter((name) => name !== ''))].sort()
    const withQuality = list.filter((item) => typeof item.sample_count === 'number')
    out.set(model, {
      sourceCount: list.length,
      groupNames,
      // 只有确实拿过质量字段、且全部样本不足时才为真（不拿"没数据"冒充"质量差"）
      allInsufficient: withQuality.length > 0 && withQuality.every((item) => item.insufficient === true),
      canonicalModelID: list.find((item) => item.canonical_model_id)?.canonical_model_id,
    })
  }
  return out
}

/** 分享分组的可选项（供篩选使用）；空值表示"全部"。 */
export function shareGroupOptions(items: PlazaOfferingItem[]): string[] {
  return [...new Set(items.map((item) => item.group_name ?? '').filter((name) => name !== ''))].sort()
}
