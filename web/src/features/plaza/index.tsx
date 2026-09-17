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
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

import { getPlaza } from './api'
import { aggregatePlazaItems, filterPlazaCards } from './lib/aggregate'

/** 模型广场：分享者提供的模型，按模型聚合展示可访问来源数与能力。 */
export function Plaza() {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')

  const query = useQuery({
    queryKey: ['apigx-plaza'],
    queryFn: () => getPlaza({ page: 1, page_size: 100 }),
  })

  const cards = useMemo(
    () => filterPlazaCards(aggregatePlazaItems(query.data?.data?.items ?? []), keyword),
    [query.data, keyword]
  )

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
          <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
            {cards.map((card) => (
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
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
