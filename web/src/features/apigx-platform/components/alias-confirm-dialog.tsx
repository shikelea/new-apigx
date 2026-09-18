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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { confirmAliasCandidate } from '../api'
import type { AliasCandidate } from '../types'

/**
 * 确认一个别名候选：把上游模型名指到某个标准模型。
 *
 * 对外标准 ID 由管理员填：**填一个还不存在的 ID 也可以** —— 后端会顺手创建标准模型，
 * 否则"先去别处建模型再回来确认"这一步会被跳过，候选就永远堆着。
 */
export function AliasConfirmDialog(props: {
  candidate: AliasCandidate | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [apiModelID, setApiModelID] = useState('')
  const [description, setDescription] = useState('')

  useEffect(() => {
    setApiModelID(props.candidate?.normalized_model_id ?? '')
    setDescription('')
  }, [props.candidate])

  const save = useMutation({
    mutationFn: () =>
      confirmAliasCandidate(props.candidate?.id ?? 0, {
        api_model_id: apiModelID.trim(),
        description: description.trim(),
        modality_type: 1,
      }),
    onSuccess: () => {
      props.onOpenChange(false)
      props.onSaved()
    },
  })

  const invalid = apiModelID.trim() === ''

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Confirm model mapping')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={invalid || save.isPending} onClick={() => save.mutate()}>
            {t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <p className='text-muted-foreground text-xs'>
          {t('Upstream model')}: <span className='font-mono'>{props.candidate?.external_model_id ?? ''}</span>
        </p>
        <div className='space-y-2'>
          <Label>{t('Standard model ID')}</Label>
          <Input value={apiModelID} onChange={(event) => setApiModelID(event.target.value)} />
          <p className='text-muted-foreground text-xs'>{t('If it does not exist, it will be created.')}</p>
        </div>
        <div className='space-y-2'>
          <Label>{t('Description')}</Label>
          <Textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={3} />
        </div>
      </div>
    </Dialog>
  )
}
