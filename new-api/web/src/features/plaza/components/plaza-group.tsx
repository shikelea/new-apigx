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
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { getPlaza } from '../api'
import { describePlazaQuality } from '../lib/format'
import type { PlazaOfferingItem } from '../types'

/**
 * 分享分组页（**访客视角**，计划书 §4：分享者、分享名称、介绍、当前用户有权看见的资源）。
 *
 * 数据来自广场：广场只会返回**当前用户有权看见**的供给（已发布 + 可见性判定），
 * 因此这里按分组过滤即可，不需要也不应该另开一条能绕过授权的查询。
 */
export function PlazaGroup(props: { groupPublicID: string }) {
  const { t } = useTranslation()

  const query = useQuery({
    queryKey: ['apigx-plaza'],
    queryFn: () => getPlaza({ page: 1, page_size: 100 }),
  })

  const items = useMemo(
    () =>
      (query.data?.data?.items ?? []).filter(
        (item) => item.group_public_id === props.groupPublicID
      ),
    [query.data, props.groupPublicID]
  )

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

  const columns: StaticDataTableColumn<PlazaOfferingItem>[] = [
    { id: 'model', header: t('Model'), cell: (row) => row.model },
    {
      id: 'channel',
      header: t('Channel'),
      cell: (row) => row.channel_public_id,
    },
    {
      id: 'quality',
      header: t('Quality'),
      cell: (row) => describePlazaQuality(row),
    },
    {
      id: 'capabilities',
      header: t('Capabilities'),
      cell: (row) =>
        Object.entries(row.capabilities ?? {})
          .filter(([, enabled]) => enabled)
          .map(([capability]) => capability)
          .join(', '),
    },
  ]

  const first = items[0]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {first?.group_name || t('Share group')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <Card>
            <CardHeader>
              <CardTitle>{t('Share group')}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-2 text-sm'>
              {/* 介绍是不可信文本：按纯文本渲染 */}
              {first?.group_description ? (
                <p className='text-muted-foreground whitespace-pre-wrap'>
                  {first.group_description}
                </p>
              ) : null}
              <div className='text-muted-foreground'>
                {t('Provider')}: {first?.owner_user_id ?? '-'}
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('Visible resources')}</CardTitle>
            </CardHeader>
            <CardContent>
              {items.length === 0 ? (
                <EmptyState title={t('No visible resource in this group')} />
              ) : (
                <StaticDataTable
                  columns={columns}
                  data={items}
                  getRowKey={(row) => row.offering_public_id}
                />
              )}
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
