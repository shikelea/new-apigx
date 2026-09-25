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
import { Plus, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { handleServerError } from '@/lib/handle-server-error'

import { setOfferingManualPrice } from '../api'
import {
  defaultManualPriceUnit,
  MANUAL_PRICE_UNIT_VALUES_BY_DIMENSION,
  manualPriceItemsForCandidate,
  manualPriceItemsAreValid,
} from '../lib/manual-price'
import type { ManualPriceItemInput, PriceMatchCandidate } from '../types'

const DIMENSIONS = [
  { value: 0, label: 'Input tokens' },
  { value: 1, label: 'Output tokens' },
  { value: 2, label: 'Cache read tokens' },
  { value: 3, label: 'Cache write tokens' },
  { value: 4, label: 'Reasoning tokens' },
  { value: 5, label: 'Images' },
  { value: 6, label: 'Input audio' },
  { value: 7, label: 'Output audio' },
  { value: 8, label: 'Output video' },
  { value: 9, label: 'Requests' },
  { value: 10, label: 'Input characters' },
  { value: 11, label: 'Documents' },
] as const

const UNITS = [
  { value: 0, label: '1M tokens' },
  { value: 1, label: '1K tokens' },
  { value: 2, label: 'image' },
  { value: 3, label: 'second' },
  { value: 4, label: 'minute' },
  { value: 5, label: 'request' },
  { value: 6, label: '1K characters' },
  { value: 7, label: 'document' },
] as const

type EditablePriceItem = ManualPriceItemInput & { rowId: number }

export function ManualPriceDialog(props: {
  candidate: PriceMatchCandidate | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const rowSequence = useRef(0)
  const [items, setItems] = useState<EditablePriceItem[]>([])

  useEffect(() => {
    if (!props.open) return
    setItems(
      manualPriceItemsForCandidate(props.candidate).map((item) => ({
        rowId: ++rowSequence.current,
        ...item,
      }))
    )
  }, [props.candidate, props.open])

  const mutation = useMutation({
    mutationFn: () =>
      setOfferingManualPrice(
        props.candidate?.offering_id ?? 0,
        items.map(({ rowId: _rowId, ...item }) => item)
      ),
    onSuccess: () => {
      toast.success(t('share.price.manualSaved'))
      props.onOpenChange(false)
      props.onSaved()
    },
    onError: (error) => handleServerError(error),
  })

  const valid = manualPriceItemsAreValid(items)
  const unusedDimension = DIMENSIONS.find(
    (option) => !items.some((item) => item.dimension === option.value)
  )

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('share.price.manualTitle')}
      description={t('share.price.manualDescription', {
        model: props.candidate?.upstream_model_id ?? '',
      })}
      contentClassName='sm:max-w-3xl'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={!valid || mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t('share.price.saveManual')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='bg-muted/30 rounded-lg border p-3 text-sm'>
          <div className='font-medium'>{t('share.price.manualCurrency')}</div>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('share.price.manualLockNotice')}
          </p>
        </div>

        <div className='space-y-3'>
          {items.map((item) => {
            const units =
              MANUAL_PRICE_UNIT_VALUES_BY_DIMENSION[item.dimension] ?? []
            return (
              <div
                key={item.rowId}
                className='grid gap-3 rounded-lg border p-3 sm:grid-cols-[minmax(0,1.35fr)_minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end'
              >
                <div className='space-y-1.5'>
                  <Label htmlFor={`manual-price-dimension-${item.rowId}`}>
                    {t('share.price.dimension')}
                  </Label>
                  <Select
                    value={String(item.dimension)}
                    onValueChange={(next) => {
                      const dimension = Number(next)
                      setItems((current) =>
                        current.map((entry) =>
                          entry.rowId === item.rowId
                            ? {
                                ...entry,
                                dimension,
                                unit_scale: defaultManualPriceUnit(dimension),
                              }
                            : entry
                        )
                      )
                    }}
                  >
                    <SelectTrigger
                      id={`manual-price-dimension-${item.rowId}`}
                      className='w-full'
                    >
                      <SelectValue>
                        {t(
                          DIMENSIONS.find(
                            (option) => option.value === item.dimension
                          )?.label ?? 'Usage'
                        )}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {DIMENSIONS.map((option) => (
                        <SelectItem
                          key={option.value}
                          value={String(option.value)}
                          disabled={items.some(
                            (entry) =>
                              entry.rowId !== item.rowId &&
                              entry.dimension === option.value
                          )}
                        >
                          {t(option.label)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className='space-y-1.5'>
                  <Label htmlFor={`manual-price-unit-${item.rowId}`}>
                    {t('share.price.unit')}
                  </Label>
                  <Select
                    value={String(item.unit_scale)}
                    onValueChange={(next) =>
                      setItems((current) =>
                        current.map((entry) =>
                          entry.rowId === item.rowId
                            ? { ...entry, unit_scale: Number(next) }
                            : entry
                        )
                      )
                    }
                  >
                    <SelectTrigger
                      id={`manual-price-unit-${item.rowId}`}
                      className='w-full'
                    >
                      <SelectValue>
                        {t(
                          UNITS.find(
                            (option) => option.value === item.unit_scale
                          )?.label ?? 'unit'
                        )}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {UNITS.filter((option) =>
                        units.includes(option.value)
                      ).map((option) => (
                        <SelectItem
                          key={option.value}
                          value={String(option.value)}
                        >
                          {t(option.label)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className='space-y-1.5'>
                  <Label htmlFor={`manual-price-value-${item.rowId}`}>
                    {t('share.price.usdUnitPrice')}
                  </Label>
                  <Input
                    id={`manual-price-value-${item.rowId}`}
                    inputMode='decimal'
                    placeholder='0.000000'
                    value={item.usd_unit_price}
                    onChange={(event) =>
                      setItems((current) =>
                        current.map((entry) =>
                          entry.rowId === item.rowId
                            ? {
                                ...entry,
                                usd_unit_price: event.target.value,
                              }
                            : entry
                        )
                      )
                    }
                  />
                </div>

                <Button
                  type='button'
                  variant='ghost'
                  size='icon'
                  aria-label={t('share.price.removeDimension')}
                  onClick={() =>
                    setItems((current) =>
                      current.filter((entry) => entry.rowId !== item.rowId)
                    )
                  }
                >
                  <Trash2 />
                </Button>
              </div>
            )
          })}
        </div>

        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={!unusedDimension}
          onClick={() => {
            if (!unusedDimension) return
            setItems((current) => [
              ...current,
              {
                rowId: ++rowSequence.current,
                dimension: unusedDimension.value,
                unit_scale: defaultManualPriceUnit(unusedDimension.value),
                usd_unit_price: '',
              },
            ])
          }}
        >
          <Plus />
          {t('share.price.addDimension')}
        </Button>
      </div>
    </Dialog>
  )
}
