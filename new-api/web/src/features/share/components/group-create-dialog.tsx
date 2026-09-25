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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { createShareGroup } from '../api'
import { visibilityLabelKey } from '../lib/labels'

/**
 * 新建分享分组（§4「分享分组」）。
 *
 * 可见性即访问控制的口径：公开 / 受限（凭邀请或指定用户）/ 仅自己；
 * "指定用户"与"邀请兑换"不需要在创建时选 —— 它们由后续的授权动作决定。
 */
export function GroupCreateDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [visibility, setVisibility] = useState('2')

  const create = useMutation({
    mutationFn: () =>
      createShareGroup({
        name: name.trim(),
        description: description.trim(),
        default_visibility: Number(visibility),
      }),
    onSuccess: () => {
      setName('')
      setDescription('')
      setVisibility('2')
      props.onOpenChange(false)
      props.onSaved()
    },
    onError: (error) => handleServerError(error),
  })

  const invalid = name.trim() === '' || name.trim().length > 64

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('New share group')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={invalid || create.isPending}
            onClick={() => create.mutate()}
          >
            {t('Create')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Name')}</Label>
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={64}
          />
          {invalid ? (
            <p className='text-destructive text-xs'>
              {t('Name is required (1-64 characters)')}
            </p>
          ) : null}
        </div>
        <div className='space-y-2'>
          <Label>{t('Description')}</Label>
          <Textarea
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            rows={3}
          />
        </div>
        <div className='space-y-2'>
          <Label>{t('Visibility')}</Label>
          <Select
            value={visibility}
            onValueChange={(next) => setVisibility(next ?? '2')}
          >
            <SelectTrigger aria-label={t('Visibility')}>
              <SelectValue>
                {t(visibilityLabelKey(Number(visibility)))}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='2'>{t('Public')}</SelectItem>
              <SelectItem value='1'>
                {t('Limited (invite or assigned users)')}
              </SelectItem>
              <SelectItem value='0'>{t('Only me')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
    </Dialog>
  )
}
