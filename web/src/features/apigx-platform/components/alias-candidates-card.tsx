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

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { bulkConfirmAliases, getAliasCandidates, rejectAliasCandidate } from '../api'
import type { AliasCandidate } from '../types'
import { AliasConfirmDialog } from './alias-confirm-dialog'

/**
 * 模型目录 · 待确认候选（§4「批量匹配、人工覆盖」）。
 *
 * 候选由"创建供给"自动累积；**确认之后**广场才会显示对外标准 ID 与模型介绍 ——
 * 候选本身绝不当作结论（模型列注释：候选仅提示，不自动路由）。
 */
export function AliasCandidatesCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<AliasCandidate | null>(null)

  const query = useQuery({ queryKey: ['apigx-alias-candidates'], queryFn: () => getAliasCandidates(0) })

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['apigx-alias-candidates'] })
  }

  const reject = useMutation({
    mutationFn: (id: number) => rejectAliasCandidate(id),
    onSuccess: refresh,
  })

  const bulk = useMutation({
    mutationFn: (normalized: string) => bulkConfirmAliases(normalized, { api_model_id: normalized, modality_type: 1 }),
    onSuccess: refresh,
  })

  const items = query.data?.data?.items ?? []

  const columns: StaticDataTableColumn<AliasCandidate>[] = [
    { id: 'external', header: t('Upstream model'), cell: (row) => row.external_model_id },
    { id: 'normalized', header: t('Normalized name'), cell: (row) => row.normalized_model_id },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex gap-2'>
          <Button size='sm' variant='outline' onClick={() => setEditing(row)}>
            {t('Confirm')}
          </Button>
          <Button size='sm' variant='outline' disabled={bulk.isPending} onClick={() => bulk.mutate(row.normalized_model_id)}>
            {t('Match all with the same name')}
          </Button>
          <Button size='sm' variant='outline' disabled={reject.isPending} onClick={() => reject.mutate(row.id)}>
            {t('Reject')}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Model catalog')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-xs'>{t('Candidates are collected automatically. The plaza only shows a standard model after confirmation.')}</p>
        {items.length === 0 ? (
          <EmptyState title={t('No pending candidate')} />
        ) : (
          <StaticDataTable columns={columns} data={items} getRowKey={(row) => row.id} />
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
