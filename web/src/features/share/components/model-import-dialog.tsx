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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import { createOffering, fetchUpstreamModels } from '../api'

/**
 * 「选择模型」：先**预览**上游模型清单，再挑要导入的那些。
 *
 * 为什么不直接"一键导入全部"：上游模型目录里往往混着本项目并不打算提供的模型，
 * 全导会制造一堆需要人工清理的草稿供给。这里让分享者自己勾 —— 且只对**勾选的**建草稿供给。
 */
export function ModelImportDialog(props: {
  channelId: number
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [picked, setPicked] = useState<Record<string, boolean>>({})
  // 人工录入（上游不支持下自动获取时的出路）：直接填模型名建供给
  const [manualModel, setManualModel] = useState('')

  // 只预览（import=false），不落任何数据
  const query = useQuery({
    queryKey: ['apigx-upstream-models', props.channelId],
    queryFn: () => fetchUpstreamModels(props.channelId, false),
    enabled: props.open,
  })

  const models = useMemo<string[]>(() => {
    const raw = (query.data as { data?: { models?: string[] } } | undefined)?.data?.models ?? []
    return [...raw].sort()
  }, [query.data])

  const importSelected = useMutation({
    mutationFn: async () => {
      const selected = models.filter((model) => picked[model])
      for (const model of selected) {
        await createOffering(props.channelId, model)
      }
    },
    onSuccess: () => {
      setPicked({})
      props.onOpenChange(false)
      props.onSaved()
    },
  })

  // 人工录入：与勾选导入走同一个接口，只是模型名由人填
  const createOne = useMutation({
    mutationFn: () => createOffering(props.channelId, manualModel.trim()),
    onSuccess: () => {
      setManualModel('')
      props.onOpenChange(false)
      props.onSaved()
    },
  })

  const selectedCount = models.filter((model) => picked[model]).length

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Choose models')}
      contentClassName='sm:max-w-2xl'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={selectedCount === 0 || importSelected.isPending}
            onClick={() => importSelected.mutate()}
          >
            {t('Import selected')} ({selectedCount})
          </Button>
        </>
      }
    >
      <div className='space-y-3'>
        <p className='text-muted-foreground text-sm'>
          {t('These models were reported by the upstream. Import the ones you want to share.')}
        </p>
        {query.isPending ? <LoadingState /> : null}
        {/* 抓取**失败**与"上游没返回模型"是两件事：失败必须显示真实原因（获取边界），不能混为一谈 */}
        {query.isError ? (
          <div className='space-y-2 rounded-md border border-dashed p-3'>
            <p className='text-sm font-medium'>{t('Could not fetch the model list from the upstream')}</p>
            <p className='text-muted-foreground text-xs'>
              {t('Reason')}: {String((query.error as Error)?.message ?? '')}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t('Some upstreams do not expose a model list. Add the model name manually below.')}
            </p>
          </div>
        ) : null}
        {!query.isPending && !query.isError && models.length === 0 ? (
          <EmptyState title={t('No model reported by the upstream')} />
        ) : null}
        <div className='space-y-2 border-t pt-3'>
          <Label htmlFor='manual-model'>{t('Manual entry')}</Label>
          <p className='text-muted-foreground text-xs'>
            {t('Type an upstream model ID and import it as a draft offering.')}
          </p>
          <div className='flex items-end gap-2'>
            <Input
              id='manual-model'
              value={manualModel}
              placeholder='model-id'
              onChange={(event) => setManualModel(event.target.value)}
            />
            <Button
              variant='outline'
              disabled={manualModel.trim() === '' || createOne.isPending}
              onClick={() => createOne.mutate()}
            >
              {t('Import this model')}
            </Button>
          </div>
        </div>

        <div className='max-h-80 space-y-1 overflow-y-auto'>
          {models.map((model) => (
            <div key={model} className='flex items-center justify-between gap-3 rounded-md border px-3 py-2'>
              <span className='font-mono text-xs'>{model}</span>
              <Switch
                checked={Boolean(picked[model])}
                onCheckedChange={(next) => setPicked((current) => ({ ...current, [model]: next }))}
              />
            </div>
          ))}
        </div>
      </div>
    </Dialog>
  )
}
