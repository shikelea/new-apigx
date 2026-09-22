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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { BadgeCheck, CircleDollarSign, Route } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { formatMicroUSD } from '@/features/plaza/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  applyRouteRanking,
  getPriceRules,
  getReviewOfferings,
  publishOffering,
  rejectOffering,
  setManualPrice,
} from './api'
import { canSubmitManualPrice, parseUsdPerMillion } from './lib/price'
import type { PriceDimension, ReviewOffering } from './types'

export function ReviewOfferingMobileCards(props: {
  items: ReviewOffering[]
  reviewPending: boolean
  onConfigurePrice: (id: number) => void
  onPublish: (id: number) => void
  onReject: (id: number) => void
}) {
  const { t } = useTranslation()

  return (
    <section
      aria-label={t('apigxPlatform.review.queue')}
      className='space-y-3 md:hidden'
    >
      {props.items.map((row) => (
        <div className='space-y-4 rounded-lg border p-4' key={row.id}>
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0'>
              <code className='block text-xs font-semibold break-all'>
                {row.upstream_model_id}
              </code>
              <p className='text-muted-foreground mt-1 text-xs break-words'>
                {row.share_group_name} · {row.channel_name}
              </p>
            </div>
            <Badge
              className='shrink-0'
              variant={row.price_configured ? 'default' : 'warning'}
            >
              {row.price_configured ? t('Configured') : t('Price unknown')}
            </Badge>
          </div>
          <div className='space-y-1'>
            <p className='text-muted-foreground text-xs'>
              {t('apigxPlatform.review.owner')}
            </p>
            <p className='text-sm font-medium'>
              {row.owner_display_name ||
                t('Provider {{id}}', { id: row.owner_user_id })}
            </p>
          </div>
          <div className='grid grid-cols-2 gap-2'>
            <Button
              className='col-span-2'
              variant='outline'
              onClick={() => props.onConfigurePrice(row.id)}
            >
              {t('apigxPlatform.configurePrice')}
            </Button>
            <Button
              disabled={
                !row.price_configured ||
                !row.mapping_confirmed ||
                props.reviewPending
              }
              onClick={() => props.onPublish(row.id)}
            >
              {t('apigxPlatform.publish')}
            </Button>
            <Button
              variant='outline'
              disabled={props.reviewPending}
              onClick={() => props.onReject(row.id)}
            >
              {t('apigxPlatform.reject')}
            </Button>
          </div>
          {!row.mapping_confirmed ? (
            <p className='text-muted-foreground text-xs'>
              {t('identity.publishBlocked')}
            </p>
          ) : null}
        </div>
      ))}
    </section>
  )
}

