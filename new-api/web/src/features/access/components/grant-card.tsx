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
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { ShareGroup } from '@/features/share/types'
import { handleServerError } from '@/lib/handle-server-error'

import { createGrant, deleteGrant, listGrants, type AccessGrant } from '../api'

export function GrantAccessCard(props: { groups: ShareGroup[] }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [groupId, setGroupId] = useState('')
  const [username, setUsername] = useState('')
  const [days, setDays] = useState('30')
  const selectedGroupId = Number(groupId)

  const grantsQuery = useQuery({
    queryKey: ['apigx-access-grants', selectedGroupId],
    queryFn: () => listGrants(0, selectedGroupId),
    enabled: selectedGroupId > 0,
  })
  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: ['apigx-access-grants', selectedGroupId],
    })

  const createMutation = useMutation({
    mutationFn: () => {
      const parsedDays = Number(days)
      return createGrant({
        resource_type: 0,
        resource_id: selectedGroupId,
        target_username: username.trim(),
        expires_at:
          parsedDays > 0
            ? Math.floor(Date.now() / 1000) + parsedDays * 86400
            : 0,
      })
    },
    onSuccess: async () => {
      setUsername('')
      await refresh()
      toast.success(t('Access granted'))
    },
    onError: (error) => handleServerError(error),
  })
  const revokeMutation = useMutation({
    mutationFn: deleteGrant,
    onSuccess: async () => {
      await refresh()
      toast.success(t('Access revoked'))
    },
    onError: (error) => handleServerError(error),
  })

  const grants = grantsQuery.data?.data?.items ?? []
  const columns: StaticDataTableColumn<AccessGrant>[] = [
    {
      id: 'user',
      header: t('User'),
      cell: (grant) =>
        grant.subject_display_name ||
        t('User {{id}}', { id: grant.subject_id }),
    },
    {
      id: 'source',
      header: t('Granted by'),
      cell: (grant) =>
        grant.scope_kind === 1 ? t('Invite code') : t('Direct grant'),
    },
    {
      id: 'expires',
      header: t('Expires'),
      cell: (grant) =>
        grant.expires_at > 0
          ? new Date(grant.expires_at * 1000).toLocaleDateString()
          : t('Never'),
    },
    {
      id: 'actions',
      header: '',
      cell: (grant) => (
        <Button
          variant='outline'
          size='sm'
          disabled={revokeMutation.isPending}
          onClick={() => revokeMutation.mutate(grant.id)}
        >
          {t('Revoke')}
        </Button>
      ),
    },
  ]
  let grantList = (
    <StaticDataTable
      columns={columns}
      data={grants}
      getRowKey={(grant) => grant.id}
    />
  )
  if (selectedGroupId <= 0) {
    grantList = (
      <EmptyState title={t('Select a shared resource to review access')} />
    )
  } else if (grants.length === 0) {
    grantList = <EmptyState title={t('No active access grants')} />
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Direct user access')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        <p className='text-muted-foreground text-sm'>
          {t('Grant a registered user access by exact username.')}
        </p>
        <FieldGroup className='grid gap-3 sm:grid-cols-3'>
          <Field>
            <FieldLabel htmlFor='grant-share-group'>
              {t('Shared resource')}
            </FieldLabel>
            <Select
              value={groupId}
              onValueChange={(next) => setGroupId(next ?? '')}
            >
              <SelectTrigger id='grant-share-group'>
                <SelectValue placeholder={t('Select a shared resource')}>
                  {props.groups.find((group) => String(group.id) === groupId)
                    ?.name ?? ''}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {props.groups.map((group) => (
                    <SelectItem key={group.id} value={String(group.id)}>
                      {group.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor='grant-username'>{t('Username')}</FieldLabel>
            <Input
              id='grant-username'
              value={username}
              autoComplete='off'
              onChange={(event) => setUsername(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='grant-valid-days'>
              {t('Valid days')}
            </FieldLabel>
            <Input
              id='grant-valid-days'
              value={days}
              inputMode='numeric'
              onChange={(event) => setDays(event.target.value)}
            />
          </Field>
        </FieldGroup>
        <div>
          <Button
            disabled={
              selectedGroupId <= 0 ||
              username.trim() === '' ||
              createMutation.isPending
            }
            onClick={() => createMutation.mutate()}
          >
            {t('Grant access')}
          </Button>
        </div>

        {grantList}
      </CardContent>
    </Card>
  )
}
