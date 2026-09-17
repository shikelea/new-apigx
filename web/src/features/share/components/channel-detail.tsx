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
*/import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { getChannel, getChannelKeys, getChannelOfferings, submitOffering } from '../api'
import { ChannelActions } from './channel-actions'
import { healthLabelKey, keyPoolModeLabelKey, keyStatusLabelKey, offeringStatusLabelKey } from '../lib/labels'
import type { Offering, UpstreamKey } from '../types'

/** 渠道详情：Key 池与供给。凭据只显示掩码（后端从不返回明文）。 */
export function ChannelDetailView(props: { channelId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const submitMutation = useMutation({
    mutationFn: (offeringId: number) => submitOffering(offeringId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['apigx-channel-offerings', props.channelId] })
    },
  })

  const channelQuery = useQuery({ queryKey: ['apigx-channel', props.channelId], queryFn: () => getChannel(props.channelId) })
  const keysQuery = useQuery({ queryKey: ['apigx-channel-keys', props.channelId], queryFn: () => getChannelKeys(props.channelId) })
  const offeringsQuery = useQuery({
    queryKey: ['apigx-channel-offerings', props.channelId],
    queryFn: () => getChannelOfferings(props.channelId),
  })

  if (channelQuery.isPending || keysQuery.isPending || offeringsQuery.isPending) {
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

  const keyColumns: StaticDataTableColumn<UpstreamKey>[] = [
    { id: 'name', header: t('share.key.name'), cell: (row) => row.name },
    { id: 'stable_key_id', header: t('share.key.stableId'), cell: (row) => row.stable_key_id },
    { id: 'credential', header: t('share.key.credential'), cell: (row) => row.masked },
    { id: 'weight', header: t('share.key.weight'), cell: (row) => String(row.weight) },
    { id: 'status', header: t('share.key.status'), cell: (row) => t(keyStatusLabelKey(row.status)) },
    {
      id: 'usable',
      header: t('share.key.usable'),
      cell: (row) => (row.usable ? <Badge>{t('share.key.usableYes')}</Badge> : <Badge variant='outline'>{t('share.key.usableNo')}</Badge>),
    },
  ]

  const offeringColumns: StaticDataTableColumn<Offering>[] = [
    { id: 'model', header: t('share.offering.model'), cell: (row) => row.upstream_model_id },
    { id: 'status', header: t('share.offering.status'), cell: (row) => t(offeringStatusLabelKey(row.status)) },
    { id: 'capabilities', header: t('share.offering.capabilities'), cell: (row) => (row.capabilities ?? []).join(', ') },
    { id: 'price_rule', header: t('share.offering.priceRule'), cell: (row) => String(row.price_rule_id) },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <SubmitOfferingButton offering={row} pending={submitMutation.isPending} onsubmit={submitMutation.mutate} />
      ),
    },
  ]

  if (!channel) {
    return <EmptyState title={t('share.detail.missing')} />
  }

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle className='break-all'>{channel.name}</CardTitle>
        </CardHeader>
        <CardContent className='grid gap-2 text-sm sm:grid-cols-2'>
          <div>
            {t('share.channel.protocol')}: {channel.protocol}
          </div>
          <div>
            {t('share.channel.keyPoolMode')}: {t(keyPoolModeLabelKey(channel.key_pool_mode))}
          </div>
          <div>
            {t('share.channel.health')}: {t(healthLabelKey(channel.health_state))}
          </div>
          <div className='break-all'>
            {t('share.channel.baseUrl')}: {channel.base_url}
          </div>
        </CardContent>
      </Card>

      <ChannelActions channelId={props.channelId} />

      <Card>
        <CardHeader>
          <CardTitle>{t('share.keyPool')}</CardTitle>
        </CardHeader>
        <CardContent>
          {keys.length === 0 ? (
            <EmptyState title={t('share.keyPool.empty')} />
          ) : (
            <StaticDataTable columns={keyColumns} data={keys} getRowKey={(row) => row.id} />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('share.offerings')}</CardTitle>
        </CardHeader>
        <CardContent>
          {offerings.length === 0 ? (
            <EmptyState title={t('share.offerings.empty')} />
          ) : (
            <StaticDataTable columns={offeringColumns} data={offerings} getRowKey={(row) => row.id} />
          )}
        </CardContent>
      </Card>
    </div>
  )
}
/** 草稿供给的"提交审核"按钮；非草稿不显示动作（发布/驳回属平台侧）。 */
function SubmitOfferingButton(props: { offering: Offering; pending: boolean; onsubmit: (id: number) => void }) {
  const { t } = useTranslation()
  if (props.offering.status !== 0) {
    return null
  }
  return (
    <Button variant='outline' size='sm' disabled={props.pending} onClick={() => props.onsubmit(props.offering.id)}>
      {t('share.offering.submit')}
    </Button>
  )
}