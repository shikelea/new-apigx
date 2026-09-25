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
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { handleServerError } from '@/lib/handle-server-error'

import { setOfferingModelMapping } from '../api'
import type { CanonicalModelOption, Offering } from '../types'

type MappingMode = 'existing' | 'new'

export function ModelMappingDialog(props: {
  offering: Offering | null
  models: CanonicalModelOption[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<MappingMode>('new')
  const [targetID, setTargetID] = useState('')

  useEffect(() => {
    const suggested = props.offering?.suggested_canonical_model_id ?? 0
    setMode(suggested > 0 ? 'existing' : 'new')
    setTargetID(suggested > 0 ? String(suggested) : '')
  }, [props.offering])

  const save = useMutation({
    mutationFn: () =>
      setOfferingModelMapping(
        props.offering?.id ?? 0,
        mode === 'existing' ? Number.parseInt(targetID, 10) : 0
      ),
    onSuccess: () => {
      toast.success(t('share.mapping.saved'))
      props.onSaved()
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error),
  })

  const options = props.models.map((model) => ({
    value: String(model.id),
    label: model.api_model_id,
    description: model.description,
  }))
  const invalid =
    !props.offering ||
    (mode === 'existing' &&
      (!Number.isFinite(Number(targetID)) || Number(targetID) <= 0))

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('share.mapping.title')}
      description={t('share.mapping.description')}
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
        <div className='bg-muted/30 rounded-lg border p-3'>
          <div className='text-muted-foreground text-xs'>
            {t('share.mapping.upstream')}
          </div>
          <code className='mt-1 block text-sm font-semibold break-all'>
            {props.offering?.upstream_model_id ?? ''}
          </code>
        </div>

        <RadioGroup
          value={mode}
          onValueChange={(value) => setMode(value as MappingMode)}
          className='grid gap-3'
        >
          <label className='hover:bg-muted/30 flex cursor-pointer gap-3 rounded-lg border p-4 transition-colors'>
            <RadioGroupItem value='existing' />
            <span className='min-w-0 flex-1'>
              <span className='block text-sm font-medium'>
                {t('share.mapping.existing')}
              </span>
              <span className='text-muted-foreground mt-1 block text-xs'>
                {t('share.mapping.existingDescription')}
              </span>
            </span>
          </label>
          {mode === 'existing' ? (
            <div className='space-y-2 pl-8'>
              <Label>{t('share.mapping.standardModel')}</Label>
              <Combobox
                options={options}
                value={targetID}
                onValueChange={(value) => setTargetID(value ?? '')}
                placeholder={t('share.mapping.selectModel')}
                searchPlaceholder={t('Search models')}
                emptyText={t('share.mapping.noModels')}
              />
            </div>
          ) : null}

          <label className='hover:bg-muted/30 flex cursor-pointer gap-3 rounded-lg border p-4 transition-colors'>
            <RadioGroupItem value='new' />
            <span className='min-w-0 flex-1'>
              <span className='block text-sm font-medium'>
                {t('share.mapping.new')}
              </span>
              <span className='text-muted-foreground mt-1 block text-xs'>
                {t('share.mapping.newDescription', {
                  model: props.offering?.upstream_model_id ?? '',
                })}
              </span>
            </span>
          </label>
        </RadioGroup>
      </div>
    </Dialog>
  )
}
