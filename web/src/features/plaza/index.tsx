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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

import { getPlaza } from './api'
import { aggregatePlazaItems, capabilityOptions, filterPlazaCards, filterPlazaCardsByCapability, groupPlazaCards } from './lib/aggregate'
import { describePlazaQuality } from './lib/format'

/** 模型广场：分享者提供的模型，按模型聚合展示可访问来源数与能力。 */
export function Plaza() {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const [capability, setCapability] = useState('')

  const query = useQuery({
    queryKey: ['apigx-plaza'],
    queryFn: () => getPlaza({ page: 1, page_size: 100 }),
  })

  const allCards = useMemo(
    () => filterPlazaCards(aggregatePlazaItems(query.data?.data?.items ?? []), keyword),
    [query.data, keyword]
  )
  const types = useMemo(() => capabilityOptions(allCards), [allCards])
  const cards = useMemo(
    () => filterPlazaCards(filterPlazaCardsByCapability(allCards, capability), keyword),
    [allCards, capability, keyword]
  )
  const groups = useMemo(() => groupPlazaCards(cards), [cards])

  if (query.isPending) {
    return <LoadingState />
  }

  if (query.isError) {
    return (
      <ErrorState
        title={t('plaza.error')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('plaza.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Select value={capability === '' ? 'all' : capability} onValueChange={(next) => setCapability(next === 'all' ? '' : (next ?? ''))}>
          <SelectTrigger className='w-44' aria-label={t('Type')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='all'>{t('All types')}</SelectItem>
            {types.map((type) => (
              <SelectItem key={type} value={type}>
                {type}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
          placeholder={t('plaza.search.placeholder')}
          className='w-64'
          aria-label={t('plaza.search.placeholder')}
        />
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <p className='text-muted-foreground mb-4 text-sm'>{t('plaza.description')}</p>
        {cards.length === 0 ? (
          <EmptyState title={t('plaza.empty')} />
        ) : (
          <div className='space-y-8'>
            {groups.map((group) => (
              <section key={group.key} className='space-y-3'>
                <h2 className='text-sm font-medium'>
                  {group.key === '' ? (
                    t('Ungrouped')
                  ) : (
                    <Link to='/plaza/group/$groupPublicId' params={{ groupPublicId: group.key }} className='hover:underline'>
                      {group.name === '' ? t('Ungrouped') : group.name}
                    </Link>
                  )}
                </h2>
                <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                  {group.cards.map((card) => (
              <Card key={card.model}>
                <CardHeader>
                  <CardTitle className='break-all text-base'>
                    <Link to='/plaza/$model' params={{ model: card.model }} className='hover:underline'>
                      {card.model}
                    </Link>
                  </CardTitle>
                </CardHeader>
                <CardContent className='space-y-3'>
                  <div className='text-muted-foreground text-sm'>
                    {t('plaza.sources', { count: card.offerings.length })}
                  </div>
                  {/* 对外标准模型 ID（仅映射已确认时才有）*/}
                  {card.offerings[0]?.canonical_model_id ? (
                    <div className='text-muted-foreground font-mono text-xs'>{card.offerings[0].canonical_model_id}</div>
                  ) : null}
                  {/* 质量摘要：取该模型**最好的一条来源**作为卡片上的代表值（样本不足/陈旧会如实显示） */}
                  {card.offerings.length > 0 ? (
                    <div className='text-muted-foreground text-xs'>
                      {t('Quality')}: {describePlazaQuality(card.offerings[0])}
                    </div>
                  ) : null}
                  {card.capabilities.length > 0 ? (
                    <div className='flex flex-wrap gap-2'>
                      {card.capabilities.map((capability) => (
                        <Badge key={capability} variant='secondary'>
                          {capability}
                        </Badge>
                      ))}
                    </div>
                  ) : null}
                </CardContent>
              </Card>
                  ))}
                </div>
              </section>
            ))}
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
