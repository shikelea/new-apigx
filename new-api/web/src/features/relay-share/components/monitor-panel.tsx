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
import { ChevronDown, ChevronUp } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  availabilityRate,
  checkStatus,
  formatEpoch,
} from '@/features/tools/lib/format'

import { listRelaySamples, relayQueryKeys, type RelayProbeSample } from '../api'

// Monitoring history for one relay. It loads only when opened: most visitors
// scroll past a card without caring, and a list page would otherwise fire one
// request per card.
export function MonitorPanel({ publicID }: { publicID: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const samples = useQuery({
    queryKey: relayQueryKeys.samples(publicID),
    queryFn: () => listRelaySamples(publicID),
    enabled: open,
  })
  const items = samples.data?.data?.items ?? []

  return (
    <div className='space-y-2 rounded-lg border p-3'>
      <Button
        size='sm'
        variant='ghost'
        className='h-auto w-full justify-between px-1 py-0 text-xs'
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        {t('Monitoring history')}
        {open ? (
          <ChevronUp className='size-3.5' />
        ) : (
          <ChevronDown className='size-3.5' />
        )}
      </Button>
      {open ? <MonitorBody pending={samples.isPending} items={items} /> : null}
    </div>
  )
}

function MonitorBody({
  pending,
  items,
}: {
  pending: boolean
  items: RelayProbeSample[]
}) {
  const { t } = useTranslation()
  if (pending) return <Skeleton className='h-16 w-full' />
  if (items.length === 0) {
    return (
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'No monitoring results yet. The owner may have monitoring off, or the first run has not happened.'
        )}
      </p>
    )
  }
  return <MonitorSummary items={items} />
}

function MonitorSummary({ items }: { items: RelayProbeSample[] }) {
  const { t } = useTranslation()
  const availability = items.filter((item) => item.kind === 'availability')
  const rate = availabilityRate(availability)
  // Degrade and dilution are grouped per model: one relay can serve GPT fine
  // while another model is substituted, and a single blended number hides that.
  const byModel = new Map<string, RelayProbeSample>()
  for (const item of items) {
    if (item.kind === 'availability') continue
    const composite = `${item.kind}:${item.model}`
    if (!byModel.has(composite)) byModel.set(composite, item)
  }

  return (
    <div className='space-y-2'>
      {rate === null ? null : (
        <div className='flex items-center justify-between text-xs'>
          <span className='text-muted-foreground'>
            {t('Availability over {{count}} checks', {
              count: availability.length,
            })}
          </span>
          <span className='font-medium tabular-nums'>{rate}%</span>
        </div>
      )}
      {[...byModel.entries()].map(([composite, sample]) => {
        const tone = checkStatus(sample.status)
        return (
          <div key={composite} className='space-y-0.5 text-xs'>
            <div className='flex flex-wrap items-center gap-2'>
              <Badge variant='outline'>
                {sample.kind === 'degrade'
                  ? t('Degradation')
                  : t('Model substitution')}
              </Badge>
              <span className='font-medium'>{sample.model}</span>
              <Badge variant={tone.variant}>{t(tone.label)}</Badge>
              <span className='text-muted-foreground tabular-nums'>
                {sample.score}/100
              </span>
              <span className='text-muted-foreground'>
                {formatEpoch(sample.probed_at)}
              </span>
            </div>
            <p className='text-muted-foreground leading-5'>{sample.evidence}</p>
          </div>
        )
      })}
    </div>
  )
}
