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

import { StaggerContainer, StaggerItem } from '@/components/page-transition'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useSummaryCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import type { QuotaDataItem } from '@/features/dashboard/types'
import { useStatus } from '@/hooks/use-status'
import { getCurrencyLabel, isCurrencyDisplayEnabled } from '@/lib/currency'
import { formatNumber, formatQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { computeTimeRange } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

import { StatCard } from '../ui/stat-card'

const SUMMARY_SPARKLINE_BUCKETS = 12

function buildUsageSparklines(
  data: QuotaDataItem[],
  start: number,
  end: number
): { usage: number[]; requests: number[] } {
  const usage = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)
  const requests = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)
  for (const item of data) {
    const timestamp = Number(item.created_at) || start
    const ratio = end <= start ? 0 : (timestamp - start) / (end - start)
    const index = Math.min(
      SUMMARY_SPARKLINE_BUCKETS - 1,
      Math.max(0, Math.floor(ratio * SUMMARY_SPARKLINE_BUCKETS))
    )
    usage[index] += Number(item.quota) || 0
    requests[index] += Number(item.count) || 0
  }
  return { usage, requests }
}

export function SummaryCards() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const { status, loading } = useStatus()
  const timeRange = useMemo(() => computeTimeRange(1), [])
  const usedQuota = Number(user?.used_quota ?? 0)
  const requestCount = Number(user?.request_count ?? 0)

  const usageQuery = useQuery({
    queryKey: [
      'dashboard',
      'overview',
      'summary-sparklines',
      timeRange.start_timestamp,
      timeRange.end_timestamp,
    ],
    queryFn: async () =>
      requireServerSuccess(
        await getUserQuotaDates({
          start_timestamp: timeRange.start_timestamp,
          end_timestamp: timeRange.end_timestamp,
          default_time: 'hour',
        })
      ),
    staleTime: 60 * 1000,
  })
  const data = useMemo(
    () => usageQuery.data?.data ?? [],
    [usageQuery.data?.data]
  )
  const recentUsage = useMemo(
    () => data.reduce((total, item) => total + (Number(item.quota) || 0), 0),
    [data]
  )
  const sparklines = useMemo(
    () =>
      buildUsageSparklines(
        data,
        timeRange.start_timestamp,
        timeRange.end_timestamp
      ),
    [data, timeRange.end_timestamp, timeRange.start_timestamp]
  )
  const currencyEnabledFromStore = isCurrencyDisplayEnabled()
  const currencyEnabled =
    typeof status?.display_in_currency === 'boolean'
      ? status.display_in_currency
      : currencyEnabledFromStore
  const currencyLabel = currencyEnabled ? getCurrencyLabel() : 'Tokens'
  const items = useSummaryCardsConfig({
    todayUsageDisplay: formatQuota(recentUsage),
    usedDisplay: formatQuota(usedQuota),
    requestCountDisplay: formatNumber(requestCount),
    currencyEnabled,
    currencyLabel,
  }).map((config, index) => ({
    ...config,
    tone: (['accent-1', 'accent-2', 'accent-3'] as const)[index],
    sparkline:
      config.key === 'requests' ? sparklines.requests : sparklines.usage,
  }))

  return (
    <section className='bg-card rounded-2xl border p-3 shadow-xs sm:p-5'>
      <div>
        <h2 className='text-sm font-semibold sm:text-base'>
          {t('Usage at a glance')}
        </h2>
        <p className='text-muted-foreground mt-1 text-xs sm:text-sm'>
          {t('Monitor requests, actual usage, and estimated cost.')}
        </p>
      </div>
      <StaggerContainer className='mt-3 grid grid-cols-1 gap-2 sm:grid-cols-3 sm:gap-3'>
        {items.map((item) => (
          <StaggerItem
            key={item.key}
            className='bg-background/60 rounded-xl border p-3'
          >
            <StatCard
              title={item.title}
              value={item.value}
              description={item.description}
              icon={item.icon}
              tone={item.tone}
              sparkline={item.sparkline}
              sparklineVariant='line'
              loading={loading || usageQuery.isPending}
              compactMobile
            />
          </StaggerItem>
        ))}
      </StaggerContainer>
    </section>
  )
}
