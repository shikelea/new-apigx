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
*/import { useMutation } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { updateShareGroup } from '../api'
import type { ShareGroup } from '../types'

/** 编辑分享分组的名称与简介（计划书 §4"分享名称、介绍"）。 */
export function GroupEditDialog(props: {
  group: ShareGroup | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  useEffect(() => {
    setName(props.group?.name ?? '')
    setDescription(props.group?.description ?? '')
  }, [props.group])

  const save = useMutation({
    mutationFn: () => updateShareGroup(props.group?.id ?? 0, { name, description }),
    onSuccess: () => {
      props.onOpenChange(false)
      props.onSaved()
    },
  })

  const invalidName = name.trim() === '' || name.trim().length > 64

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Edit share group')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={invalidName || save.isPending} onClick={() => save.mutate()}>
            {t('Save')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-2'>
          <Label>{t('Name')}</Label>
          <Input value={name} onChange={(event) => setName(event.target.value)} maxLength={64} />
          {invalidName ? <p className='text-destructive text-xs'>{t('Name is required (1-64 characters)')}</p> : null}
        </div>
        <div className='space-y-2'>
          <Label>{t('Description')}</Label>
          <Textarea
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            rows={4}
            placeholder={t('Describe what this share group provides')}
          />
        </div>
      </div>
    </Dialog>
  )
}