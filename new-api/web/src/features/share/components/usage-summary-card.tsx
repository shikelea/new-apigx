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
import { ChevronLeft, ChevronRight, RefreshCw } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import {
  getUsageSummary,
  type UsageSummaryItem,
  type UsageSummaryQuantity,
} from '../api'

type UsageGroupBy = 'channel' | 'key' | 'model'

const PAGE_SIZE = 10
const WINDOW_OPTIONS = [7, 30, 90] as const

const CAPABILITY_LABELS: Record<number, string> = {
  1: 'Chat',
  2: 'Embeddings',
  3: 'Image generation',
  4: 'Image editing',
  5: 'Speech recognition',
  6: 'Speech generation',
  7: 'Rerank',
  8: 'Video',
}

const DIMENSION_LABELS: Record<number, string> = {
  0: 'Input tokens',
  1: 'Output tokens',
  2: 'Cache read tokens',
  3: 'Cache write tokens',
  4: 'Reasoning tokens',
  5: 'Images',
  6: 'Input audio',
  7: 'Output audio',
  8: 'Output video',
  9: 'Requests',
  10: 'Input characters',
  11: 'Documents',
}

/**
 * 分享者只在「我的分享」里看消耗：请求/重试来自 Attempt，实际数量与成本来自 UsageEvent。
 * 布局：筛选条 → 一张可横向滚动的明细表 → 简单上一页/下一页；移动端不复制第二套卡片。
 */
