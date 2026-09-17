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
*/import { useQuery } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { formatMicroUSD } from '@/features/plaza/lib/format'

import { getUsage } from './api'
import { aggregateUsageByModel, describeUsageSource, type UsageByModel } from './lib/aggregate'
import type { UsageEntry } from './types'

const PAGE_SIZE = 50

/** 我的用量：按模型汇总 + 最近请求（含路由结果）。 */
export function MyUsage() {
  const { t } = useTranslation()

  const query = useQuery({
    queryKey: ['apigx-usage', PAGE_SIZE],
    queryFn: () => getUsage({ page: 1, page_size: PAGE_SIZE }),
  })

  const entries = useMemo(() => query.data?.data?.items ?? [], [query.data])
  const byModel = useMemo(() => aggregateUsageByModel(entries), [entries])

  if (query.isPending) {
    return <LoadingState />
  }
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }

  const modelColumns: StaticDataTableColumn<UsageByModel>[] = [
    { id: 'model', header: t('usage.model'), cell: (row) => row.model },
    { id: 'requests', header: t('usage.requests'), cell: (row) => String(row.requests) },
    { id: 'tokens', header: t('usage.tokens'), cell: (row) => String(row.total_tokens) },
    { id: 'cost', header: t('usage.cost'), cell: (row) => formatMicroUSD(row.cost_micro_usd) },
  ]

  const requestColumns: StaticDataTableColumn<UsageEntry>[] = [
    { id: 'time', header: t('usage.time'), cell: (row) => dayjs.unix(row.created_at).format('YYYY-MM-DD HH:mm:ss') },
    { id: 'model', header: t('usage.model'), cell: (row) => row.model },
    { id: 'tokens', header: t('usage.tokens'), cell: (row) => String(row.total_tokens) },
    { id: 'cost', header: t('usage.cost'), cell: (row) => formatMicroUSD(row.cost_micro_usd) },
    { id: 'source', header: t('usage.source'), cell: (row) => describeUsageSource(row) },
    { id: 'request_id', header: t('usage.requestId'), cell: (row) => row.request_id },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('usage.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {entries.length === 0 ? (
          <EmptyState title={t('usage.empty')} />
        ) : (
          <div className='space-y-6'>
            <Card>
              <CardHeader>
                <CardTitle>{t('usage.byModel')}</CardTitle>
              </CardHeader>
              <CardContent>
                <StaticDataTable columns={modelColumns} data={byModel} getRowKey={(row) => row.model} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>{t('usage.recentRequests')}</CardTitle>
              </CardHeader>
              <CardContent>
                <StaticDataTable columns={requestColumns} data={entries} getRowKey={(row) => row.request_id} />
              </CardContent>
            </Card>
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}