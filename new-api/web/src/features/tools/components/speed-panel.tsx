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
import { zodResolver } from '@hookform/resolvers/zod'
import { Gauge, Loader2 } from 'lucide-react'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import type { ProbeTargetInput, RelayOption, SpeedReport } from '../api'
import {
  PROBE_TARGET_DEFAULTS,
  probeTargetSchema,
  toProbeTarget,
  type ProbeTargetValues,
} from '../lib/probe-target'
import { ProbeTargetFields } from './probe-target-fields'

export function SpeedForm({
  relays,
  pending,
  onSubmit,
}: {
  relays: RelayOption[]
  pending: boolean
  onSubmit: (input: ProbeTargetInput) => void
}) {
  const { t } = useTranslation()
  const schema = useMemo(() => probeTargetSchema(t), [t])
  const form = useForm<ProbeTargetValues>({
    resolver: zodResolver(schema),
    defaultValues: PROBE_TARGET_DEFAULTS,
  })
  const source = form.watch('source')

  return (
    <Form {...form}>
      <form
        noValidate
        className='bg-muted/20 grid gap-3 rounded-lg border p-3'
        onSubmit={form.handleSubmit((values) =>
          onSubmit(toProbeTarget(values))
        )}
      >
        <ProbeTargetFields
          control={form.control}
          source={source}
          relays={relays}
        />
        <Button type='submit' disabled={pending}>
          {pending ? <Loader2 className='animate-spin' /> : <Gauge />}
          {pending ? t('Measuring, about 30 seconds') : t('Run speed test')}
        </Button>
      </form>
    </Form>
  )
}

/** A single headline metric with its spread underneath. */
function Metric({
  label,
  value,
  spread,
}: {
  label: string
  value: string
  spread: string
}) {
  return (
    <div className='rounded-lg border p-3'>
      <p className='text-muted-foreground text-xs'>{label}</p>
      <p className='mt-1 text-2xl font-semibold tabular-nums'>{value}</p>
      <p className='text-muted-foreground mt-0.5 text-xs'>{spread}</p>
    </div>
  )
}

export function SpeedResult({ report }: { report: SpeedReport }) {
  const { t } = useTranslation()
  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-2'>
        <Badge variant='secondary'>{report.model}</Badge>
        <Badge variant='outline'>
          {t('{{count}} samples', { count: report.samples.length })}
        </Badge>
        {/* A relay that buffers the whole answer and replays it as one chunk
            makes TTFT meaningless, so say so instead of showing a fast number. */}
        {report.pseudostream ? (
          <Badge variant='destructive'>{t('Pseudo-streaming')}</Badge>
        ) : null}
        {report.tokens_exact ? null : (
          <Badge variant='outline'>{t('Token count estimated')}</Badge>
        )}
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <Metric
          label={t('First token latency (median)')}
          value={`${report.ttft_ms} ms`}
          spread={t('Spread {{value}} ms', { value: report.ttft_spread_ms })}
        />
        <Metric
          label={t('Output speed (median)')}
          value={`${report.output_tps.toFixed(1)} tok/s`}
          spread={t('Spread {{value}} tok/s', {
            value: report.tps_spread.toFixed(1),
          })}
        />
      </div>
      {report.note ? (
        <p className='text-muted-foreground text-xs leading-5'>{report.note}</p>
      ) : null}
      {report.failed_samples > 0 ? (
        <p className='text-muted-foreground text-xs leading-5'>
          {t('{{count}} samples failed and were excluded', {
            count: report.failed_samples,
          })}
        </p>
      ) : null}
      {report.disclaimer ? (
        <p className='text-muted-foreground text-xs leading-5'>
          {report.disclaimer}
        </p>
      ) : null}
    </div>
  )
}
