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
*/import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { getShareChannels, getShareGroups } from './api'
import { healthLabelKey, keyPoolModeLabelKey, visibilityLabelKey } from './lib/labels'
import type { ShareChannel, ShareGroup } from './types'

/** 我的分享：分组与渠道。 */
export function MyShares() {
  const { t } = useTranslation()

  const groupsQuery = useQuery({ queryKey: ['apigx-share-groups'], queryFn: getShareGroups })
  const channelsQuery = useQuery({ queryKey: ['apigx-share-channels'], queryFn: getShareChannels })

  if (groupsQuery.isPending || channelsQuery.isPending) {
    return <LoadingState />
  }
  if (groupsQuery.isError || channelsQuery.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void groupsQuery.refetch()
          void channelsQuery.refetch()
        }}
      />
    )
  }

  const groups = groupsQuery.data?.data?.items ?? []
  const channels = channelsQuery.data?.data?.items ?? []

  const groupColumns: StaticDataTableColumn<ShareGroup>[] = [
    { id: 'name', header: t('share.group.name'), cell: (row) => row.name },
    {
      id: 'visibility',
      header: t('share.group.visibility'),
      cell: (row) => t(visibilityLabelKey(row.default_visibility)),
    },
    { id: 'public_id', header: t('share.group.publicId'), cell: (row) => row.public_id },
  ]

  const channelColumns: StaticDataTableColumn<ShareChannel>[] = [
    { id: 'name', header: t('share.channel.name'), cell: (row) => row.name },
    { id: 'protocol', header: t('share.channel.protocol'), cell: (row) => row.protocol },
    {
      id: 'key_pool_mode',
      header: t('share.channel.keyPoolMode'),
      cell: (row) => t(keyPoolModeLabelKey(row.key_pool_mode)),
    },
    {
      id: 'health_state',
      header: t('share.channel.health'),
      cell: (row) => t(healthLabelKey(row.health_state)),
    },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('share.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <Card>
            <CardHeader>
              <CardTitle>{t('share.groups')}</CardTitle>
            </CardHeader>
            <CardContent>
              {groups.length === 0 ? (
                <EmptyState title={t('share.groups.empty')} />
              ) : (
                <StaticDataTable columns={groupColumns} data={groups} getRowKey={(row) => row.id} />
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('share.channels')}</CardTitle>
            </CardHeader>
            <CardContent>
              {channels.length === 0 ? (
                <EmptyState title={t('share.channels.empty')} />
              ) : (
                <StaticDataTable columns={channelColumns} data={channels} getRowKey={(row) => row.id} />
              )}
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}