export function UsageSummaryCard() {
  const { t } = useTranslation()
  const [groupBy, setGroupBy] = useState<UsageGroupBy>('channel')
  const [windowDays, setWindowDays] =
    useState<(typeof WINDOW_OPTIONS)[number]>(30)
  const [page, setPage] = useState(1)

  const query = useQuery({
    queryKey: ['apigx-usage-summary', groupBy, windowDays, page],
    queryFn: () => {
      const end = Math.floor(Date.now() / 1000)
      return getUsageSummary(groupBy, {
        start: end - windowDays * 24 * 60 * 60,
        end,
        page,
        pageSize: PAGE_SIZE,
      })
    },
  })

  const items = query.data?.data?.items ?? []
  const hasMore = query.data?.data?.has_more ?? false

  const selectGroup = (next: UsageGroupBy) => {
    setGroupBy(next)
    setPage(1)
  }
  const selectWindow = (next: (typeof WINDOW_OPTIONS)[number]) => {
    setWindowDays(next)
    setPage(1)
  }

  const columns: StaticDataTableColumn<UsageSummaryItem>[] = [
    {
      id: 'name',
      header: t('Source'),
      className: 'min-w-36',
      cell: (row) => (
        <div className='font-medium'>{row.name || t('Not recorded')}</div>
      ),
    },
    {
      id: 'requests',
      header: t('Request activity'),
      className: 'min-w-36',
      cell: (row) => (
        <div className='tabular-nums'>
          <div className='font-medium'>
            {formatCount(row.request_count)} {t('requests')}
          </div>
          <div className='text-muted-foreground mt-0.5 text-xs'>
            {t('{{attempts}} attempts · {{retries}} retries', {
              attempts: formatCount(row.attempt_count),
              retries: formatCount(row.retry_count),
            })}
          </div>
        </div>
      ),
    },
    {
      id: 'outcomes',
      header: t('Outcomes'),
      className: 'min-w-36',
      cell: (row) => (
        <div className='flex flex-wrap gap-x-3 gap-y-1 text-xs tabular-nums'>
          <span className='text-emerald-600 dark:text-emerald-400'>
            {t('Succeeded')} {formatCount(row.succeeded_attempt_count)}
          </span>
          <span className='text-destructive'>
            {t('Failed')} {formatCount(row.failed_attempt_count)}
          </span>
          {row.unfinished_attempt_count > 0 && (
            <span className='text-muted-foreground'>
              {t('Unfinished')} {formatCount(row.unfinished_attempt_count)}
            </span>
          )}
        </div>
      ),
    },
    {
      id: 'usage',
      header: t('Measured usage'),
      className: 'min-w-64',
      cell: (row) => <UsageBreakdown usage={row.usage} />,
    },
    {
      id: 'cost',
      header: t('Estimated cost (USD)'),
      className: 'min-w-52',
      cell: (row) => <CostBreakdown item={row} />,
    },
    {
      id: 'ttft',
      header: t('Avg first token (ms)'),
      className: 'min-w-28',
      cell: (row) => (
        <span className='tabular-nums'>
          {row.ttft_samples > 0
            ? formatCount(Math.round(row.ttft_ms_sum / row.ttft_samples))
            : '—'}
        </span>
      ),
    },
  ]

  const groupOptions: { key: UsageGroupBy; label: string }[] = [
    { key: 'channel', label: t('By channel') },
    { key: 'key', label: t('By key') },
    { key: 'model', label: t('By model') },
  ]

  let tableContent: ReactNode
  if (query.isPending) {
    tableContent = (
      <div className='text-muted-foreground flex min-h-28 items-center justify-center gap-2 text-sm'>
        <RefreshCw className='size-4 animate-spin' />
        {t('Loading...')}
      </div>
    )
  } else if (query.isError) {
    tableContent = (
      <div className='border-destructive/30 bg-destructive/5 flex min-h-28 flex-col items-center justify-center gap-3 rounded-lg border p-4 text-center'>
        <p className='text-destructive text-sm'>{t('Failed to load data')}</p>
        <Button
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (items.length === 0) {
    tableContent = (
      <EmptyState title={t('No consumption recorded for this period')} />
    )
  } else {
    tableContent = (
      <StaticDataTable
        columns={columns}
        data={items}
        getRowKey={(row) => row.group_key}
        className={query.isFetching ? 'opacity-70' : undefined}
      />
    )
  }

  return (
    <Card>
      <CardHeader className='gap-4'>
        <div>
          <CardTitle>{t('Shared resource usage')}</CardTitle>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Requests, measured usage, and estimated USD cost generated by your shared resources.'
            )}
          </p>
        </div>
        <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
          <div
            className='flex flex-wrap gap-2'
            aria-label={t('Group usage by')}
          >
            {groupOptions.map((option) => (
              <Button
                key={option.key}
                variant={groupBy === option.key ? 'default' : 'outline'}
                size='sm'
                onClick={() => selectGroup(option.key)}
              >
                {option.label}
              </Button>
            ))}
          </div>
          <div className='flex flex-wrap gap-2' aria-label={t('Time range')}>
            {WINDOW_OPTIONS.map((days) => (
              <Button
                key={days}
                variant={windowDays === days ? 'secondary' : 'ghost'}
                size='sm'
                onClick={() => selectWindow(days)}
              >
                {t('Last {{days}} days', { days })}
              </Button>
            ))}
          </div>
        </div>
      </CardHeader>
      <CardContent className='space-y-3'>
        {tableContent}

        {!query.isPending && !query.isError && (page > 1 || hasMore) && (
          <div className='flex items-center justify-between border-t pt-3'>
            <span className='text-muted-foreground text-sm'>
              {t('Page {{page}}', { page })}
            </span>
            <div className='flex gap-2'>
              <Button
                variant='outline'
                size='icon'
                className='size-9'
                disabled={page === 1 || query.isFetching}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
                aria-label={t('Previous page')}
              >
                <ChevronLeft className='size-4' />
              </Button>
              <Button
                variant='outline'
                size='icon'
                className='size-9'
                disabled={!hasMore || query.isFetching}
                onClick={() => setPage((current) => current + 1)}
                aria-label={t('Next page')}
              >
                <ChevronRight className='size-4' />
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function UsageBreakdown({ usage }: { usage: UsageSummaryQuantity[] }) {
  const { t } = useTranslation()
  if (usage.length === 0) {
    return (
      <span className='text-muted-foreground text-sm'>
        {t('No measured usage')}
      </span>
    )
  }

  return (
    <div className='space-y-1.5'>
      {usage.slice(0, 4).map((item) => (
        <div
          key={`${item.capability}:${item.dimension}`}
          className='flex items-baseline justify-between gap-3 text-xs'
        >
          <span className='text-muted-foreground'>
            {t(CAPABILITY_LABELS[item.capability] ?? 'Unknown')} ·{' '}
            {t(DIMENSION_LABELS[item.dimension] ?? 'Usage')}
          </span>
          <span className='font-medium whitespace-nowrap tabular-nums'>
            {formatMilliQuantity(item.quantity_milli)} {t(item.unit)}
          </span>
        </div>
      ))}
      {usage.some((item) => item.local_estimated_milli > 0) && (
        <Badge variant='outline'>{t('Includes estimates')}</Badge>
      )}
      {sumUnavailableMeasurements(usage) > 0 && (
        <Badge variant='warning'>
          {t('{{count}} unavailable measurements', {
            count: sumUnavailableMeasurements(usage),
          })}
        </Badge>
      )}
      {usage.length > 4 && (
        <div className='text-muted-foreground text-xs'>
          {t('{{count}} more measurements', { count: usage.length - 4 })}
        </div>
      )}
    </div>
  )
}

function CostBreakdown({ item }: { item: UsageSummaryItem }) {
  const { t } = useTranslation()
  const hasEstimated = item.estimated_cost_event_count > 0
  const hasOnlyExplicitZero =
    !hasEstimated &&
    item.explicit_zero_event_count > 0 &&
    item.unpriced_event_count === 0 &&
    item.pending_fx_event_count === 0

  return (
    <div className='space-y-2'>
      <div className='font-medium tabular-nums'>
        {hasEstimated || hasOnlyExplicitZero
          ? formatMicroUSD(item.estimated_cost_usd_micro)
          : '—'}
      </div>
      <div className='flex flex-wrap gap-1'>
        {item.unpriced_event_count > 0 && (
          <Badge variant='warning'>
            {t('{{count}} unpriced', { count: item.unpriced_event_count })}
          </Badge>
        )}
        {item.pending_fx_event_count > 0 && (
          <Badge variant='outline'>
            {t('{{count}} pending FX', { count: item.pending_fx_event_count })}
          </Badge>
        )}
        {item.explicit_zero_event_count > 0 && (
          <Badge variant='secondary'>
            {t('{{count}} explicitly free', {
              count: item.explicit_zero_event_count,
            })}
          </Badge>
        )}
      </div>
    </div>
  )
}

function formatCount(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(
    value
  )
}

function formatMilliQuantity(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 3 }).format(
    value / 1000
  )
}

function formatMicroUSD(value: number): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 6,
    maximumFractionDigits: 6,
  }).format(value / 1_000_000)
}

function sumUnavailableMeasurements(usage: UsageSummaryQuantity[]): number {
  return usage.reduce(
    (total, item) =>
      total + item.unknown_event_count + item.unavailable_event_count,
    0
  )
}
