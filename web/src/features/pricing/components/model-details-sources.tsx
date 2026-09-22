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
import { Gauge, KeyRound, Route } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  getApiKeys,
  getPlatformKeyPreferences,
  setPlatformKeyPreferences,
} from '@/features/keys/api'
import { formatThroughput } from '@/features/performance-metrics/lib/format'
import { getRouteExplain } from '@/features/plaza/api'
import { formatMicroUSD, formatPpm } from '@/features/plaza/lib/format'
import type {
  PlazaOfferingItem,
  RouteExplainCandidate,
} from '@/features/plaza/types'
import { handleServerError } from '@/lib/handle-server-error'

import { useApigxModelSources } from '../hooks/use-apigx-model-sources'
import { dedupeModelSources } from '../lib/apigx-sources'
import { buildModelSourcePreferences } from '../lib/source-preferences'

const STRATEGIES = [
  'auto',
  'latency',
  'throughput',
  'reliability',
  'pinned',
] as const
type SourceStrategy = (typeof STRATEGIES)[number]

export function ModelDetailsSources(props: { modelName: string }) {
  const { t } = useTranslation()
  const { items } = useApigxModelSources()
  const [tokenId, setTokenId] = useState('')
  const [strategy, setStrategy] = useState<SourceStrategy>('auto')
  const [sourcePublicID, setSourcePublicID] = useState('')
  const [allowFallback, setAllowFallback] = useState(true)

  const modelName = props.modelName
  const sources = useMemo(
    () =>
      dedupeModelSources(
        items.filter(
          (item) =>
            item.model === modelName || item.canonical_model_id === modelName
        )
      ),
    [items, modelName]
  )
  const explainStrategy = strategy === 'pinned' ? 'auto' : strategy
  const explainQuery = useQuery({
    queryKey: ['apigx-route-explain', modelName, explainStrategy],
    queryFn: () => getRouteExplain(modelName, explainStrategy),
    enabled: modelName !== '' && sources.length > 0,
  })
  const keysQuery = useQuery({
    queryKey: ['api-keys', 'model-source-picker'],
    queryFn: () => getApiKeys({ p: 1, size: 100 }),
  })
  const selectedTokenId = Number.parseInt(tokenId, 10)
  const prefsQuery = useQuery({
    queryKey: ['apigx-key-preferences', selectedTokenId],
    queryFn: () => getPlatformKeyPreferences(selectedTokenId),
    enabled: Number.isFinite(selectedTokenId) && selectedTokenId > 0,
  })

  useEffect(() => {
    const prefs = prefsQuery.data?.data
    if (!prefs) return
    if (prefs.scope_mode === 2 && prefs.pinned_offering_public_id) {
      setStrategy('pinned')
      setSourcePublicID(prefs.pinned_offering_public_id)
    } else {
      const mode = STRATEGIES.includes(
        prefs.default_route_mode as SourceStrategy
      )
        ? (prefs.default_route_mode as SourceStrategy)
        : 'auto'
      setStrategy(mode === 'pinned' ? 'auto' : mode)
      setSourcePublicID('')
    }
    setAllowFallback(prefs.allow_fallback)
  }, [prefsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () =>
      setPlatformKeyPreferences(
        selectedTokenId,
        buildModelSourcePreferences(strategy, sourcePublicID, allowFallback)
      ),
    onSuccess: () => {
      toast.success(t('plaza.routing.saved'))
      void prefsQuery.refetch()
    },
    onError: (error) => handleServerError(error),
  })

  if (sources.length === 0) return null

  const explainBySource = new Map(
    (explainQuery.data?.data?.candidates ?? []).map((candidate) => [
      candidate.offering_public_id,
      candidate,
    ])
  )
  const keyOptions = (keysQuery.data?.data?.items ?? []).map((key) => ({
    value: String(key.id),
    label: key.name.trim() || `${t('share.key.unnamed')} #${String(key.id)}`,
    description: key.status === 1 ? t('Enabled') : t('Disabled'),
  }))

  return (
    <section className='space-y-4'>
      <div>
        <h3 className='text-sm font-semibold'>{t('plaza.sources.title')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('plaza.sources.description')}
        </p>
      </div>

      <RadioGroup
        value={sourcePublicID}
        onValueChange={(value) => {
          setSourcePublicID(String(value))
          setStrategy('pinned')
        }}
        className='grid gap-3 lg:grid-cols-2'
      >
        {sources.map((source) => (
          <SourceCard
            key={source.offering_public_id}
            source={source}
            explanation={explainBySource.get(source.offering_public_id)}
            selected={
              strategy === 'pinned' &&
              sourcePublicID === source.offering_public_id
            }
          />
        ))}
      </RadioGroup>

      <Card className='bg-muted/20'>
        <CardContent className='space-y-4 p-4'>
          <div className='flex items-center gap-2 text-sm font-semibold'>
            <Route className='size-4' />
            {t('plaza.routing.title')}
          </div>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label>{t('plaza.routing.apiKey')}</Label>
              <Combobox
                options={keyOptions}
                value={tokenId}
                onValueChange={(value) => {
                  setTokenId(value ?? '')
                  setStrategy('auto')
                  setSourcePublicID('')
                  setAllowFallback(true)
                }}
                placeholder={t('plaza.routing.selectKey')}
                searchPlaceholder={t('Search API keys')}
                emptyText={t('No API keys')}
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('plaza.routing.strategy')}</Label>
              <Select
                value={strategy}
                onValueChange={(value) =>
                  setStrategy((value ?? 'auto') as SourceStrategy)
                }
              >
                <SelectTrigger>
                  <SelectValue>
                    {t(`plaza.routing.strategy.${strategy}`)}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {STRATEGIES.map((value) => (
                    <SelectItem
                      key={value}
                      value={value}
                      disabled={value === 'pinned' && sourcePublicID === ''}
                    >
                      {t(`plaza.routing.strategy.${value}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div className='flex items-center gap-2'>
              <Switch
                id='plaza-source-fallback'
                checked={allowFallback}
                onCheckedChange={setAllowFallback}
              />
              <Label htmlFor='plaza-source-fallback'>
                {t('plaza.routing.allowFallback')}
              </Label>
            </div>
            <Button
              disabled={
                !Number.isFinite(selectedTokenId) ||
                selectedTokenId <= 0 ||
                prefsQuery.isFetching ||
                (strategy === 'pinned' && sourcePublicID === '') ||
                saveMutation.isPending
              }
              onClick={() => saveMutation.mutate()}
            >
              <KeyRound />
              {t('plaza.routing.save')}
            </Button>
          </div>
        </CardContent>
      </Card>
    </section>
  )
}

function SourceCard(props: {
  source: PlazaOfferingItem
  explanation?: RouteExplainCandidate
  selected: boolean
}) {
  const { t } = useTranslation()
  const sourceName =
    props.source.owner_display_name ||
    t('Provider {{id}}', { id: props.source.owner_user_id })
  const channelName =
    props.source.channel_name?.trim() || props.source.channel_public_id
  return (
    <label
      className={`cursor-pointer rounded-xl border p-4 transition-colors ${
        props.selected ? 'border-primary bg-primary/5' : 'hover:bg-muted/40'
      }`}
    >
      <div className='flex items-start gap-3'>
        <RadioGroupItem
          value={props.source.offering_public_id}
          aria-label={channelName}
        />
        <div className='min-w-0 flex-1 space-y-3'>
          <div className='flex flex-wrap items-start justify-between gap-2'>
            <div>
              <div className='font-medium'>{channelName}</div>
              <span className='text-muted-foreground text-xs'>
                {t('identity.sharer')}: {sourceName}
              </span>
            </div>
            {props.explanation?.selected ? (
              <Badge>{t('Recommended')}</Badge>
            ) : null}
          </div>
          <div className='grid grid-cols-2 gap-2 text-xs sm:grid-cols-4'>
            <SourceMetric
              label={t('Reliability (6h)')}
              value={
                props.source.insufficient
                  ? t('Not enough samples')
                  : formatPpm(
                      props.source.reliability_lower_bound_ppm ?? 0,
                      props.source.sample_count ?? 0
                    )
              }
            />
            <SourceMetric
              label={t('Latency')}
              value={
                props.source.latency_ms_p50
                  ? `${props.source.latency_ms_p50} ms`
                  : '—'
              }
            />
            <SourceMetric
              label={t('Throughput')}
              value={formatThroughput(
                props.source.output_tokens_per_second ?? 0
              )}
            />
            <SourceMetric
              label={t('Reference price')}
              value={formatSourcePrice(props.explanation, t('Price unknown'))}
            />
          </div>
          {props.explanation?.excluded_reason ? (
            <p className='text-destructive text-xs'>
              {props.explanation.excluded_reason}
            </p>
          ) : null}
        </div>
      </div>
    </label>
  )
}

function SourceMetric(props: { label: string; value: string }) {
  return (
    <div className='bg-background rounded-md border p-2'>
      <div className='text-muted-foreground'>{props.label}</div>
      <div className='mt-1 flex items-center gap-1 font-medium tabular-nums'>
        <Gauge className='size-3' />
        {props.value || '—'}
      </div>
    </div>
  )
}

function formatSourcePrice(
  candidate: RouteExplainCandidate | undefined,
  unknownLabel: string
): string {
  if (!candidate?.price_known) return unknownLabel
  return `$${formatMicroUSD(candidate.input_price_micro)} / $${formatMicroUSD(candidate.output_price_micro)}`
}
