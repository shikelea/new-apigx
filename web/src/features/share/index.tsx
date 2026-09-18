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
import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { deleteShareChannel, deleteShareGroup, getShareChannels, getShareGroups } from './api'
import { AccessInviteCard } from '@/features/access/components/invite-card'
import { ChannelCreateDialog } from './components/channel-create-dialog'
import { GroupCreateDialog } from './components/group-create-dialog'
import { GroupEditDialog } from './components/group-edit-dialog'
import { healthLabelKey, keyPoolModeLabelKey, visibilityLabelKey } from './lib/labels'
import type { ShareChannel, ShareGroup } from './types'

/** 我的分享：分组与渠道。 */
/** 分组发布状态 → i18n 键（查表代替嵌套三元，遵守 web/AGENTS.md §3.2）。 */
function groupPublishStatusLabelKey(status: number): string {
  if (status === 0) return 'Draft'
  if (status === 1) return 'Pending review'
  if (status === 2) return 'Published'
  if (status === 3) return 'Suspended'
  return 'Retired'
}

export function MyShares() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editingGroup, setEditingGroup] = useState<ShareGroup | null>(null)
  const [creatingGroup, setCreatingGroup] = useState(false)
  const navigate = useNavigate()
  const [creatingChannel, setCreatingChannel] = useState(false)
  const [deletingGroup, setDeletingGroup] = useState<ShareGroup | null>(null)
  const [deletingChannel, setDeletingChannel] = useState<ShareChannel | null>(null)

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

  // 删除（§4/§5 的 CRUD 含 Delete）：软删除，删完刷新列表
  const deleteGroupMutation = useMutation({
    mutationFn: (id: number) => deleteShareGroup(id),
    onSuccess: () => {
      setDeletingGroup(null)
      void queryClient.invalidateQueries({ queryKey: ['apigx-share-groups'] })
      void queryClient.invalidateQueries({ queryKey: ['apigx-share-channels'] })
    },
  })
  const deleteChannelMutation = useMutation({
    mutationFn: (id: number) => deleteShareChannel(id),
    onSuccess: () => {
      setDeletingChannel(null)
      void queryClient.invalidateQueries({ queryKey: ['apigx-share-channels'] })
    },
  })

  const groupColumns: StaticDataTableColumn<ShareGroup>[] = [
    { id: 'publish_status', header: t('Status'), cell: (row) => t(groupPublishStatusLabelKey(row.publish_status)) },
    { id: 'name', header: t('share.group.name'), cell: (row) => row.name },
    {
      id: 'visibility',
      header: t('share.group.visibility'),
      cell: (row) => t(visibilityLabelKey(row.default_visibility)),
    },
    { id: 'public_id', header: t('share.group.publicId'), cell: (row) => row.public_id },
    {
      id: 'description',
      header: t('Description'),
      // 说明是不可信文本：这里按纯文本渲染（不打 HTML）
      cell: (row) => <span className='text-muted-foreground line-clamp-2 text-xs'>{row.description}</span>,
    },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex gap-2'>
          <Button variant='outline' size='sm' onClick={() => setEditingGroup(row)}>
            {t('Edit')}
          </Button>
          <Button variant='outline' size='sm' onClick={() => setDeletingGroup(row)}>
            {t('Delete')}
          </Button>
        </div>
      ),
    },
  ]

  const channelColumns: StaticDataTableColumn<ShareChannel>[] = [
    {
      id: 'name',
      header: t('share.channel.name'),
      cell: (row) => (
        <Link to='/share/$channelId' params={{ channelId: String(row.id) }} className='hover:underline'>
          {row.name}
        </Link>
      ),
    },
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
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <Button variant='outline' size='sm' onClick={() => setDeletingChannel(row)}>
          {t('Delete')}
        </Button>
      ),
    },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('share.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <Card>
            <CardHeader>
              <div className='flex items-center justify-between gap-3'>
          <CardTitle>{t('share.groups')}</CardTitle>
            <Button size='sm' onClick={() => setCreatingGroup(true)}>
              {t('New share group')}
            </Button>
          </div>
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
              <div className='flex items-center justify-between gap-3'>
          <CardTitle>{t('share.channels')}</CardTitle>
            <Button size='sm' onClick={() => setCreatingChannel(true)}>
              {t('New share channel')}
            </Button>
          </div>
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
          <AccessInviteCard groups={groups} />
      <ConfirmDialog
        open={deletingGroup !== null}
        onOpenChange={(next) => {
          if (!next) setDeletingGroup(null)
        }}
        title={t('Delete share group')}
        desc={t('The group disappears from your lists. Channels under it are not deleted.')}
        destructive
        confirmText={t('Delete')}
        isLoading={deleteGroupMutation.isPending}
        handleConfirm={() => {
          if (deletingGroup) deleteGroupMutation.mutate(deletingGroup.id)
        }}
      />
      <ConfirmDialog
        open={deletingChannel !== null}
        onOpenChange={(next) => {
          if (!next) setDeletingChannel(null)
        }}
        title={t('Delete share channel')}
        desc={t('The channel disappears from your lists and stops serving requests.')}
        destructive
        confirmText={t('Delete')}
        isLoading={deleteChannelMutation.isPending}
        handleConfirm={() => {
          if (deletingChannel) deleteChannelMutation.mutate(deletingChannel.id)
        }}
      />
      <ChannelCreateDialog
          groups={groups}
          open={creatingChannel}
          onOpenChange={setCreatingChannel}
          onSaved={(createdId) => {
            // 新建渠道会同时影响渠道列表（以及分组下的渠道数）
            void queryClient.invalidateQueries({ queryKey: ['apigx-share-channels'] })
            void queryClient.invalidateQueries({ queryKey: ['apigx-share-groups'] })
            // U-1 向导：渠道建好只是第一步 —— 直接把人带到渠道详情页的「选择模型」，
            // 免得"建完就以为已经就绪"（模型没导入 = 供给是草稿，不会对外服务）。
            if (createdId) {
              void navigate({ to: '/share/$channelId', params: { channelId: String(createdId) } })
            }
          }}
        />
        <GroupCreateDialog
          open={creatingGroup}
          onOpenChange={setCreatingGroup}
          onSaved={() => {
            void queryClient.invalidateQueries({ queryKey: ['apigx-share-groups'] })
          }}
        />
      <GroupEditDialog
          group={editingGroup}
          open={editingGroup !== null}
          onOpenChange={(next) => {
            if (!next) setEditingGroup(null)
          }}
          onSaved={() => {
            setEditingGroup(null)
            void queryClient.invalidateQueries({ queryKey: ['apigx-share-groups'] })
          }}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}