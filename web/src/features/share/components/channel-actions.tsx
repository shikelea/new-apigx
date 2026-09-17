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
*/import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

import { fetchUpstreamModels, setChannelCapacityPolicy } from '../api'
import { buildCapacityPolicyPayload, type LimitDimensionInput } from '../lib/capacity'
import type { LimitMode } from '../types'

const MODE_OPTIONS: { mode: LimitMode; labelKey: string }[] = [
  { mode: 0, labelKey: 'share.limitMode.unset' },
  { mode: 1, labelKey: 'share.limitMode.disabled' },
  { mode: 2, labelKey: 'share.limitMode.inherit' },
  { mode: 3, labelKey: 'share.limitMode.unlimited' },
  { mode: 4, labelKey: 'share.limitMode.value' },
]

/** 分享者对渠道的两个动作：同步上游模型、设置容量策略。 */
export function ChannelActions(props: { channelId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [syncOpen, setSyncOpen] = useState(false)
  const [concurrency, setConcurrency] = useState<LimitDimensionInput>({ mode: 0, value: '' })
  const [rpm, setRpm] = useState<LimitDimensionInput>({ mode: 0, value: '' })
  const [tpm, setTpm] = useState<LimitDimensionInput>({ mode: 0, value: '' })

  const invalidateChannelData = () => {
    void queryClient.invalidateQueries({ queryKey: ['apigx-channel-offerings', props.channelId] })
    void queryClient.invalidateQueries({ queryKey: ['apigx-channel', props.channelId] })
  }

  const syncMutation = useMutation({
    mutationFn: () => fetchUpstreamModels(props.channelId, true),
    onSuccess: () => {
      setSyncOpen(false)
      invalidateChannelData()
    },
  })

  const policyMutation = useMutation({
    mutationFn: () => setChannelCapacityPolicy(props.channelId, buildCapacityPolicyPayload({ concurrency, rpm, tpm })),
    onSuccess: invalidateChannelData,
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('share.actions')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-6'>
        <div>
          <Button variant='outline' onClick={() => setSyncOpen(true)}>
            {t('share.actions.syncModels')}
          </Button>
          <p className='text-muted-foreground mt-2 text-xs'>{t('share.actions.syncModelsHint')}</p>
        </div>

        <div className='space-y-4'>
          <div className='text-sm font-medium'>{t('share.actions.capacity')}</div>
          <LimitRow
            label={t('share.actions.concurrency')}
            input={concurrency}
            onChange={setConcurrency}
            valueLabel={t('share.actions.concurrencyValue')}
          />
          <LimitRow label={t('share.actions.rpm')} input={rpm} onChange={setRpm} valueLabel={t('share.actions.rpmValue')} />
          <LimitRow label={t('share.actions.tpm')} input={tpm} onChange={setTpm} valueLabel={t('share.actions.tpmValue')} />
          <Button disabled={policyMutation.isPending} onClick={() => policyMutation.mutate()}>
            {t('share.actions.saveCapacity')}
          </Button>
        </div>

        <ConfirmDialog
          open={syncOpen}
          onOpenChange={setSyncOpen}
          title={t('share.actions.syncModels')}
          desc={t('share.actions.syncModelsConfirm')}
          confirmText={t('share.actions.confirm')}
          isLoading={syncMutation.isPending}
          handleConfirm={() => syncMutation.mutate()}
        />
      </CardContent>
    </Card>
  )
}

/** 单个维度：模式下拉 + （仅"明确值"时）数值输入。 */
function LimitRow(props: {
  label: string
  valueLabel: string
  input: LimitDimensionInput
  onChange: (next: LimitDimensionInput) => void
}) {
  const { t } = useTranslation()
  const modeOptions = MODE_OPTIONS.map((option) => (
    <SelectItem key={option.mode} value={String(option.mode)}>
      {t(option.labelKey)}
    </SelectItem>
  ))

  return (
    <div className='grid items-end gap-2 sm:grid-cols-3'>
      <div className='space-y-1'>
        <Label>{props.label}</Label>
        <Select
          value={String(props.input.mode)}
          onValueChange={(next) => props.onChange({ ...props.input, mode: Number(next) as LimitMode })}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>{modeOptions}</SelectContent>
        </Select>
      </div>
      {props.input.mode === 4 ? (
        <div className='space-y-1'>
          <Label>{props.valueLabel}</Label>
          <Input
            value={props.input.value}
            onChange={(event) => props.onChange({ ...props.input, value: event.target.value })}
            inputMode='numeric'
          />
        </div>
      ) : null}
    </div>
  )
}