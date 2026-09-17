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
*/import type { UsageEntry } from '../types'

/** 按模型汇总后的一行。 */
export interface UsageByModel {
  model: string
  requests: number
  total_tokens: number
  cost_micro_usd: number
}

/**
 * 按模型汇总用量。
 *
 * 计划书要求「按请求**和模型**查看个人消耗及估算成本」，因此两份视图必须来自**同一份数据**：
 * 这里只做分组求和，不做任何补齐 —— 没有用量的模型不会凭空出现一行 0 ✓。
 * 成本直接累加后端给出的 `cost_micro_usd`（不在前端重新定价 ✓）。
 */
export function aggregateUsageByModel(entries: UsageEntry[]): UsageByModel[] {
  const byModel = new Map<string, UsageByModel>()
  for (const entry of entries) {
    const existing = byModel.get(entry.model)
    if (existing) {
      existing.requests += 1
      existing.total_tokens += entry.total_tokens
      existing.cost_micro_usd += entry.cost_micro_usd
      continue
    }
    byModel.set(entry.model, {
      model: entry.model,
      requests: 1,
      total_tokens: entry.total_tokens,
      cost_micro_usd: entry.cost_micro_usd,
    })
  }
  return [...byModel.values()].sort((a, b) => b.cost_micro_usd - a.cost_micro_usd)
}

/** 一条记录的路由结果描述：分享渠道（含对外 ID）或平台自有。 */
export function describeUsageSource(entry: UsageEntry): string {
  if (entry.source?.kind === 'share') {
    return entry.source.channel_public_id ?? ''
  }
  return entry.source?.kind ?? ''
}