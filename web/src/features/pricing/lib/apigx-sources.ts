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
export interface ModelSourceGroup {
  /** 分享分组对外 ID（用于链接到分享分组页，§4） */
  publicID: string
  /** 分享分组名称（展示用） */
  name: string
}

export interface ModelSourcesSummary {
  /** 分享来源条数（有几条已发布供给在提供这个模型） */
  sourceCount: number
  /** 提供该模型的分享分组（按名称去重排序，带上对外 ID 以便链接） */
  groups: ModelSourceGroup[]
  /** 是否全部样本不足（质量不可判定）—— 只有真拿到质量字段时才表达 */
  allInsufficient: boolean
  /** 是否有已确认的标准模型 ID（§8.2：对外标准 ID） */
  canonicalModelID?: string
}

export type ResourceAccessKind = PlazaOfferingItem['access_kind']

export interface ResourceModelQualitySummary {
  /** 有质量字段的来源是否全部样本不足。 */
  allInsufficient: boolean
  /** 充分且新鲜样本中最高的 6 小时可靠性置信下界。 */
  bestReliabilityLowerBoundPpm?: number
  /** 所有已知来源中最低的 P50 延迟。 */
  lowestLatencyMsP50?: number
  /** 所有已知来源中最高的文本输出吞吐。 */
  highestOutputTokensPerSecond?: number
}

/** 资源广场卡片的模型视图。价格留在详情，质量摘要用于快速判断。 */
export interface ResourceModelSummary {
  modelID: string
  canonicalModelID?: string
  description?: string
  sources: PlazaOfferingItem[]
  quality: ResourceModelQualitySummary
}

export function summarizeResourceQuality(
  sources: PlazaOfferingItem[]
): ResourceModelQualitySummary {
  let qualitySourceCount = 0
  let insufficientSourceCount = 0
  let bestReliabilityLowerBoundPpm: number | undefined
  let lowestLatencyMsP50: number | undefined
  let highestOutputTokensPerSecond: number | undefined

  for (const source of sources) {
    const samples = source.sample_count
    if (
      typeof samples === 'number' ||
      typeof source.insufficient === 'boolean'
    ) {
      qualitySourceCount += 1
      if (source.insufficient === true || (samples ?? 0) <= 0) {
        insufficientSourceCount += 1
      }
    }

    const reliability = source.reliability_lower_bound_ppm
    if (
      source.insufficient !== true &&
      source.stale !== true &&
      (samples ?? 0) > 0 &&
      typeof reliability === 'number' &&
      Number.isFinite(reliability) &&
      reliability >= 0 &&
      (bestReliabilityLowerBoundPpm === undefined ||
        reliability > bestReliabilityLowerBoundPpm)
    ) {
      bestReliabilityLowerBoundPpm = reliability
    }

    const latency = source.latency_ms_p50
    if (
      typeof latency === 'number' &&
      Number.isFinite(latency) &&
      latency > 0 &&
      (lowestLatencyMsP50 === undefined || latency < lowestLatencyMsP50)
    ) {
      lowestLatencyMsP50 = latency
    }

    const throughput = source.output_tokens_per_second
    if (
      typeof throughput === 'number' &&
      Number.isFinite(throughput) &&
      throughput > 0 &&
      (highestOutputTokensPerSecond === undefined ||
        throughput > highestOutputTokensPerSecond)
    ) {
      highestOutputTokensPerSecond = throughput
    }
  }

  return {
    allInsufficient:
      qualitySourceCount > 0 && insufficientSourceCount === qualitySourceCount,
    bestReliabilityLowerBoundPpm,
    lowestLatencyMsP50,
    highestOutputTokensPerSecond,
  }
}

/**
 * 按用户可理解的资源范围筛选后，再把相同模型 ID 聚合为一张卡。
 * ShareGroup 仍是后端授权边界，但不作为普通用户的一级筛选维度。
 */
export function aggregateResourceModels(
  items: PlazaOfferingItem[],
  accessKind: ResourceAccessKind
): ResourceModelSummary[] {
  const byModel = new Map<string, PlazaOfferingItem[]>()
  for (const item of items) {
    if (item.access_kind !== accessKind || item.model.trim() === '') continue
    const current = byModel.get(item.model)
    if (current) current.push(item)
    else byModel.set(item.model, [item])
  }

  return [...byModel.entries()]
    .map(([modelID, sources]) => ({
      modelID,
      canonicalModelID: sources.find((source) => source.canonical_model_id)
        ?.canonical_model_id,
      description: sources.find((source) => source.model_description)
        ?.model_description,
      sources: [...sources].sort((left, right) => {
        const leftName = left.owner_display_name ?? left.group_name ?? ''
        const rightName = right.owner_display_name ?? right.group_name ?? ''
        return leftName.localeCompare(rightName)
      }),
      quality: summarizeResourceQuality(sources),
    }))
    .sort((left, right) => left.modelID.localeCompare(right.modelID))
}

export function summarizeModelSources(
  items: PlazaOfferingItem[]
): Map<string, ModelSourcesSummary> {
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
    // 按分组名去重（同一分组的多条来源只出现一次），再按名称排序。
    // 注意：即使拿不到对外 ID 也要**保留分组名** —— 否则筛选里会凭空少掉这个分组；
    // 对外 ID 只用于生成"分享分组页"的链接（没有 ID 时就不渲染链接）。
    const groupMap = new Map<string, string>()
    for (const item of list) {
      const name = item.group_name ?? ''
      if (name === '') {
        continue
      }
      const publicID = item.group_public_id ?? ''
      if (!groupMap.has(name) || groupMap.get(name) === '') {
        groupMap.set(name, publicID)
      }
    }
    const groups: ModelSourceGroup[] = [...groupMap.entries()]
      .map(([name, publicID]) => ({ publicID, name }))
      .sort((a, b) => a.name.localeCompare(b.name))
    const withQuality = list.filter(
      (item) => typeof item.sample_count === 'number'
    )
    out.set(model, {
      sourceCount: list.length,
      groups,
      // 只有确实拿过质量字段、且全部样本不足时才为真（不拿"没数据"冒充"质量差"）
      allInsufficient:
        withQuality.length > 0 &&
        withQuality.every((item) => item.insufficient === true),
      canonicalModelID: list.find((item) => item.canonical_model_id)
        ?.canonical_model_id,
    })
  }
  return out
}

/** 分享分组的可选项（供篩选使用）；空值表示"全部"。 */
export function shareGroupOptions(items: PlazaOfferingItem[]): string[] {
  return [
    ...new Set(
      items.map((item) => item.group_name ?? '').filter((name) => name !== '')
    ),
  ].sort()
}
