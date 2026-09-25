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
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { ShareGroup } from '@/features/share/types'

import {
  createAccessInvite,
  listAccessInvites,
  revokeAccessInvite,
  type AccessInvite,
} from '../api'

/**
 * 访问授权：资源访问邀请（§4「访问授权」/「邀请兑换」）。
 *
 * 交互上的两条硬约定：
 *  1. **明文邀请码只显示一次**（库里只存哈希）⇒ 发放成功后就地展示 + 提供复制，并明确提示"仅此一次"；
 *  2. 撤销时可以**连带撤销**由该邀请产生的授权（后端支持 `revoke_grants`），默认打开 ——
 *     否则"撤销邀请"会留下已经发出去的权限，与"访问撤销"的要求不符。
 */
/** 邀请状态 → i18n 键（查表代替嵌套三元，遵守 web/AGENTS.md §3.2）。 */
function inviteStatusLabelKey(status: number): string {
  if (status === 0) return 'Active'
  if (status === 1) return 'Revoked'
  if (status === 2) return 'Used up'
  return 'Expired'
}

function canRevokeInvite(invite: AccessInvite): boolean {
  return invite.status === 0
}

export function AccessInviteCard(props: { groups: ShareGroup[] }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [groupId, setGroupId] = useState('')
  const [maxUses, setMaxUses] = useState('1')
  const [days, setDays] = useState('7')
  const [issuedToken, setIssuedToken] = useState('')
  const [revoking, setRevoking] = useState<AccessInvite | null>(null)

  const query = useQuery({
    queryKey: ['apigx-access-invites'],
    queryFn: () => listAccessInvites(),
  })

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['apigx-access-invites'] })
  }

  const create = useMutation({
    mutationFn: () => {
      const parsedMaxUses = Number(maxUses)
      const parsedDays = Number(days)
      return createAccessInvite({
        target_scope_type: 1, // 1=分享分组
        target_scope_id: Number(groupId),
        max_uses: Number.isFinite(parsedMaxUses) ? parsedMaxUses : 1,
        // 有效期按天换算成 UTC epoch 秒；0 表示不过期
        expires_at:
          parsedDays > 0
            ? Math.floor(Date.now() / 1000) + parsedDays * 86400
            : 0,
        revoke_grants: true,
      })
    },
    onSuccess: (res) => {
      setIssuedToken(res.data?.token ?? '')
      refresh()
    },
  })

  const revoke = useMutation({
    mutationFn: (id: number) => revokeAccessInvite(id, true),
    onSuccess: () => {
      setRevoking(null)
      refresh()
    },
  })

  const items = query.data?.data?.items ?? []
  const groupName = (invite: AccessInvite) =>
    props.groups.find((group) => group.id === invite.target_scope_id)?.name ??
    String(invite.target_scope_id)

  const columns: StaticDataTableColumn<AccessInvite>[] = [
    {
      id: 'prefix',
      header: t('Invite code'),
      cell: (row) => `${row.token_prefix}…`,
    },
    {
      id: 'target',
      header: t('Shared resource'),
      cell: (row) => groupName(row),
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
      id: 'status',
      header: t('Status'),
      cell: (row) => t(inviteStatusLabelKey(row.status)),
    },
    {
      id: 'actions',
      header: '',
      cell: (row) =>
        canRevokeInvite(row) ? (
          <Button variant='outline' size='sm' onClick={() => setRevoking(row)}>
            {t('Revoke')}
          </Button>
        ) : null,
    },
  ]

  const missing = groupId === ''

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Access invites')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t('Issue an invite so others can use one of your shared resources.')}
        </p>

        <FieldGroup className='grid gap-3 sm:grid-cols-3'>
          <Field>
            <FieldLabel htmlFor='access-invite-group'>
              {t('Shared resource')}
            </FieldLabel>
            <Select
              value={groupId}
              disabled={props.groups.length === 0}
              onValueChange={(next) => setGroupId(next ?? '')}
            >
              <SelectTrigger id='access-invite-group' className='w-full'>
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
            <FieldLabel htmlFor='access-invite-max-uses'>
              {t('Max uses')}
            </FieldLabel>
            <Input
              id='access-invite-max-uses'
              value={maxUses}
              onChange={(event) => setMaxUses(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='access-invite-days'>
              {t('Valid days')}
            </FieldLabel>
            <Input
              id='access-invite-days'
              value={days}
              onChange={(event) => setDays(event.target.value)}
            />
          </Field>
        </FieldGroup>

        <Button
          disabled={missing || create.isPending}
          onClick={() => create.mutate()}
        >
          {t('Issue invite')}
        </Button>

        {issuedToken === '' ? null : (
          <div className='bg-muted/40 space-y-2 rounded-md p-3'>
            <p className='text-sm font-medium'>
              {t('Copy the invite code now — it is shown only once.')}
            </p>
            <div className='flex items-center gap-2'>
              <code className='bg-background rounded px-2 py-1 font-mono text-xs'>
                {issuedToken}
              </code>
              <CopyButton value={issuedToken} />
            </div>
          </div>
        )}

        {items.length === 0 ? (
          <EmptyState title={t('No invite yet')} />
        ) : (
          <>
            <div className='space-y-2 sm:hidden'>
              {items.map((invite) => (
                <div
                  key={invite.id}
                  className='space-y-3 rounded-md border p-3'
                >
                  <div className='flex items-start justify-between gap-3'>
                    <div className='min-w-0'>
                      <p className='text-muted-foreground text-xs'>
                        {t('Invite code')}
                      </p>
                      <p className='truncate font-mono text-sm'>
                        {invite.token_prefix}…
                      </p>
                    </div>
                    <span className='bg-muted rounded-full px-2 py-1 text-xs'>
                      {t(inviteStatusLabelKey(invite.status))}
                    </span>
                  </div>
                  <dl className='grid grid-cols-2 gap-3 text-sm'>
                    <div className='min-w-0'>
                      <dt className='text-muted-foreground text-xs'>
                        {t('Shared resource')}
                      </dt>
                      <dd className='truncate'>{groupName(invite)}</dd>
                    </div>
                    <div>
                      <dt className='text-muted-foreground text-xs'>
                        {t('Uses')}
                      </dt>
                      <dd>
                        {invite.max_uses > 0
                          ? `${invite.used_count} / ${invite.max_uses}`
                          : t('Unlimited')}
                      </dd>
                    </div>
                  </dl>
                  {canRevokeInvite(invite) ? (
                    <Button
                      variant='outline'
                      size='sm'
                      className='w-full'
                      onClick={() => setRevoking(invite)}
                    >
                      {t('Revoke')}
                    </Button>
                  ) : null}
                </div>
              ))}
            </div>
            <div className='hidden sm:block'>
              <StaticDataTable
                columns={columns}
                data={items}
                getRowKey={(row) => row.id}
              />
            </div>
          </>
        )}

        <ConfirmDialog
          open={revoking !== null}
          onOpenChange={(next) => {
            if (!next) setRevoking(null)
          }}
          title={t('Revoke invite')}
          desc={t(
            'The code stops working and grants created by it are revoked.'
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
