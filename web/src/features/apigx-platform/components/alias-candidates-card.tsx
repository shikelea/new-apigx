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

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import {
  bulkConfirmAliases,
  getAliasCandidates,
  rejectAliasCandidate,
} from '../api'
import type { AliasCandidate } from '../types'
import { AliasConfirmDialog } from './alias-confirm-dialog'

export function AliasCandidateMobileCards(props: {
  items: AliasCandidate[]
  bulkPending: boolean
  rejectPending: boolean
  onConfirm: (candidate: AliasCandidate) => void
  onBulk: (normalizedModelID: string) => void
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
          <div className='space-y-3'>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Upstream model')}
              </p>
              <code className='mt-1 block text-xs font-semibold break-all'>
                {row.external_model_id}
              </code>
            </div>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Suggested model name')}
              </p>
              <code className='mt-1 block text-xs break-all'>
                {row.normalized_model_id}
              </code>
            </div>
          </div>
          <div className='grid gap-2'>
            <Button variant='outline' onClick={() => props.onConfirm(row)}>
              {t('Confirm model')}
            </Button>
            <Button
              variant='outline'
              disabled={props.bulkPending}
              onClick={() => props.onBulk(row.normalized_model_id)}
            >
              {t('Confirm all matching names')}
            </Button>
            <Button
              variant='outline'
              disabled={props.rejectPending}
              onClick={() => props.onReject(row.id)}
            >
              {t('Reject')}
            </Button>
          </div>
        </div>
      ))}
    </section>
  )
}

/**
 * 模型身份确认（§4「批量匹配、人工覆盖」）。
 *
 * 新抓取的上游模型需要管理员确认它对应的统一模型；确认后，模型广场才能
 * 合并相同模型的分享来源。未经确认的名称只提供操作建议，不参与自动路由。
 */
export function AliasCandidatesCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<AliasCandidate | null>(null)

  const query = useQuery({
    queryKey: ['apigx-alias-candidates'],
    queryFn: () => getAliasCandidates(0),
  })

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['apigx-alias-candidates'] })
  }

  const reject = useMutation({
    mutationFn: (id: number) => rejectAliasCandidate(id),
    onSuccess: refresh,
  })

  const bulk = useMutation({
    mutationFn: (normalized: string) =>
      bulkConfirmAliases(normalized, {
        api_model_id: normalized,
        modality_type: 1,
      }),
    onSuccess: refresh,
  })

  const items = query.data?.data?.items ?? []

  const columns: StaticDataTableColumn<AliasCandidate>[] = [
    {
      id: 'external',
      header: t('Upstream model'),
      cell: (row) => row.external_model_id,
    },
    {
      id: 'normalized',
      header: t('Suggested model name'),
      cell: (row) => row.normalized_model_id,
    },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex gap-2'>
          <Button size='sm' variant='outline' onClick={() => setEditing(row)}>
            {t('Confirm model')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={bulk.isPending}
            onClick={() => bulk.mutate(row.normalized_model_id)}
          >
            {t('Confirm all matching names')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={reject.isPending}
            onClick={() => reject.mutate(row.id)}
          >
            {t('Reject')}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Model identity review')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Newly discovered models appear here for review. Confirm which model they belong to, and the model plaza will combine matching resources under one name with a shared description.'
          )}
        </p>
        {items.length === 0 ? (
          <EmptyState title={t('No models awaiting confirmation')} />
        ) : (
          <>
            <AliasCandidateMobileCards
              items={items}
              bulkPending={bulk.isPending}
              rejectPending={reject.isPending}
              onConfirm={setEditing}
              onBulk={(normalizedModelID) => bulk.mutate(normalizedModelID)}
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
