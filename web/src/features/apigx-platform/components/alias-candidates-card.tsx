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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'

import { getAliasCandidates, rejectAliasCandidate } from '../api'
import type { AliasCandidate } from '../types'
import { AliasConfirmDialog } from './alias-confirm-dialog'

export function AliasCandidateMobileCards(props: {
  items: AliasCandidate[]
  rejectPending: boolean
  onConfirm: (candidate: AliasCandidate) => void
  onReject: (id: number) => void
}) {
  const { t } = useTranslation()

  return (
    <section
      aria-label={t('Model identity review')}
      className='space-y-3 md:hidden'
    >
      {props.items.map((row) => (
        <div className='space-y-4 rounded-lg border p-4' key={row.id}>
          <div>
            <code className='block text-xs font-semibold break-all'>
              {row.external_model_id}
            </code>
            <p className='text-muted-foreground mt-1 text-xs break-words'>
              {row.share_group_name || '—'} · {row.channel_name || '—'}
            </p>
          </div>
          <div className='grid grid-cols-2 gap-2 text-xs'>
            <div className='bg-muted/30 rounded-md border p-2'>
              <span className='text-muted-foreground block'>
                {t('identity.sharer')}
              </span>
              <span className='mt-1 block font-medium'>
                {t('Provider {{id}}', { id: row.owner_user_id })}
              </span>
            </div>
            <div className='bg-muted/30 rounded-md border p-2'>
              <span className='text-muted-foreground block'>
                {t('identity.target')}
              </span>
              <code className='mt-1 block break-all'>
                {row.target_api_model_id || t('identity.newModelPending')}
              </code>
            </div>
          </div>
          <div className='grid gap-2 sm:grid-cols-2'>
            <Button variant='outline' onClick={() => props.onConfirm(row)}>
              {row.confirmation_state === 1
                ? t('identity.editMapping')
                : t('Confirm model')}
            </Button>
            {row.confirmation_state === 0 ? (
              <Button
                variant='outline'
                disabled={props.rejectPending}
                onClick={() => props.onReject(row.id)}
              >
                {t('Reject')}
              </Button>
            ) : null}
          </div>
        </div>
      ))}
    </section>
  )
}

/** Submitted share-model identities, managed alongside the system model list. */
export function AliasCandidatesCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [state, setState] = useState<0 | 1>(0)
  const [editing, setEditing] = useState<AliasCandidate | null>(null)

  const query = useQuery({
    queryKey: ['apigx-alias-candidates', state],
    queryFn: () => getAliasCandidates(state, 1, 200),
  })

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['apigx-alias-candidates'] })
    void queryClient.invalidateQueries({ queryKey: ['apigx-canonical-models'] })
    void queryClient.invalidateQueries({
      queryKey: ['apigx-channel-offerings'],
    })
    void queryClient.invalidateQueries({ queryKey: ['apigx-review-offerings'] })
    void queryClient.invalidateQueries({ queryKey: ['apigx-plaza'] })
  }

  const reject = useMutation({
    mutationFn: (id: number) => rejectAliasCandidate(id),
    onSuccess: () => {
      toast.success(t('identity.rejected'))
      refresh()
    },
    onError: (error) => handleServerError(error),
  })

  const items = query.data?.data?.items ?? []
  const columns: StaticDataTableColumn<AliasCandidate>[] = [
    {
      id: 'model',
      header: t('Upstream model'),
      className: 'min-w-48',
      cell: (row) => (
        <div>
          <code className='text-xs font-semibold break-all'>
            {row.external_model_id}
          </code>
          <div className='text-muted-foreground mt-1 text-xs break-words'>
            {row.share_group_name || '—'} · {row.channel_name || '—'}
          </div>
        </div>
      ),
    },
    {
      id: 'owner',
      header: t('identity.sharer'),
      cell: (row) => t('Provider {{id}}', { id: row.owner_user_id }),
    },
    {
      id: 'target',
      header: t('identity.target'),
      className: 'min-w-48',
      cell: (row) => (
        <code className='text-xs break-all'>
          {row.target_api_model_id || t('identity.newModelPending')}
        </code>
      ),
    },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex flex-wrap justify-end gap-2'>
          <Button size='sm' variant='outline' onClick={() => setEditing(row)}>
            {row.confirmation_state === 1
              ? t('identity.editMapping')
              : t('Confirm model')}
          </Button>
          {row.confirmation_state === 0 ? (
            <Button
              size='sm'
              variant='outline'
              disabled={reject.isPending}
              onClick={() => reject.mutate(row.id)}
            >
              {t('Reject')}
            </Button>
          ) : null}
        </div>
      ),
    },
  ]

  return (
    <Card>
      <CardHeader className='gap-4'>
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <div>
            <CardTitle>{t('Model identity review')}</CardTitle>
            <p className='text-muted-foreground mt-1 max-w-3xl text-sm'>
              {t('identity.description')}
            </p>
          </div>
          <div className='flex rounded-lg border p-1'>
            <Button
              size='sm'
              variant={state === 0 ? 'secondary' : 'ghost'}
              onClick={() => setState(0)}
            >
              {t('identity.pending')}
            </Button>
            <Button
              size='sm'
              variant={state === 1 ? 'secondary' : 'ghost'}
              onClick={() => setState(1)}
            >
              {t('identity.confirmed')}
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <EmptyState
            title={
              state === 0
                ? t('No models awaiting confirmation')
                : t('identity.noConfirmed')
            }
          />
        ) : (
          <>
            <AliasCandidateMobileCards
              items={items}
              rejectPending={reject.isPending}
              onConfirm={setEditing}
              onReject={(id) => reject.mutate(id)}
            />
            <div className='hidden md:block'>
              <StaticDataTable
                columns={columns}
                data={items}
                getRowKey={(row) => row.id}
              />
            </div>
          </>
        )}
        <AliasConfirmDialog
          candidate={editing}
          open={editing !== null}
          onOpenChange={(next) => {
            if (!next) setEditing(null)
          }}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
        />
      </CardContent>
    </Card>
  )
}
