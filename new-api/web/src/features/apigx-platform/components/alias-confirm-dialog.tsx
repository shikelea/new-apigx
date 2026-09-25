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

import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { confirmAliasCandidate, getCanonicalModels } from '../api'
import type { AliasCandidate } from '../types'

type TargetMode = 'existing' | 'new'

const MODALITIES = [
  { value: 1, key: 'identity.modality.chat' },
  { value: 2, key: 'identity.modality.embedding' },
  { value: 3, key: 'identity.modality.image' },
  { value: 4, key: 'identity.modality.audioRecognition' },
  { value: 5, key: 'identity.modality.audioGeneration' },
  { value: 6, key: 'identity.modality.rerank' },
  { value: 7, key: 'identity.modality.video' },
] as const

export function AliasConfirmDialog(props: {
  candidate: AliasCandidate | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<TargetMode>('new')
  const [targetID, setTargetID] = useState('')
  const [apiModelID, setApiModelID] = useState('')
  const [description, setDescription] = useState('')
  const [modalityType, setModalityType] = useState(1)
  const modelsQuery = useQuery({
    queryKey: ['apigx-canonical-models'],
    queryFn: getCanonicalModels,
    enabled: props.open,
  })

  useEffect(() => {
    const target = props.candidate?.target_canonical_model_id ?? 0
    setMode(target > 0 ? 'existing' : 'new')
    setTargetID(target > 0 ? String(target) : '')
    setApiModelID(
      props.candidate?.target_api_model_id ||
        props.candidate?.normalized_model_id ||
        ''
    )
    setDescription('')
    setModalityType(1)
  }, [props.candidate])

  const save = useMutation({
    mutationFn: () =>
      confirmAliasCandidate(
        props.candidate?.id ?? 0,
        mode === 'existing'
          ? { target_canonical_model_id: Number.parseInt(targetID, 10) }
          : {
              api_model_id: apiModelID.trim(),
              description: description.trim(),
              modality_type: modalityType,
            }
      ),
    onSuccess: () => {
      toast.success(t('identity.mappingSaved'))
      props.onOpenChange(false)
      props.onSaved()
    },
    onError: (error) => handleServerError(error),
  })

  const options = (modelsQuery.data?.data?.items ?? []).map((model) => ({
    value: String(model.id),
    label: model.api_model_id,
    description: model.description,
  }))
  const modalityOptions = MODALITIES.map((modality) => ({
    value: String(modality.value),
    label: t(modality.key),
  }))
  const invalid =
    mode === 'existing'
      ? !Number.isFinite(Number(targetID)) || Number(targetID) <= 0
      : apiModelID.trim() === ''

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        props.candidate?.confirmation_state === 1
          ? t('identity.editTitle')
          : t('Confirm model mapping')
      }
      description={t('identity.dialogDescription')}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={invalid || save.isPending}
            onClick={() => save.mutate()}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <div className='space-y-5'>
        <div className='bg-muted/30 grid gap-3 rounded-lg border p-3 text-sm sm:grid-cols-2'>
          <div>
            <div className='text-muted-foreground text-xs'>
              {t('Upstream model')}
            </div>
            <code className='mt-1 block break-all'>
              {props.candidate?.external_model_id ?? ''}
            </code>
          </div>
          <div>
            <div className='text-muted-foreground text-xs'>
              {t('identity.source')}
            </div>
            <div className='mt-1 break-words'>
              {props.candidate?.share_group_name || '—'} ·{' '}
              {props.candidate?.channel_name || '—'}
            </div>
          </div>
        </div>

        <RadioGroup
          value={mode}
          onValueChange={(value) => setMode(value as TargetMode)}
          className='grid gap-3'
        >
          <label className='hover:bg-muted/30 flex cursor-pointer gap-3 rounded-lg border p-4 transition-colors'>
            <RadioGroupItem value='existing' />
            <span>
              <span className='block text-sm font-medium'>
                {t('identity.existing')}
              </span>
              <span className='text-muted-foreground mt-1 block text-xs'>
                {t('identity.existingDescription')}
              </span>
            </span>
          </label>
          {mode === 'existing' ? (
            <div className='space-y-2 pl-8'>
              <Label>{t('Standard model ID')}</Label>
              <Combobox
                options={options}
                value={targetID}
                onValueChange={(value) => setTargetID(value ?? '')}
                placeholder={t('identity.selectExisting')}
                searchPlaceholder={t('Search models')}
                emptyText={t('identity.noModels')}
              />
            </div>
          ) : null}

          <label className='hover:bg-muted/30 flex cursor-pointer gap-3 rounded-lg border p-4 transition-colors'>
            <RadioGroupItem value='new' />
            <span>
              <span className='block text-sm font-medium'>
                {t('identity.new')}
              </span>
              <span className='text-muted-foreground mt-1 block text-xs'>
                {t('identity.newDescription')}
              </span>
            </span>
          </label>
        </RadioGroup>

        {mode === 'new' ? (
          <div className='grid gap-4 pl-8'>
            <div className='space-y-2'>
              <Label>{t('Standard model ID')}</Label>
              <Input
                value={apiModelID}
                onChange={(event) => setApiModelID(event.target.value)}
              />
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
              <Label>{t('identity.modality')}</Label>
              <Combobox
                options={modalityOptions}
                value={String(modalityType)}
                onValueChange={(value) =>
                  setModalityType(Number.parseInt(value ?? '1', 10))
                }
                placeholder={t('identity.modality.select')}
                searchPlaceholder={t('identity.modality.search')}
                emptyText={t('identity.modality.empty')}
              />
            </div>
          </div>
        ) : null}
      </div>
    </Dialog>
  )
}
