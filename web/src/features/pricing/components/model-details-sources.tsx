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

import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'

import { useApigxModelSources } from '../hooks/use-apigx-model-sources'
import type { PricingModel } from '../types'

/**
 * 模型详情的「分享来源」区块（§4 第 2 条要求）。
 *
 * 数据来自我们的广场（按模型名聚合）—— 展示**谁在提供这个模型**、属于哪些分享分组、质量是否可判定。
 * 没有数据时**整块不渲染**（公开页访客拿不到数据的情形，不留空壳）。
 */
export function ModelDetailsSources(props: { model: PricingModel }) {
  const { t } = useTranslation()
  const { byModel } = useApigxModelSources()

  const summary = byModel.get(props.model.model_name || '')
  if (!summary || summary.groups.length === 0) {
    return null
  }

  const columns: StaticDataTableColumn<{ publicID: string; name: string }>[] = [
    {
      id: 'group',
      header: t('Share group'),
      cell: (row) => (
        <Link
          to='/plaza/group/$groupPublicId'
          params={{ groupPublicId: row.publicID }}
          className='hover:underline'
        >
          {row.name}
        </Link>
      ),
    },
    {
      id: 'sources',
      header: t('Sources'),
      cell: () => String(summary.sourceCount),
    },
    {
      id: 'quality',
      header: t('Quality'),
      cell: () => (summary.allInsufficient ? t('Not enough samples') : t('Available')),
    },
  ]

  return (
    <section>
      <h3 className='mb-2 text-sm font-semibold'>{t('Share sources')}</h3>
      <StaticDataTable
        columns={columns}
        data={summary.groups}
        getRowKey={(row) => row.publicID}
      />
    </section>
  )
}
