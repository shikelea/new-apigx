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
*/import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { formatMicroUSD } from '@/features/plaza/lib/format'
import { getChannelOfferings } from '@/features/share/api'
import type { Offering } from '@/features/share/types'

import { applyRouteRanking, getPriceRules, publishOffering, rejectOffering, setManualPrice } from './api'
import { AliasCandidatesCard } from './components/alias-candidates-card'
import { canSubmitManualPrice, isOfferingReviewable, parseUsdPerMillion, PRICE_SCOPE_OPTIONS } from './lib/price'
import type { PriceDimension } from './types'

/** 平台管理：价格规则（查询与人工覆盖）与路由排名应用。均为平台侧动作。 */
export function ApigxPlatform() {
  const { t } = useTranslation()

  const [scopeType, setScopeType] = useState(String(PRICE_SCOPE_OPTIONS[0].value))
  const [scopeId, setScopeId] = useState('')
  const [inputPrice, setInputPrice] = useState('')
  const [outputPrice, setOutputPrice] = useState('')

  const [model, setModel] = useState('')
  const [strategy, setStrategy] = useState('auto')
  const [dryRun, setDryRun] = useState(true)

  const [reviewChannelId, setReviewChannelId] = useState('')
  const reviewChannel = Number.parseInt(reviewChannelId.trim(), 10)
  const canReview = Number.isFinite(reviewChannel) && reviewChannel > 0

  const offeringsQuery = useQuery({
    queryKey: ['apigx-review-offerings', reviewChannel],
    queryFn: () => getChannelOfferings(reviewChannel),
    enabled: canReview,
  })

  const reviewMutation = useMutation({
    mutationFn: (input: { id: number; publish: boolean }) =>
      input.publish ? publishOffering(input.id) : rejectOffering(input.id),
    onSuccess: () => {
      void offeringsQuery.refetch()
    },
  })

  const parsedScopeId = Number.parseInt(scopeId.trim(), 10)
  const canQuery = Number.isFinite(parsedScopeId) && parsedScopeId > 0

  const priceQuery = useQuery({
    queryKey: ['apigx-price-rules', scopeType, parsedScopeId],
    queryFn: () => getPriceRules(Number(scopeType), parsedScopeId),
    enabled: canQuery,
  })

  const savePriceMutation = useMutation({
    mutationFn: () =>
      setManualPrice({
        scope_type: Number(scopeType),
        scope_id: parsedScopeId,
        input_usd_per_million: parseUsdPerMillion(inputPrice) ?? 0,
        output_usd_per_million: parseUsdPerMillion(outputPrice) ?? 0,
      }),
    onSuccess: () => {
      void priceQuery.refetch()
    },
  })

  const applyRankingMutation = useMutation({
    mutationFn: () => applyRouteRanking({ model, strategy, dry_run: dryRun }),
  })

  const offerings = offeringsQuery.data?.data?.items ?? []
  const reviewColumns: StaticDataTableColumn<Offering>[] = [
    { id: 'model', header: t('apigxPlatform.model'), cell: (row) => row.upstream_model_id },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={!isOfferingReviewable(row.status) || reviewMutation.isPending}
            onClick={() => reviewMutation.mutate({ id: row.id, publish: true })}
          >
            {t('apigxPlatform.publish')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={!isOfferingReviewable(row.status) || reviewMutation.isPending}
            onClick={() => reviewMutation.mutate({ id: row.id, publish: false })}
          >
            {t('apigxPlatform.reject')}
          </Button>
        </div>
      ),
    },
  ]

  const dimensions = priceQuery.data?.data?.dimensions ?? []
  const priceColumns: StaticDataTableColumn<PriceDimension>[] = [
    { id: 'dimension', header: t('apigxPlatform.dimension'), cell: (row) => row.dimension },
    {
      id: 'unit_price',
      header: t('apigxPlatform.unitPrice'),
      cell: (row) => (row.price_known ? formatMicroUSD(row.usd_unit_price_micro) : t('apigxPlatform.priceUnknown')),
    },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('apigxPlatform.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <Card>
            <CardHeader>
              <CardTitle>{t('apigxPlatform.priceRules')}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='grid gap-3 sm:grid-cols-3'>
                <div className='space-y-1'>
                  <Label>{t('apigxPlatform.scope')}</Label>
                  <Select value={scopeType} onValueChange={(next) => setScopeType(next ?? '0')}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PRICE_SCOPE_OPTIONS.map((option) => (
                        <SelectItem key={option.value} value={String(option.value)}>
                          {t(option.labelKey)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className='space-y-1'>
                  <Label>{t('apigxPlatform.scopeId')}</Label>
                  <Input value={scopeId} onChange={(event) => setScopeId(event.target.value)} inputMode='numeric' />
                </div>
              </div>

              {canQuery && dimensions.length > 0 ? (
                <StaticDataTable columns={priceColumns} data={dimensions} getRowKey={(row) => row.dimension} />
              ) : null}
              {canQuery && !priceQuery.isFetching && dimensions.length === 0 ? (
                <EmptyState title={t('apigxPlatform.noPrice')} />
              ) : null}

              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='space-y-1'>
                  <Label>{t('apigxPlatform.inputPrice')}</Label>
                  <Input value={inputPrice} onChange={(event) => setInputPrice(event.target.value)} inputMode='decimal' />
                </div>
                <div className='space-y-1'>
                  <Label>{t('apigxPlatform.outputPrice')}</Label>
                  <Input
                    value={outputPrice}
                    onChange={(event) => setOutputPrice(event.target.value)}
                    inputMode='decimal'
                  />
                </div>
              </div>
              <Button
                disabled={!canSubmitManualPrice({ scopeType: Number(scopeType), scopeId, inputPrice, outputPrice }) || savePriceMutation.isPending}
                onClick={() => savePriceMutation.mutate()}
              >
                {t('apigxPlatform.savePrice')}
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('apigxPlatform.applyRanking')}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='space-y-1'>
                  <Label>{t('apigxPlatform.model')}</Label>
                  <Input value={model} onChange={(event) => setModel(event.target.value)} />
                </div>
                <div className='space-y-1'>
                  <Label>{t('apigxPlatform.strategy')}</Label>
                  <Select value={strategy} onValueChange={(next) => setStrategy(next ?? 'auto')}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='auto'>{t('apigxPlatform.strategy.auto')}</SelectItem>
                      <SelectItem value='latency'>{t('apigxPlatform.strategy.latency')}</SelectItem>
                      <SelectItem value='throughput'>{t('apigxPlatform.strategy.throughput')}</SelectItem>
                      <SelectItem value='reliability'>{t('apigxPlatform.strategy.reliability')}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div className='flex items-center gap-3'>
                <Switch checked={dryRun} onCheckedChange={setDryRun} id='apigx-route-dry-run' />
                <Label htmlFor='apigx-route-dry-run'>{t('apigxPlatform.dryRun')}</Label>
              </div>
              <Button
                disabled={model.trim() === '' || applyRankingMutation.isPending}
                onClick={() => applyRankingMutation.mutate()}
              >
                {t('apigxPlatform.apply')}
              </Button>
            </CardContent>
          </Card>
          <AliasCandidatesCard />

          <Card>
            <CardHeader>
              <CardTitle>{t('apigxPlatform.review')}</CardTitle>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='space-y-1 sm:max-w-xs'>
                <Label>{t('apigxPlatform.channelId')}</Label>
                <Input
                  value={reviewChannelId}
                  onChange={(event) => setReviewChannelId(event.target.value)}
                  inputMode='numeric'
                />
              </div>
              {canReview && offerings.length === 0 && !offeringsQuery.isFetching ? (
                <EmptyState title={t('apigxPlatform.noOffering')} />
              ) : null}
              {offerings.length > 0 ? (
                <StaticDataTable columns={reviewColumns} data={offerings} getRowKey={(row) => row.id} />
              ) : null}
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}