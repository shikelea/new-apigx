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

import {
  aggregateResourceModels,
  shareGroupOptions,
  summarizeResourceQuality,
  summarizeModelSources,
} from '../lib/apigx-sources'

const item = (
  over: Partial<PlazaOfferingItem> & { model: string }
): PlazaOfferingItem =>
  ({ offering_public_id: `${over.model}-o`, ...over }) as PlazaOfferingItem

describe('模型广场：分享来源聚合', () => {
  it('同一模型的多个来源被汇总，分组按名称去重排序并带上对外 ID', () => {
    const map = summarizeModelSources([
      item({
        model: 'm1',
        channel_public_id: 'c1',
        group_name: 'B组',
        group_public_id: 'gb',
      }),
      item({
        model: 'm1',
        channel_public_id: 'c2',
        group_name: 'A组',
        group_public_id: 'ga',
      }),
      item({
        model: 'm1',
        channel_public_id: 'c3',
        group_name: 'B组',
        group_public_id: 'gb',
      }),
      item({
        model: 'm2',
        channel_public_id: 'c4',
        group_name: 'A组',
        group_public_id: 'ga',
      }),
    ])
    expect(map.get('m1')?.sourceCount).toBe(3)
    expect(map.get('m1')?.groups).toEqual([
      { publicID: 'ga', name: 'A组' },
      { publicID: 'gb', name: 'B组' },
    ])
    expect(map.get('m2')?.sourceCount).toBe(1)
  })

  it('空模型名被忽略（不产生无意义的条目）', () => {
    const map = summarizeModelSources([
      item({ model: '' }),
      item({ model: 'm1' }),
    ])
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
      item({ model: 'm1', channel_public_id: 'c1' }),
      item({
        model: 'm1',
        channel_public_id: 'c2',
        canonical_model_id: 'standard-1',
      }),
    ])
    expect(map.get('m1')?.canonicalModelID).toBe('standard-1')
  })

  it('同一渠道映射到同一标准模型时只保留质量证据更完整的来源', () => {
    const models = aggregateResourceModels(
      [
        item({
          model: 'standard-m1',
          access_kind: 'public',
          channel_public_id: 'same-channel',
          offering_public_id: 'older-alias',
          sample_count: 0,
          insufficient: true,
          updated_at: 20,
        }),
        item({
          model: 'standard-m1',
          access_kind: 'public',
          channel_public_id: 'same-channel',
          offering_public_id: 'measured-alias',
          sample_count: 12,
          insufficient: false,
          latency_ms_p50: 180,
          output_tokens_per_second: 42,
          reliability_lower_bound_ppm: 970_000,
          updated_at: 10,
        }),
      ],
      'public'
    )

    expect(models).toHaveLength(1)
    expect(models[0]?.sources).toHaveLength(1)
    expect(models[0]?.sources[0]?.offering_public_id).toBe('measured-alias')
    expect(models[0]?.quality).toMatchObject({
      lowestLatencyMsP50: 180,
      highestOutputTokensPerSecond: 42,
      bestReliabilityLowerBoundPpm: 970_000,
    })
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

  it('按公开、私有、分享给我筛选，并按模型 ID 聚合来源', () => {
    const items = [
      item({ model: 'm2', access_kind: 'public', channel_public_id: 'c2' }),
      item({
        model: 'm1',
        access_kind: 'public',
        channel_public_id: 'c1',
        model_description: '公开模型',
      }),
      item({ model: 'm1', access_kind: 'public', channel_public_id: 'c3' }),
      item({ model: 'm1', access_kind: 'private', channel_public_id: 'c4' }),
      item({ model: 'm3', access_kind: 'shared', channel_public_id: 'c5' }),
    ]

    const publicModels = aggregateResourceModels(items, 'public')
    expect(publicModels.map((model) => model.modelID)).toEqual(['m1', 'm2'])
    expect(publicModels[0]?.sources).toHaveLength(2)
    expect(publicModels[0]?.description).toBe('公开模型')
    expect(aggregateResourceModels(items, 'private')).toHaveLength(1)
    expect(aggregateResourceModels(items, 'shared')[0]?.modelID).toBe('m3')
  })

  it('为模型卡选择当前可访问来源中的最佳已知质量指标', () => {
    const quality = summarizeResourceQuality([
      item({
        model: 'm1',
        sample_count: 30,
        reliability_lower_bound_ppm: 940_000,
        latency_ms_p50: 120,
        output_tokens_per_second: 32,
        insufficient: false,
      }),
      item({
        model: 'm1',
        sample_count: 40,
        reliability_lower_bound_ppm: 980_000,
        latency_ms_p50: 180,
        insufficient: false,
      }),
      item({
        model: 'm1',
        sample_count: 3,
        reliability_lower_bound_ppm: 999_000,
        latency_ms_p50: 80,
        output_tokens_per_second: 55,
        insufficient: true,
      }),
    ])

    expect(quality).toEqual({
      allInsufficient: false,
      bestReliabilityLowerBoundPpm: 980_000,
      lowestLatencyMsP50: 80,
      highestOutputTokensPerSecond: 55,
    })
  })

  it('全部来源样本不足时明确标记，但保留已知延迟和吞吐', () => {
    const quality = summarizeResourceQuality([
      item({
        model: 'm1',
        sample_count: 2,
        reliability_lower_bound_ppm: 300_000,
        latency_ms_p50: 90,
        output_tokens_per_second: 12,
        insufficient: true,
      }),
      item({
        model: 'm1',
        sample_count: 0,
        insufficient: true,
      }),
    ])

    expect(quality.allInsufficient).toBe(true)
    expect(quality.bestReliabilityLowerBoundPpm).toBeUndefined()
    expect(quality.lowestLatencyMsP50).toBe(90)
    expect(quality.highestOutputTokensPerSecond).toBe(12)
  })

  it('部分或全部来源吞吐未知时不会把未知值聚合成 0', () => {
    const partial = summarizeResourceQuality([
      item({ model: 'm1' }),
      item({ model: 'm1', output_tokens_per_second: 18 }),
    ])
    expect(partial.highestOutputTokensPerSecond).toBe(18)

    const unknown = summarizeResourceQuality([
      item({ model: 'm1' }),
      item({ model: 'm1', output_tokens_per_second: 0 }),
    ])
    expect(unknown.highestOutputTokensPerSecond).toBeUndefined()
  })
})
