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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getPlaza } from '@/features/plaza/api'

import {
  shareGroupOptions,
  summarizeModelSources,
  type ModelSourcesSummary,
} from '../lib/apigx-sources'

/**
 * 拉取"分享来源"数据（我们的广场），供底座模型广场展示与筛选。
 *
 * 注意：`/pricing` 是**公开页**，未登录访客调用我们的广场接口会拿到 401。
 * 这里**静默降级**为"没有分享来源数据" ⇒ 界面照常工作，只是不显示这一块
 * （不弹错误、不显示空壳文案 —— 没有数据就不显示，避免无意义的东西）。
 */
export function useApigxModelSources() {
  const query = useQuery({
    queryKey: ['apigx-plaza', 'for-pricing'],
    queryFn: async () => {
      const result = await getPlaza({ page: 1, page_size: 100 })
      return result.data?.items ?? []
    },
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  const items = useMemo(() => query.data ?? [], [query.data])

  return {
    isLoading: query.isLoading,
    /** 模型名 -> 分享来源汇总 */
    byModel: useMemo<Map<string, ModelSourcesSummary>>(
      () => summarizeModelSources(items),
      [items]
    ),
    /** 分享分组可选项 */
    groupOptions: useMemo<string[]>(() => shareGroupOptions(items), [items]),
    /** 是否拿到了数据（用于决定"显示不显示"这一块） */
    hasData: items.length > 0,
    /** 当前用户可见的原始来源，供模型详情比较具体渠道。 */
    items,
  }
}