export function ApigxPlatform() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selectedResourceID, setSelectedResourceID] = useState('')
  const [priceScope, setPriceScope] = useState<'offering' | 'channel'>(
    'offering'
  )
  const [inputPrice, setInputPrice] = useState('')
  const [outputPrice, setOutputPrice] = useState('')
  const [model, setModel] = useState('')
  const [strategy, setStrategy] = useState('auto')
  const [dryRun, setDryRun] = useState(true)

  const reviewQuery = useQuery({
    queryKey: ['apigx-review-offerings', 'all'],
    queryFn: () => getReviewOfferings({ all: true, pageSize: 100 }),
  })
  const resources = useMemo(
    () => reviewQuery.data?.data?.items ?? [],
    [reviewQuery.data]
  )
  const pending = resources.filter((resource) => resource.status === 1)
  const selectedResource = resources.find(
    (resource) => String(resource.id) === selectedResourceID
  )
  const scopeType = priceScope === 'offering' ? 0 : 2
  let scopeID = 0
  if (selectedResource) {
    scopeID =
      priceScope === 'offering'
        ? selectedResource.id
        : selectedResource.channel_id
  }

  const priceQuery = useQuery({
    queryKey: ['apigx-price-rules', scopeType, scopeID],
    queryFn: () => getPriceRules(scopeType, scopeID),
    enabled: scopeID > 0,
  })

  useEffect(() => {
    setInputPrice('')
    setOutputPrice('')
  }, [priceScope, selectedResourceID])

  const reviewMutation = useMutation({
    mutationFn: (input: { id: number; publish: boolean }) =>
      input.publish ? publishOffering(input.id) : rejectOffering(input.id),
    onSuccess: (_, input) => {
      toast.success(
        input.publish
          ? t('apigxPlatform.review.published')
          : t('apigxPlatform.review.rejected')
      )
      void queryClient.invalidateQueries({
        queryKey: ['apigx-review-offerings'],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const savePriceMutation = useMutation({
    mutationFn: () =>
      setManualPrice({
        scope_type: scopeType,
        scope_id: scopeID,
        input_usd_per_million: parseUsdPerMillion(inputPrice) ?? 0,
        output_usd_per_million: parseUsdPerMillion(outputPrice) ?? 0,
      }),
    onSuccess: () => {
      toast.success(t('apigxPlatform.price.saved'))
      void priceQuery.refetch()
      void queryClient.invalidateQueries({
        queryKey: ['apigx-review-offerings'],
      })
    },
    onError: (error) => handleServerError(error),
  })

  const applyRankingMutation = useMutation({
    mutationFn: () => applyRouteRanking({ model, strategy, dry_run: dryRun }),
    onSuccess: () =>
      toast.success(
        dryRun
          ? t('apigxPlatform.route.previewed')
          : t('apigxPlatform.route.applied')
      ),
    onError: (error) => handleServerError(error),
  })

  const resourceOptions = resources.map((resource) => ({
    value: String(resource.id),
    label: resource.upstream_model_id,
    description: `${resource.share_group_name} · ${resource.channel_name}`,
  }))
  const modelOptions = useMemo(
    () =>
      [
        ...new Set(
          resources
            .filter((resource) => resource.status === 2)
            .map((resource) => resource.upstream_model_id)
        ),
      ]
        .sort()
        .map((name) => ({ value: name, label: name })),
    [resources]
  )

  if (reviewQuery.isPending) {
    return <LoadingState />
  }
  if (reviewQuery.isError) {
    return <ErrorState onRetry={() => void reviewQuery.refetch()} />
  }

  const reviewColumns: StaticDataTableColumn<ReviewOffering>[] = [
    {
      id: 'resource',
      header: t('apigxPlatform.review.resource'),
      className: 'min-w-56',
      cell: (row) => (
        <div>
          <code className='text-xs font-semibold'>{row.upstream_model_id}</code>
          <div className='text-muted-foreground mt-1 text-xs'>
            {row.share_group_name} · {row.channel_name}
          </div>
        </div>
      ),
    },
    {
      id: 'owner',
      header: t('apigxPlatform.review.owner'),
      cell: (row) =>
        row.owner_display_name ||
        t('Provider {{id}}', { id: row.owner_user_id }),
    },
    {
      id: 'identity',
      header: t('Model identity review'),
      cell: (row) => (
        <div className='space-y-1'>
          <Badge variant={row.mapping_confirmed ? 'default' : 'warning'}>
            {row.mapping_confirmed
              ? t('identity.confirmed')
              : t('identity.pending')}
          </Badge>
          {row.canonical_api_model_id ? (
            <code className='block text-xs break-all'>
              {row.canonical_api_model_id}
            </code>
          ) : null}
        </div>
      ),
    },
    {
      id: 'price',
      header: t('apigxPlatform.review.price'),
      cell: (row) => (
        <Badge variant={row.price_configured ? 'default' : 'warning'}>
          {row.price_configured ? t('Configured') : t('Price unknown')}
        </Badge>
      ),
    },
    {
      id: 'actions',
      header: '',
      cell: (row) => (
        <div className='flex flex-wrap justify-end gap-2'>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => {
              setSelectedResourceID(String(row.id))
              setPriceScope('offering')
            }}
          >
            {t('apigxPlatform.configurePrice')}
          </Button>
          <Button
            size='sm'
            disabled={
              !row.price_configured ||
              !row.mapping_confirmed ||
              reviewMutation.isPending
            }
            onClick={() => reviewMutation.mutate({ id: row.id, publish: true })}
          >
            {t('apigxPlatform.publish')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={reviewMutation.isPending}
            onClick={() =>
              reviewMutation.mutate({ id: row.id, publish: false })
            }
          >
            {t('apigxPlatform.reject')}
          </Button>
        </div>
      ),
    },
  ]

  const dimensions = priceQuery.data?.data?.dimensions ?? []
  const priceColumns: StaticDataTableColumn<PriceDimension>[] = [
    {
      id: 'dimension',
      header: t('apigxPlatform.dimension'),
      cell: (row) => t(row.dimension),
    },
    {
      id: 'unit_price',
      header: t('apigxPlatform.unitPrice'),
      cell: (row) =>
        row.price_known
          ? `$${formatMicroUSD(row.usd_unit_price_micro)}`
          : t('Price unknown'),
    },
  ]

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <BadgeCheck className='size-4' />
            {t('apigxPlatform.review.queue')}
          </CardTitle>
          <p className='text-muted-foreground text-sm'>
            {t('apigxPlatform.review.description')}
          </p>
        </CardHeader>
        <CardContent>
          {pending.length === 0 ? (
            <EmptyState title={t('apigxPlatform.review.empty')} />
          ) : (
            <>
              <ReviewOfferingMobileCards
                items={pending}
                reviewPending={reviewMutation.isPending}
                onConfigurePrice={(id) => {
                  setSelectedResourceID(String(id))
                  setPriceScope('offering')
                }}
                onPublish={(id) => reviewMutation.mutate({ id, publish: true })}
                onReject={(id) => reviewMutation.mutate({ id, publish: false })}
              />
              <div className='hidden md:block'>
                <StaticDataTable
                  columns={reviewColumns}
                  data={pending}
                  getRowKey={(row) => row.id}
                />
              </div>
            </>
          )}
        </CardContent>
      </Card>

      <Card id='apigx-price-editor'>
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <CircleDollarSign className='size-4' />
            {t('apigxPlatform.priceRules')}
          </CardTitle>
          <PriceEditingHint />
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label>{t('apigxPlatform.resource')}</Label>
              <Combobox
                options={resourceOptions}
                value={selectedResourceID}
                onValueChange={(value) => setSelectedResourceID(value ?? '')}
                placeholder={t('apigxPlatform.selectResource')}
                searchPlaceholder={t('Search models')}
                emptyText={t('apigxPlatform.noOffering')}
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('apigxPlatform.scope')}</Label>
              <Select
                value={priceScope}
                onValueChange={(value) =>
                  setPriceScope((value ?? 'offering') as 'offering' | 'channel')
                }
              >
                <SelectTrigger>
                  <SelectValue>
                    {t(`apigxPlatform.priceScope.${priceScope}`)}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='offering'>
                    {t('apigxPlatform.priceScope.offering')}
                  </SelectItem>
                  <SelectItem value='channel'>
                    {t('apigxPlatform.priceScope.channel')}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {scopeID > 0 && dimensions.length > 0 ? (
            <StaticDataTable
              columns={priceColumns}
              data={dimensions}
              getRowKey={(row) => row.dimension}
            />
          ) : null}
          {scopeID > 0 && !priceQuery.isFetching && dimensions.length === 0 ? (
            <EmptyState title={t('apigxPlatform.noPrice')} />
          ) : null}

          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label>{t('apigxPlatform.inputPrice')}</Label>
              <Input
                value={inputPrice}
                onChange={(event) => setInputPrice(event.target.value)}
                inputMode='decimal'
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('apigxPlatform.outputPrice')}</Label>
              <Input
                value={outputPrice}
                onChange={(event) => setOutputPrice(event.target.value)}
                inputMode='decimal'
              />
            </div>
          </div>
          <Button
            disabled={
              !selectedResource ||
              !canSubmitManualPrice({
                scopeType,
                scopeId: String(scopeID),
                inputPrice,
                outputPrice,
              }) ||
              savePriceMutation.isPending
            }
            onClick={() => savePriceMutation.mutate()}
          >
            {t('apigxPlatform.savePrice')}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <Route className='size-4' />
            {t('apigxPlatform.applyRanking')}
          </CardTitle>
          <p className='text-muted-foreground text-sm'>
            {t('apigxPlatform.route.description')}
          </p>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label>{t('apigxPlatform.model')}</Label>
              <Combobox
                options={modelOptions}
                value={model}
                onValueChange={(value) => setModel(value ?? '')}
                placeholder={t('apigxPlatform.selectModel')}
                searchPlaceholder={t('Search models')}
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('apigxPlatform.strategy')}</Label>
              <Select
                value={strategy}
                onValueChange={(value) => setStrategy(value ?? 'auto')}
              >
                <SelectTrigger>
                  <SelectValue>
                    {t(`apigxPlatform.strategy.${strategy}`)}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='auto'>
                    {t('apigxPlatform.strategy.auto')}
                  </SelectItem>
                  <SelectItem value='latency'>
                    {t('apigxPlatform.strategy.latency')}
                  </SelectItem>
                  <SelectItem value='throughput'>
                    {t('apigxPlatform.strategy.throughput')}
                  </SelectItem>
                  <SelectItem value='reliability'>
                    {t('apigxPlatform.strategy.reliability')}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div className='flex items-center gap-3'>
              <Switch
                checked={dryRun}
                onCheckedChange={setDryRun}
                id='apigx-route-dry-run'
              />
              <Label htmlFor='apigx-route-dry-run'>
                {t('apigxPlatform.dryRun')}
              </Label>
            </div>
            <Button
              disabled={model === '' || applyRankingMutation.isPending}
              onClick={() => applyRankingMutation.mutate()}
            >
              {dryRun ? t('apigxPlatform.preview') : t('apigxPlatform.apply')}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

function PriceEditingHint() {
  const { t } = useTranslation()
  return (
    <p className='text-muted-foreground text-xs'>
      {t('Prices are configured and synced in system model pricing.')}{' '}
      <Link to='/system-settings/models' className='underline'>
        {t('Open model pricing')}
      </Link>
      {' · '}
      <Link to='/pricing' className='underline'>
        {t('Price list')}
      </Link>
    </p>
  )
}
