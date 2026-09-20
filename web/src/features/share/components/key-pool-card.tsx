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
import { KeyRound, Plus, RefreshCw, RotateCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { handleServerError } from '@/lib/handle-server-error'

import {
  applyChannelKeyPool,
  createUpstreamKey,
  deleteUpstreamKey,
  rotateUpstreamKey,
  updateUpstreamKeyStatus,
  updateUpstreamKeyWeight,
} from '../api'
import { parseKeyWeight } from '../lib/key-weight'
import {
  isUpstreamKeyEnabled,
  keyStatusLabelKey,
  toggledUpstreamKeyStatus,
} from '../lib/labels'
import type { UpstreamKey } from '../types'

type KeyEditor = { mode: 'rotate' | 'weight'; key: UpstreamKey } | null

export function KeyPoolCard(props: { channelId: number; keys: UpstreamKey[] }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [editor, setEditor] = useState<KeyEditor>(null)
  const [deleting, setDeleting] = useState<UpstreamKey | null>(null)
  const [poolChanged, setPoolChanged] = useState(false)
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [weight, setWeight] = useState('1')

  const refresh = () => {
    void queryClient.invalidateQueries({
      queryKey: ['apigx-channel-keys', props.channelId],
    })
    void queryClient.invalidateQueries({
      queryKey: ['apigx-channel', props.channelId],
    })
  }

  const fail = (error: unknown) => handleServerError(error)

  const createMutation = useMutation({
    mutationFn: () =>
      createUpstreamKey(props.channelId, {
        name: name.trim(),
        key: secret.trim(),
        weight: parseKeyWeight(weight) ?? 0,
      }),
    onSuccess: () => {
      setAddOpen(false)
      setName('')
      setSecret('')
      setWeight('1')
      setPoolChanged(true)
      refresh()
      toast.success(t('share.key.savedApplyHint'))
    },
    onError: fail,
  })

  const rotateMutation = useMutation({
    mutationFn: () => rotateUpstreamKey(editor?.key.id ?? 0, secret.trim()),
    onSuccess: () => {
      setEditor(null)
      setSecret('')
      refresh()
      toast.success(t('share.key.rotated'))
    },
    onError: fail,
  })

  const weightMutation = useMutation({
    mutationFn: () =>
      updateUpstreamKeyWeight(editor?.key.id ?? 0, parseKeyWeight(weight) ?? 0),
    onSuccess: () => {
      setEditor(null)
      setWeight('1')
      setPoolChanged(true)
      refresh()
      toast.success(t('share.key.savedApplyHint'))
    },
    onError: fail,
  })

  const statusMutation = useMutation({
    mutationFn: (key: UpstreamKey) =>
      updateUpstreamKeyStatus(key.id, toggledUpstreamKeyStatus(key.status)),
    onSuccess: () => {
      setPoolChanged(true)
      refresh()
      toast.success(t('share.key.savedApplyHint'))
    },
    onError: fail,
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteUpstreamKey(id),
    onSuccess: () => {
      setDeleting(null)
      setPoolChanged(true)
      refresh()
      toast.success(t('share.key.savedApplyHint'))
    },
    onError: fail,
  })

  const applyMutation = useMutation({
    mutationFn: () => applyChannelKeyPool(props.channelId),
    onSuccess: () => {
      setPoolChanged(false)
      refresh()
      toast.success(t('share.keyPool.applied'))
    },
    onError: fail,
  })

  const columns: StaticDataTableColumn<UpstreamKey>[] = [
    {
      id: 'name',
      header: t('share.key.name'),
      cell: (row) => (
        <div className='min-w-36'>
          <div className='font-medium'>
            {row.name || t('share.key.unnamed')}
          </div>
          <div className='text-muted-foreground font-mono text-xs'>
            {row.masked}
          </div>
        </div>
      ),
    },
    {
      id: 'weight',
      header: t('share.key.weight'),
      cell: (row) => <span className='font-mono text-sm'>{row.weight}</span>,
    },
    {
      id: 'status',
      header: t('share.key.status'),
      cell: (row) => (
        <div className='flex flex-wrap gap-1.5'>
          <Badge variant={row.usable ? 'default' : 'outline'}>
            {t(keyStatusLabelKey(row.status))}
          </Badge>
          <Badge variant='outline'>v{row.credential_version}</Badge>
        </div>
      ),
    },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex flex-wrap justify-end gap-1'>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => {
              setWeight(String(row.weight))
              setEditor({ mode: 'weight', key: row })
            }}
          >
            {t('share.key.editWeight')}
          </Button>
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('share.key.rotate')}
            onClick={() => setEditor({ mode: 'rotate', key: row })}
          >
            <RotateCw />
          </Button>
          <Button
            variant='ghost'
            size='sm'
            disabled={statusMutation.isPending}
            onClick={() => statusMutation.mutate(row)}
          >
            {isUpstreamKeyEnabled(row.status) ? t('Disable') : t('Enable')}
          </Button>
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('Delete')}
            onClick={() => setDeleting(row)}
          >
            <Trash2 />
          </Button>
        </div>
      ),
    },
  ]

  const validWeight = parseKeyWeight(weight) !== null

  return (
    <Card id='key-pool'>
      <CardHeader className='gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <div>
          <CardTitle className='flex items-center gap-2'>
            <KeyRound className='size-4' />
            {t('share.keyPool')}
          </CardTitle>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('share.keyPool.description')}
          </p>
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button variant='outline' size='sm' onClick={() => setAddOpen(true)}>
            <Plus />
            {t('share.key.add')}
          </Button>
          <Button
            size='sm'
            disabled={props.keys.length === 0 || applyMutation.isPending}
            onClick={() => applyMutation.mutate()}
          >
            <RefreshCw
              className={applyMutation.isPending ? 'animate-spin' : undefined}
            />
            {t('share.keyPool.apply')}
          </Button>
        </div>
      </CardHeader>
      <CardContent className='space-y-3'>
        {poolChanged ? (
          <Alert>
            <AlertTitle>{t('share.keyPool.pendingTitle')}</AlertTitle>
            <AlertDescription>
              {t('share.keyPool.pendingDescription')}
            </AlertDescription>
          </Alert>
        ) : null}
        {props.keys.length === 0 ? (
          <EmptyState title={t('share.keyPool.empty')} />
        ) : (
          <StaticDataTable
            columns={columns}
            data={props.keys}
            getRowKey={(row) => row.id}
          />
        )}
      </CardContent>

      <Dialog
        open={addOpen}
        onOpenChange={setAddOpen}
        title={t('share.key.add')}
        description={t('share.key.addDescription')}
        footer={
          <>
            <Button variant='outline' onClick={() => setAddOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={
                secret.trim() === '' || !validWeight || createMutation.isPending
              }
              onClick={() => createMutation.mutate()}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <div className='space-y-1.5'>
            <Label htmlFor='apigx-key-name'>{t('share.key.name')}</Label>
            <Input
              id='apigx-key-name'
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='apigx-key-secret'>{t('share.key.secret')}</Label>
            <Input
              id='apigx-key-secret'
              type='password'
              autoComplete='off'
              value={secret}
              onChange={(event) => setSecret(event.target.value)}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='apigx-key-weight'>{t('share.key.weight')}</Label>
            <Input
              id='apigx-key-weight'
              type='number'
              min={1}
              max={1000}
              value={weight}
              onChange={(event) => setWeight(event.target.value)}
            />
          </div>
        </div>
      </Dialog>

      <Dialog
        open={editor?.mode === 'rotate'}
        onOpenChange={(open) => {
          if (!open) setEditor(null)
        }}
        title={t('share.key.rotate')}
        description={t('share.key.rotateDescription')}
        footer={
          <>
            <Button variant='outline' onClick={() => setEditor(null)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={secret.trim() === '' || rotateMutation.isPending}
              onClick={() => rotateMutation.mutate()}
            >
              {t('Confirm')}
            </Button>
          </>
        }
      >
        <div className='space-y-1.5'>
          <Label htmlFor='apigx-key-rotate'>{t('share.key.secret')}</Label>
          <Input
            id='apigx-key-rotate'
            type='password'
            autoComplete='off'
            value={secret}
            onChange={(event) => setSecret(event.target.value)}
          />
        </div>
      </Dialog>

      <Dialog
        open={editor?.mode === 'weight'}
        onOpenChange={(open) => {
          if (!open) setEditor(null)
        }}
        title={t('share.key.editWeight')}
        description={t('share.key.weightDescription')}
        footer={
          <>
            <Button variant='outline' onClick={() => setEditor(null)}>
              {t('Cancel')}
            </Button>
            <Button
              disabled={!validWeight || weightMutation.isPending}
              onClick={() => weightMutation.mutate()}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='space-y-1.5'>
          <Label htmlFor='apigx-key-weight-edit'>{t('share.key.weight')}</Label>
          <Input
            id='apigx-key-weight-edit'
            type='number'
            min={1}
            max={1000}
            value={weight}
            onChange={(event) => setWeight(event.target.value)}
          />
        </div>
      </Dialog>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
        title={t('share.key.deleteTitle')}
        desc={t('share.key.deleteDescription')}
        destructive
        confirmText={t('Delete')}
        isLoading={deleteMutation.isPending}
        handleConfirm={() => {
          if (deleting) deleteMutation.mutate(deleting.id)
        }}
      />
    </Card>
  )
}
