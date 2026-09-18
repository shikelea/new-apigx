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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { createShareChannel } from '../api'
import type { ShareGroup } from '../types'

/**
 * 新建分享渠道（§4「渠道 CRUD」）。
 *
 * 只需填配方：名称 / 协议 / 上游地址 / **自己的上游 Key** —— 平台代建底座渠道并绑定，
 * 分享者不必（也不应该）知道管理员侧的渠道 ID。
 */
export function ChannelCreateDialog(props: {
  groups: ShareGroup[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [groupId, setGroupId] = useState('')
  const [name, setName] = useState('')
  const [protocol, setProtocol] = useState('openai')
  const [baseUrl, setBaseUrl] = useState('')
  const [upstreamKey, setUpstreamKey] = useState('')

  const create = useMutation({
    mutationFn: () =>
      createShareChannel({
        share_group_id: Number(groupId),
        name: name.trim(),
        protocol,
        base_url: baseUrl.trim(),
        upstream_key: upstreamKey.trim(),
      }),
    onSuccess: () => {
      setName('')
      setBaseUrl('')
      setUpstreamKey('')
      props.onOpenChange(false)
      props.onSaved()
    },
  })

  const missing =
    groupId === '' || name.trim() === '' || baseUrl.trim() === '' || upstreamKey.trim() === ''

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('New share channel')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={missing || create.isPending} onClick={() => create.mutate()}>
            {t('Create')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Share group')}</Label>
          <select
            className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
            value={groupId}
            onChange={(event) => setGroupId(event.target.value)}
          >
            <option value=''>{t('Select a share group')}</option>
            {props.groups.map((group) => (
              <option key={group.id} value={String(group.id)}>
                {group.name}
              </option>
            ))}
          </select>
        </div>
        <div className='space-y-2'>
          <Label>{t('Name')}</Label>
          <Input value={name} onChange={(event) => setName(event.target.value)} maxLength={64} />
        </div>
        <div className='space-y-2'>
          <Label>{t('Protocol')}</Label>
          <select
            className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
            value={protocol}
            onChange={(event) => setProtocol(event.target.value)}
          >
            <option value='openai'>OpenAI</option>
            <option value='anthropic'>Anthropic</option>
            <option value='gemini'>Gemini</option>
          </select>
        </div>
        <div className='space-y-2'>
          <Label>{t('Upstream address')}</Label>
          <Input
            placeholder='https://api.example.com'
            value={baseUrl}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label>{t('Your upstream key')}</Label>
          <Input
            type='password'
            autoComplete='off'
            value={upstreamKey}
            onChange={(event) => setUpstreamKey(event.target.value)}
          />
          <p className='text-muted-foreground text-xs'>{t('It is stored encrypted and never shown again.')}</p>
        </div>
      </div>
    </Dialog>
  )
}
