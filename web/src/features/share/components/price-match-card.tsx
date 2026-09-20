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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Calculator, Check, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { handleServerError } from '@/lib/handle-server-error'

import { confirmChannelPriceMatch, previewChannelPriceMatch } from '../api'
import type { PriceMatchCandidate, PriceMatchDimension } from '../types'

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

const UNIT_LABELS: Record<number, string> = {
  0: '1M tokens',
  1: '1K tokens',
  2: 'image',
  3: 'second',
  4: 'minute',
  5: 'request',
  6: '1K characters',
  7: 'document',
}

export function PriceMatchCard(props: {
  channelId: number
  offeringCount: number
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [candidates, setCandidates] = useState<PriceMatchCandidate[] | null>(
    null
  )
  const [selected, setSelected] = useState<Set<number>>(new Set())

  const previewMutation = useMutation({
    mutationFn: () => previewChannelPriceMatch(props.channelId),
    onSuccess: (response) => {
      const next = response.data?.candidates ?? []
      setCandidates(next)
      setSelected(
        new Set(
          next
            .filter((item) => item.confirmable)
            .map((item) => item.offering_id)
        )
      )
    },
    onError: (error) => handleServerError(error),
  })

  const confirmMutation = useMutation({
    mutationFn: () => {
      const selections = (candidates ?? [])
        .filter(
          (candidate) =>
            selected.has(candidate.offering_id) && candidate.candidate_token
        )
        .map((candidate) => ({
          offering_id: candidate.offering_id,
          candidate_token: candidate.candidate_token ?? '',
        }))
      return confirmChannelPriceMatch(props.channelId, selections)
    },
    onSuccess: (response) => {
      toast.success(
        t('share.price.appliedCount', { count: response.data?.applied ?? 0 })
      )
      setCandidates(null)
      setSelected(new Set())
      void queryClient.invalidateQueries({
        queryKey: ['apigx-channel-offerings', props.channelId],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const confirmableCount =
    candidates?.filter((item) => item.confirmable).length ?? 0

  return (
    <Card id='price-match'>
      <CardHeader className='gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <div>
          <CardTitle className='flex items-center gap-2'>
            <Calculator className='size-4' />
            {t('share.price.title')}
          </CardTitle>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('share.price.description')}
          </p>
        </div>
        <Button
          variant='outline'
          size='sm'
          disabled={props.offeringCount === 0 || previewMutation.isPending}
          onClick={() => previewMutation.mutate()}
        >
          <RefreshCw
            className={previewMutation.isPending ? 'animate-spin' : undefined}
          />
          {t('share.price.preview')}
        </Button>
      </CardHeader>
      <CardContent className='space-y-4'>
        {props.offeringCount === 0 ? (
          <EmptyState title={t('share.price.noModels')} />
        ) : null}

        {candidates !== null && candidates.length === 0 ? (
          <EmptyState title={t('share.price.noCandidates')} />
        ) : null}

        {candidates && candidates.length > 0 ? (
          <>
            <Alert>
              <AlertTitle>{t('share.price.previewReady')}</AlertTitle>
              <AlertDescription>
                {t('share.price.previewSummary', {
                  matched: confirmableCount,
                  manual: candidates.length - confirmableCount,
                })}
              </AlertDescription>
            </Alert>
            <div className='grid gap-3 lg:grid-cols-2'>
              {candidates.map((candidate) => (
                <PriceCandidate
                  key={candidate.offering_id}
                  candidate={candidate}
                  checked={selected.has(candidate.offering_id)}
                  onCheckedChange={(checked) => {
                    setSelected((current) => {
                      const next = new Set(current)
                      if (checked) next.add(candidate.offering_id)
                      else next.delete(candidate.offering_id)
                      return next
                    })
                  }}
                />
              ))}
            </div>
            <div className='flex justify-end'>
              <Button
                disabled={selected.size === 0 || confirmMutation.isPending}
                onClick={() => confirmMutation.mutate()}
              >
                <Check />
                {t('share.price.confirmSelected', { count: selected.size })}
              </Button>
            </div>
          </>
        ) : null}
      </CardContent>
    </Card>
  )
}

function PriceCandidate(props: {
  candidate: PriceMatchCandidate
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  const { t } = useTranslation()
  const statusKey = `share.price.status.${props.candidate.status}`
  return (
    <article className='bg-muted/20 rounded-lg border p-4'>
      <div className='flex items-start gap-3'>
        <Checkbox
          checked={props.checked}
          disabled={!props.candidate.confirmable}
          onCheckedChange={(checked) => props.onCheckedChange(checked === true)}
          aria-label={t('share.price.selectModel', {
            model: props.candidate.upstream_model_id,
          })}
        />
        <div className='min-w-0 flex-1 space-y-2'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <code className='text-sm font-semibold break-all'>
              {props.candidate.upstream_model_id}
            </code>
            <Badge
              variant={props.candidate.confirmable ? 'default' : 'outline'}
            >
              {t(statusKey)}
            </Badge>
          </div>
          {props.candidate.matched_model_id &&
          props.candidate.matched_model_id !==
            props.candidate.upstream_model_id ? (
            <p className='text-muted-foreground text-xs'>
              {t('share.price.matchedAs', {
                model: props.candidate.matched_model_id,
              })}
            </p>
          ) : null}
          {(props.candidate.dimensions ?? []).length > 0 ? (
            <div className='space-y-1.5'>
              {(props.candidate.dimensions ?? []).map((dimension) => (
                <DimensionPrice
                  key={`${dimension.dimension}:${dimension.unit_scale}`}
                  dimension={dimension}
                />
              ))}
            </div>
          ) : null}
          {props.candidate.expression ? (
            <code className='bg-background block max-h-20 overflow-auto rounded-md border p-2 text-xs break-all'>
              {props.candidate.expression}
            </code>
          ) : null}
          {!props.candidate.confirmable ? (
            <p className='text-muted-foreground text-xs'>
              {t(
                `share.price.reason.${props.candidate.reason ?? props.candidate.status}`
              )}
            </p>
          ) : null}
        </div>
      </div>
    </article>
  )
}

function DimensionPrice(props: { dimension: PriceMatchDimension }) {
  const { t } = useTranslation()
  return (
    <div className='flex items-baseline justify-between gap-3 text-xs'>
      <span className='text-muted-foreground'>
        {t(DIMENSION_LABELS[props.dimension.dimension] ?? 'Usage')}
      </span>
      <span className='font-medium whitespace-nowrap tabular-nums'>
        {formatMicroUSD(props.dimension.usd_unit_price_micro)} /{' '}
        {t(UNIT_LABELS[props.dimension.unit_scale] ?? 'unit')}
      </span>
    </div>
  )
}

function formatMicroUSD(value: number): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 0,
    maximumFractionDigits: 6,
  }).format(value / 1_000_000)
}
