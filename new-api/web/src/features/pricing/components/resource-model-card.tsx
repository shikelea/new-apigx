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
import { ArrowUpRight, Boxes } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card'
import {
  formatLatency,
  formatThroughput,
} from '@/features/performance-metrics/lib/format'

import type { ResourceModelSummary } from '../lib/apigx-sources'

export function ResourceModelCard(props: {
  model: ResourceModelSummary
  onOpen: () => void
}) {
  const { t } = useTranslation()
  const reliability = props.model.quality.bestReliabilityLowerBoundPpm
  let reliabilityText = '—'
  if (reliability !== undefined) {
    reliabilityText = `${(reliability / 10_000).toFixed(2)}%`
  } else if (props.model.quality.allInsufficient) {
    reliabilityText = t('Not enough samples')
  }

  return (
    <Card className='group hover:border-foreground/20 flex min-h-72 flex-col transition-colors'>
      <CardHeader className='flex flex-col gap-4'>
        <div className='flex items-start justify-between gap-3'>
          <div className='bg-muted flex size-11 shrink-0 items-center justify-center rounded-xl font-mono text-sm font-semibold'>
            {props.model.modelID.slice(0, 2).toUpperCase()}
          </div>
          <Badge variant='secondary' className='gap-1.5'>
            <span className='bg-success size-1.5 rounded-full' aria-hidden />
            {t('Available')}
          </Badge>
        </div>
        <div className='space-y-1.5'>
          <h2 className='line-clamp-2 font-mono text-base font-semibold tracking-tight'>
            {props.model.modelID}
          </h2>
          {props.model.canonicalModelID &&
          props.model.canonicalModelID !== props.model.modelID ? (
            <p className='text-muted-foreground text-xs'>
              {t('Canonical model')}: {props.model.canonicalModelID}
            </p>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className='flex flex-1 flex-col gap-4'>
        <p className='text-muted-foreground line-clamp-3 text-sm leading-relaxed'>
          {props.model.description || t('No model description')}
        </p>
        <dl className='mt-auto grid grid-cols-3 gap-2'>
          <QualityMetric
            label={t('Reliability (6h)')}
            value={reliabilityText}
          />
          <QualityMetric
            label={t('plaza.detail.latency')}
            value={formatLatency(props.model.quality.lowestLatencyMsP50 ?? 0)}
          />
          <QualityMetric
            label={t('Throughput short')}
            value={formatThroughput(
              props.model.quality.highestOutputTokensPerSecond ?? 0
            ).replace(' t/s', 't/s')}
          />
        </dl>
      </CardContent>
      <CardFooter className='flex items-center justify-between border-t pt-4'>
        <span className='text-muted-foreground flex items-center gap-1.5 text-sm'>
          <Boxes className='size-4' aria-hidden />
          {t('{{count}} available sources', {
            count: props.model.sources.length,
          })}
        </span>
        <Button variant='ghost' size='sm' onClick={props.onOpen}>
          {t('View details')}
          <ArrowUpRight data-icon='inline-end' />
        </Button>
      </CardFooter>
    </Card>
  )
}

function QualityMetric(props: { label: string; value: string }) {
  return (
    <div className='bg-muted/30 min-w-0 rounded-md border p-2'>
      <dt className='text-muted-foreground truncate text-[11px]'>
        {props.label}
      </dt>
      <dd className='mt-1 truncate font-mono text-xs font-medium tabular-nums'>
        {props.value}
      </dd>
    </div>
  )
}
