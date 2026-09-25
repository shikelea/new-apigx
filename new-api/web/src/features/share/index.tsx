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
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowRight, Boxes, Plus, Settings2, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'

import {
  deleteShareChannel,
  deleteShareGroup,
  getShareChannels,
  getShareGroups,
} from './api'
import { ChannelCreateDialog } from './components/channel-create-dialog'
import { GroupCreateDialog } from './components/group-create-dialog'
import { GroupEditDialog } from './components/group-edit-dialog'
import {
  healthLabelKey,
  keyPoolModeLabelKey,
  visibilityLabelKey,
} from './lib/labels'
import type { ShareChannel, ShareGroup } from './types'

export function MyShares() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [editingGroup, setEditingGroup] = useState<ShareGroup | null>(null)
  const [creatingGroup, setCreatingGroup] = useState(false)
  const [creatingChannel, setCreatingChannel] = useState(false)
  const [deletingGroup, setDeletingGroup] = useState<ShareGroup | null>(null)
  const [deletingChannel, setDeletingChannel] = useState<ShareChannel | null>(
    null
  )

  const groupsQuery = useQuery({
    queryKey: ['apigx-share-groups'],
    queryFn: getShareGroups,
  })
  const channelsQuery = useQuery({
    queryKey: ['apigx-share-channels'],
    queryFn: getShareChannels,
  })

  const deleteGroupMutation = useMutation({
    mutationFn: (id: number) => deleteShareGroup(id),
    onSuccess: () => {
      setDeletingGroup(null)
      toast.success(t('share.group.deleted'))
      void queryClient.invalidateQueries({ queryKey: ['apigx-share-groups'] })
      void queryClient.invalidateQueries({ queryKey: ['apigx-share-channels'] })
      void queryClient.invalidateQueries({ queryKey: ['apigx-plaza'] })
      void queryClient.invalidateQueries({ queryKey: ['channels'] })
    },
    onError: (error) => handleServerError(error),
  })
  const deleteChannelMutation = useMutation({
    mutationFn: (id: number) => deleteShareChannel(id),
    onSuccess: () => {
      setDeletingChannel(null)
      toast.success(t('share.channel.deleted'))
      void queryClient.invalidateQueries({ queryKey: ['apigx-share-channels'] })
      void queryClient.invalidateQueries({ queryKey: ['apigx-plaza'] })
      void queryClient.invalidateQueries({ queryKey: ['channels'] })
    },
    onError: (error) => handleServerError(error),
  })

  if (groupsQuery.isPending || channelsQuery.isPending) return <LoadingState />
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

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('share.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' onClick={() => setCreatingGroup(true)}>
          <Boxes />
          {t('New share group')}
        </Button>
        <Button
          disabled={groups.length === 0}
          onClick={() => setCreatingChannel(true)}
        >
          <Plus />
          {t('New share channel')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <section className='space-y-3'>
            <div>
              <h2 className='text-lg font-semibold'>{t('share.groups')}</h2>
              <p className='text-muted-foreground text-sm'>
                {t('share.groups.description')}
              </p>
            </div>
            {groups.length === 0 ? (
              <Card>
                <CardContent className='py-8'>
                  <EmptyState title={t('share.groups.empty')} />
                  <div className='mt-4 flex justify-center'>
                    <Button onClick={() => setCreatingGroup(true)}>
                      {t('New share group')}
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ) : (
              <div className='grid gap-4 xl:grid-cols-2'>
                {groups.map((group) => (
                  <ShareGroupCard
                    key={group.id}
                    group={group}
                    channels={channels.filter(
                      (channel) => channel.share_group_id === group.id
                    )}
                    onEdit={() => setEditingGroup(group)}
                    onDeleteGroup={() => setDeletingGroup(group)}
                    onDeleteChannel={setDeletingChannel}
                  />
                ))}
              </div>
            )}
          </section>
        </div>

        <ConfirmDialog
          open={deletingGroup !== null}
          onOpenChange={(next) => {
            if (!next) setDeletingGroup(null)
          }}
          title={t('Delete share group')}
          desc={t(
            'The group and its channels disappear from your lists and stop serving requests.'
          )}
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
          desc={t(
            'The channel disappears from your lists and stops serving requests.'
          )}
          destructive
          confirmText={t('Delete')}
          isLoading={deleteChannelMutation.isPending}
          handleConfirm={() => {
            if (deletingChannel) {
              deleteChannelMutation.mutate(deletingChannel.id)
            }
          }}
        />
        <ChannelCreateDialog
          groups={groups}
          open={creatingChannel}
          onOpenChange={setCreatingChannel}
          onSaved={(createdId) => {
            void queryClient.invalidateQueries({
              queryKey: ['apigx-share-channels'],
            })
            void queryClient.invalidateQueries({
              queryKey: ['apigx-share-groups'],
            })
            if (createdId) {
              void navigate({
                to: '/share/$channelId',
                params: { channelId: String(createdId) },
              })
            }
          }}
        />
        <GroupCreateDialog
          open={creatingGroup}
          onOpenChange={setCreatingGroup}
          onSaved={() =>
            void queryClient.invalidateQueries({
              queryKey: ['apigx-share-groups'],
            })
          }
        />
        <GroupEditDialog
          group={editingGroup}
          open={editingGroup !== null}
          onOpenChange={(next) => {
            if (!next) setEditingGroup(null)
          }}
          onSaved={() => {
            setEditingGroup(null)
            void queryClient.invalidateQueries({
              queryKey: ['apigx-share-groups'],
            })
          }}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function ShareGroupCard(props: {
  group: ShareGroup
  channels: ShareChannel[]
  onEdit: () => void
  onDeleteGroup: () => void
  onDeleteChannel: (channel: ShareChannel) => void
}) {
  const { t } = useTranslation()
  return (
    <Card className='overflow-hidden'>
      <CardHeader className='border-b'>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <CardTitle className='truncate'>{props.group.name}</CardTitle>
            {props.group.description ? (
              <p className='text-muted-foreground mt-1 line-clamp-2 text-sm'>
                {props.group.description}
              </p>
            ) : null}
          </div>
          <div className='flex shrink-0 gap-1'>
            <Button
              variant='ghost'
              size='icon-sm'
              aria-label={t('Edit')}
              onClick={props.onEdit}
            >
              <Settings2 />
            </Button>
            <Button
              variant='ghost'
              size='icon-sm'
              aria-label={t('Delete')}
              onClick={props.onDeleteGroup}
            >
              <Trash2 />
            </Button>
          </div>
        </div>
        <div className='flex flex-wrap gap-2 pt-1'>
          <Badge variant='outline'>
            {t(visibilityLabelKey(props.group.default_visibility))}
          </Badge>
          <Badge variant='secondary'>
            {t('{{count}} channels', { count: props.channels.length })}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className='p-0'>
        {props.channels.length === 0 ? (
          <div className='text-muted-foreground px-5 py-6 text-center text-sm'>
            {t('share.group.noChannels')}
          </div>
        ) : (
          <div className='divide-y'>
            {props.channels.map((channel) => (
              <div
                key={channel.id}
                className='flex items-center gap-3 px-4 py-3'
              >
                <div className='min-w-0 flex-1'>
                  <Link
                    to='/share/$channelId'
                    params={{ channelId: String(channel.id) }}
                    className='font-medium hover:underline'
                  >
                    {channel.name}
                  </Link>
                  <div className='text-muted-foreground mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs'>
                    <span>{channel.protocol}</span>
                    <span>{t(keyPoolModeLabelKey(channel.key_pool_mode))}</span>
                    <span>{t(healthLabelKey(channel.health_state))}</span>
                  </div>
                </div>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  aria-label={t('Delete')}
                  onClick={() => props.onDeleteChannel(channel)}
                >
                  <Trash2 />
                </Button>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  render={
                    <Link
                      to='/share/$channelId'
                      params={{ channelId: String(channel.id) }}
                    />
                  }
                  aria-label={t('Open')}
                >
                  <ArrowRight />
                </Button>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
