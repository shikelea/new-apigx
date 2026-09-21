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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  ArrowLeft,
  CircleCheck,
  CircleDashed,
  Send,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'

import {
  deleteOffering,
  getChannel,
  getChannelKeys,
  getChannelOfferings,
  submitChannelOfferings,
  submitOffering,
} from '../api'
import {
  healthLabelKey,
  keyPoolModeLabelKey,
  offeringStatusLabelKey,
} from '../lib/labels'
import { normalizeOfferingCapabilities } from '../lib/offering-capabilities'
import type { Offering } from '../types'
import { ChannelActions } from './channel-actions'
import { KeyPoolCard } from './key-pool-card'
import { PriceMatchCard } from './price-match-card'

export function ChannelDetailView(props: { channelId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [deletingOffering, setDeletingOffering] = useState<Offering | null>(
    null
  )

  const submitMutation = useMutation({
    mutationFn: (offeringId: number) => submitOffering(offeringId),
    onSuccess: () => {
      toast.success(t('share.review.submitted'))
      void queryClient.invalidateQueries({
        queryKey: ['apigx-channel-offerings', props.channelId],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const submitAllMutation = useMutation({
    mutationFn: () => submitChannelOfferings(props.channelId),
    onSuccess: (response) => {
      const result = response.data
      const submitted = result?.submitted ?? 0
      const skipped = result?.skipped.length ?? 0
      const failed = result?.failed.length ?? 0
      const notify = failed > 0 ? toast.warning : toast.success
      notify(
        t('share.review.bulkResult', {
          submitted,
          skipped,
          failed,
        })
      )
      void queryClient.invalidateQueries({
        queryKey: ['apigx-channel-offerings', props.channelId],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const deleteOfferingMutation = useMutation({
    mutationFn: (offeringId: number) => deleteOffering(offeringId),
    onSuccess: () => {
      setDeletingOffering(null)
      toast.success(t('share.offering.deleted'))
      void queryClient.invalidateQueries({
        queryKey: ['apigx-channel-offerings', props.channelId],
      })
      void queryClient.invalidateQueries({ queryKey: ['apigx-plaza'] })
    },
    onError: (error) => handleServerError(error),
  })

  const channelQuery = useQuery({
    queryKey: ['apigx-channel', props.channelId],
    queryFn: () => getChannel(props.channelId),
  })
  const keysQuery = useQuery({
    queryKey: ['apigx-channel-keys', props.channelId],
    queryFn: () => getChannelKeys(props.channelId),
  })
  const offeringsQuery = useQuery({
    queryKey: ['apigx-channel-offerings', props.channelId],
    queryFn: () => getChannelOfferings(props.channelId),
  })

  if (
    channelQuery.isPending ||
    keysQuery.isPending ||
    offeringsQuery.isPending
  ) {
    return <LoadingState />
  }
  if (channelQuery.isError || keysQuery.isError || offeringsQuery.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void channelQuery.refetch()
          void keysQuery.refetch()
          void offeringsQuery.refetch()
        }}
      />
    )
  }

  const channel = channelQuery.data?.data
  const keys = keysQuery.data?.data?.items ?? []
  const offerings = offeringsQuery.data?.data?.items ?? []
  if (!channel) {
    return <EmptyState title={t('share.detail.missing')} />
  }

  const usableKeys = keys.filter((key) => key.usable).length
  const pendingOfferings = offerings.filter(
    (offering) => offering.status === 1
  ).length
  const publishedOfferings = offerings.filter(
    (offering) => offering.status === 2
  ).length
  const draftOfferings = offerings.filter(
    (offering) => offering.status === 0 || offering.status === 3
  ).length
  const readyToSubmit = offerings.filter(
    (offering) =>
      (offering.status === 0 || offering.status === 3) &&
      offering.price_configured
  ).length
  const ready = usableKeys > 0 && publishedOfferings > 0

  const offeringColumns: StaticDataTableColumn<Offering>[] = [
    {
      id: 'model',
      header: t('share.offering.model'),
      className: 'min-w-48',
      cell: (row) => (
        <code className='text-xs break-all'>{row.upstream_model_id}</code>
      ),
    },
    {
      id: 'status',
      header: t('share.offering.status'),
      cell: (row) => (
        <Badge variant={row.status === 2 ? 'default' : 'outline'}>
          {t(offeringStatusLabelKey(row.status))}
        </Badge>
      ),
    },
    {
      id: 'price',
      header: t('share.offering.price'),
      cell: (row) => (
        <Badge variant={row.price_configured ? 'default' : 'outline'}>
          {t(
            row.price_configured
              ? 'share.offering.priced'
              : 'share.offering.unpriced'
          )}
        </Badge>
      ),
    },
    {
      id: 'capabilities',
      header: t('share.offering.capabilities'),
      cell: (row) =>
        normalizeOfferingCapabilities(row.capabilities).join(', ') ||
        t('Not configured'),
    },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex justify-end gap-1'>
          <SubmitOfferingButton
            offering={row}
            pending={submitMutation.isPending}
            onSubmit={submitMutation.mutate}
          />
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('share.offering.delete')}
            onClick={() => setDeletingOffering(row)}
          >
            <Trash2 />
          </Button>
        </div>
      ),
    },
  ]

  let nextAction = t('share.readiness.waitReview')
  if (usableKeys === 0) {
    nextAction = t('share.readiness.addKey')
  } else if (offerings.length === 0) {
    nextAction = t('share.readiness.importModels')
  } else if (draftOfferings > 0) {
    nextAction = t('share.readiness.priceAndSubmit')
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <div className='flex min-w-0 items-center gap-3'>
          <Button
            variant='ghost'
            size='icon-sm'
            render={<Link to='/share' />}
            aria-label={t('Back')}
          >
            <ArrowLeft />
          </Button>
          <span className='truncate'>{channel.name}</span>
        </div>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <Alert
            className={ready ? undefined : 'border-warning/40 bg-warning/10'}
          >
            {ready ? <CircleCheck /> : <CircleDashed />}
            <AlertTitle>
              {ready
                ? t('share.readiness.ready')
                : t('share.readiness.incomplete')}
            </AlertTitle>
            <AlertDescription>
              {ready
                ? t('share.readiness.readyDescription', {
                    count: publishedOfferings,
                  })
                : t('share.readiness.nextAction', { action: nextAction })}
            </AlertDescription>
          </Alert>

          <Card>
            <CardHeader>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div>
                  <CardTitle>{t('share.channel.overview')}</CardTitle>
                  <p className='text-muted-foreground mt-1 text-sm break-all'>
                    {channel.base_url}
                  </p>
                </div>
                <Badge
                  variant={channel.health_state === 0 ? 'default' : 'outline'}
                >
                  {t(healthLabelKey(channel.health_state))}
                </Badge>
              </div>
            </CardHeader>
            <CardContent className='grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4'>
              <Metric
                label={t('share.channel.protocol')}
                value={channel.protocol}
              />
              <Metric
                label={t('share.channel.keyPoolMode')}
                value={t(keyPoolModeLabelKey(channel.key_pool_mode))}
              />
              <Metric
                label={t('share.readiness.usableKeys')}
                value={`${usableKeys}/${keys.length}`}
              />
              <Metric
                label={t('share.readiness.models')}
                value={t('share.readiness.modelCounts', {
                  published: publishedOfferings,
                  pending: pendingOfferings,
                  draft: draftOfferings,
                })}
              />
            </CardContent>
          </Card>

          <KeyPoolCard channelId={props.channelId} keys={keys} />
          <ChannelActions channelId={props.channelId} />

          <Card id='models'>
            <CardHeader>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div>
                  <CardTitle>{t('share.offerings')}</CardTitle>
                  <p className='text-muted-foreground mt-1 text-sm'>
                    {t('share.models.description')}
                  </p>
                </div>
                {draftOfferings > 0 ? (
                  <Button
                    size='sm'
                    disabled={
                      readyToSubmit === 0 || submitAllMutation.isPending
                    }
                    onClick={() => submitAllMutation.mutate()}
                  >
                    <Send />
                    {t('share.review.submitReady', { count: readyToSubmit })}
                  </Button>
                ) : null}
              </div>
            </CardHeader>
            <CardContent>
              {offerings.length === 0 ? (
                <EmptyState title={t('share.offerings.empty')} />
              ) : (
                <StaticDataTable
                  columns={offeringColumns}
                  data={offerings}
                  getRowKey={(row) => row.id}
                />
              )}
            </CardContent>
          </Card>

          <PriceMatchCard channelId={props.channelId} offerings={offerings} />

          <ConfirmDialog
            open={deletingOffering !== null}
            onOpenChange={(open) => {
              if (!open) setDeletingOffering(null)
            }}
            title={t('share.offering.deleteTitle')}
            desc={t('share.offering.deleteDescription', {
              model: deletingOffering?.upstream_model_id ?? '',
            })}
            confirmText={t('Delete')}
            destructive
            isLoading={deleteOfferingMutation.isPending}
            handleConfirm={() => {
              if (deletingOffering) {
                deleteOfferingMutation.mutate(deletingOffering.id)
              }
            }}
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function Metric(props: { label: string; value: string }) {
  return (
    <div className='bg-muted/30 rounded-lg border p-3'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='mt-1 font-medium break-words'>{props.value}</div>
    </div>
  )
}

function SubmitOfferingButton(props: {
  offering: Offering
  pending: boolean
  onSubmit: (id: number) => void
}) {
  const { t } = useTranslation()
  if (props.offering.status !== 0 && props.offering.status !== 3) {
    return null
  }
  if (!props.offering.price_configured) {
    return (
      <Button variant='outline' size='sm' render={<a href='#price-match' />}>
        {t('share.offering.setPriceFirst')}
      </Button>
    )
  }
  return (
    <Button
      variant='outline'
      size='sm'
      disabled={props.pending}
      onClick={() => props.onSubmit(props.offering.id)}
    >
      {t('share.offering.submit')}
    </Button>
  )
}
