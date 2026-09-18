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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { getUsageSummary, type UsageSummaryItem } from '../api'

/**
 * 分享者视角的消耗（U-3，计划书 §4「Key 池策略及单 Key 消耗」+ 分享者维度统计）。
 *
 * 三个维度都看自己的量：按渠道 / 按上游 Key / 按模型。
 * 名称为空时显示「未记录」—— 确有 attempt 未记录 key/model（异步任务创建等路径），
 * 这是数据事实，界面上不隐藏、也不编一个名字。
 */
export function UsageSummaryCard() {
  const { t } = useTranslation()
  const [groupBy, setGroupBy] = useState<'channel' | 'key' | 'model'>('channel')

  const query = useQuery({
    queryKey: ['apigx-usage-summary', groupBy],
    queryFn: () => getUsageSummary(groupBy),
  })

  const items = query.data?.data?.items ?? []

  const label = (item: UsageSummaryItem) =>
    item.name !== '' ? item.name : t('Not recorded')

  const avgTtft = (item: UsageSummaryItem) =>
    item.ttft_samples > 0 ? Math.round(item.ttft_ms_sum / item.ttft_samples) : 0

  const columns: StaticDataTableColumn<UsageSummaryItem>[] = [
    { id: 'name', header: t('Source'), cell: (row) => label(row) },
    { id: 'attempts', header: t('Requests'), cell: (row) => String(row.attempts) },
    { id: 'success', header: t('Succeeded'), cell: (row) => String(row.success) },
    { id: 'failed', header: t('Failed'), cell: (row) => String(row.failed) },
    { id: 'unfinished', header: t('Unfinished'), cell: (row) => String(row.unfinished) },
    { id: 'tokens', header: t('Output tokens'), cell: (row) => String(row.output_tokens) },
    { id: 'ttft', header: t('Avg first token (ms)'), cell: (row) => String(avgTtft(row)) },
  ]

  const GROUP_BY: { key: typeof groupBy; labelKey: string }[] = [
    { key: 'channel', labelKey: 'By channel' },
    { key: 'key', labelKey: 'By key' },
    { key: 'model', labelKey: 'By model' },
  ]

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('My consumption')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t('How much your shared channels were used. Amounts are not shown here yet.')}
        </p>
        <div className='flex gap-2'>
          {GROUP_BY.map((item) => (
            <Button
              key={item.key}
              variant={groupBy === item.key ? 'default' : 'outline'}
              size='sm'
              onClick={() => setGroupBy(item.key)}
            >
              {t(item.labelKey)}
            </Button>
          ))}
        </div>
        {items.length === 0 ? (
          <EmptyState title={t('No consumption recorded yet')} />
        ) : (
          <StaticDataTable columns={columns} data={items} getRowKey={(row) => String(row.group_id ?? 0) + row.name} />
        )}
      </CardContent>
    </Card>
  )
}
