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

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  createRegistrationInvite,
  listRegistrationInvites,
  revokeRegistrationInvite,
  type RegistrationInvite,
} from '@/features/access/api'
import { handleServerError } from '@/lib/handle-server-error'

function effectiveInviteStatus(invite: RegistrationInvite): number {
  if (
    invite.status === 0 &&
    invite.expires_at > 0 &&
    invite.expires_at <= Math.floor(Date.now() / 1000)
  ) {
    return 3
  }
  return invite.status
}

function statusLabelKey(status: number): string {
  if (status === 0) return 'Active'
  if (status === 1) return 'Revoked'
  if (status === 2) return 'Used up'
  return 'Expired'
}

export function RegistrationInvitesCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [maxUses, setMaxUses] = useState('1')
  const [validDays, setValidDays] = useState('7')
  const [issuedToken, setIssuedToken] = useState('')
  const [revoking, setRevoking] = useState<RegistrationInvite | null>(null)

  const query = useQuery({
    queryKey: ['apigx-registration-invites'],
    queryFn: listRegistrationInvites,
  })

  const create = useMutation({
    mutationFn: () => {
      const uses = Number.parseInt(maxUses, 10)
      const days = Number.parseInt(validDays, 10)
      return createRegistrationInvite({
        max_uses: Number.isFinite(uses) && uses >= 0 ? uses : 1,
        expires_at:
          Number.isFinite(days) && days > 0
            ? Math.floor(Date.now() / 1000) + days * 86400
            : 0,
      })
    },
    onSuccess: (result) => {
      setIssuedToken(result.data?.token ?? '')
      toast.success(t('Registration invite created'))
      void queryClient.invalidateQueries({
        queryKey: ['apigx-registration-invites'],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const revoke = useMutation({
    mutationFn: (id: number) => revokeRegistrationInvite(id),
    onSuccess: () => {
      setRevoking(null)
      toast.success(t('Registration invite revoked'))
      void queryClient.invalidateQueries({
        queryKey: ['apigx-registration-invites'],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const columns: StaticDataTableColumn<RegistrationInvite>[] = [
    {
      id: 'code',
      header: t('Invite code'),
      cell: (row) => <span className='font-mono'>{row.token_prefix}…</span>,
    },
    {
      id: 'uses',
      header: t('Uses'),
      cell: (row) =>
        row.max_uses > 0
          ? `${row.used_count} / ${row.max_uses}`
          : t('Unlimited'),
    },
    {
      id: 'expires',
      header: t('Expires at'),
      cell: (row) =>
        row.expires_at > 0
          ? new Date(row.expires_at * 1000).toLocaleString()
          : t('Never'),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (row) => t(statusLabelKey(effectiveInviteStatus(row))),
    },
    {
      id: 'actions',
      header: '',
      cell: (row) =>
        effectiveInviteStatus(row) === 0 ? (
          <Button variant='outline' size='sm' onClick={() => setRevoking(row)}>
            {t('Revoke')}
          </Button>
        ) : null,
    },
  ]

  const items = query.data?.data?.items ?? []
  const inviteList = (() => {
    if (query.isError) {
      return <p className='text-destructive text-sm'>{query.error.message}</p>
    }
    if (query.isPending) {
      return <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
    }
    if (items.length === 0) {
      return <EmptyState title={t('No registration invite yet')} />
    }
    return (
      <StaticDataTable
        columns={columns}
        data={items}
        getRowKey={(row) => row.id}
      />
    )
  })()

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Registration invite codes')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Generate codes here before requiring an invite for registration. The full code is shown only once.'
          )}
        </p>

        <FieldGroup className='grid gap-3 sm:grid-cols-2'>
          <Field>
            <FieldLabel htmlFor='registration-invite-max-uses'>
              {t('Max uses')}
            </FieldLabel>
            <Input
              id='registration-invite-max-uses'
              type='number'
              min={0}
              step={1}
              value={maxUses}
              onChange={(event) => setMaxUses(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='registration-invite-valid-days'>
              {t('Valid days')}
            </FieldLabel>
            <Input
              id='registration-invite-valid-days'
              type='number'
              min={0}
              step={1}
              value={validDays}
              onChange={(event) => setValidDays(event.target.value)}
            />
          </Field>
        </FieldGroup>

        <Button
          disabled={create.isPending}
          onClick={() => create.mutate(undefined)}
        >
          {t('Generate registration invite')}
        </Button>

        {issuedToken === '' ? null : (
          <div className='bg-muted/40 space-y-2 rounded-md p-3'>
            <p className='text-sm font-medium'>
              {t('Copy the invite code now — it is shown only once.')}
            </p>
            <div className='flex min-w-0 items-center gap-2'>
              <code className='bg-background min-w-0 truncate rounded px-2 py-1 font-mono text-xs'>
                {issuedToken}
              </code>
              <CopyButton value={issuedToken} />
            </div>
          </div>
        )}

        {inviteList}

        <ConfirmDialog
          open={revoking !== null}
          onOpenChange={(open) => {
            if (!open) setRevoking(null)
          }}
          title={t('Revoke registration invite')}
          desc={t(
            'This code will stop accepting new registrations. Existing users are not affected.'
          )}
          destructive
          confirmText={t('Revoke')}
          isLoading={revoke.isPending}
          handleConfirm={() => {
            if (revoking) revoke.mutate(revoking.id)
          }}
        />
      </CardContent>
    </Card>
  )
}
