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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout/components/public-layout'
import { PublicPageBackdrop } from '@/components/layout/components/public-page-backdrop'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  listPublicCrowdtests,
  toolsQueryKeys,
  type Crowdtest,
} from '@/features/tools/api'
import { CrowdtestCard } from '@/features/tools/components/crowdtest-panel'

// The public crowdtest board. Every entry here was published on purpose by the
// person who ran it, and carries no address or credential — so this page is a
// read-only wall of results, not a list of relays you can go and test.
export function CrowdtestPage() {
  const { t } = useTranslation()
  const [benchmark, setBenchmark] = useState('')

  const tests = useQuery({
    queryKey: [...toolsQueryKeys.publicCrowdtests, benchmark],
    queryFn: () => listPublicCrowdtests(benchmark || undefined),
  })
  const items = tests.data?.data?.items ?? []
  const disclaimer = tests.data?.data?.disclaimer ?? ''

  return (
    <PublicLayout showMainContainer={false}>
      <PublicPageBackdrop>
        <div className='mx-auto w-full max-w-[1000px] space-y-6 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div>
            <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
              {t('APIGX Tools')}
            </p>
            <h1 className='mt-1 text-3xl font-semibold tracking-tight'>
              {t('Crowdtest results')}
            </h1>
            <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-6'>
              {t(
                'Results people chose to publish. Each entry shows the model, the score and the notes only — no address and no key — so these cannot be re-run by anyone else.'
              )}
            </p>
          </div>

          <div className='flex flex-wrap items-center justify-between gap-3'>
            <ToggleGroup
              value={[benchmark]}
              onValueChange={(values) => setBenchmark(values[0] ?? '')}
              variant='outline'
              size='sm'
              aria-label={t('Benchmark')}
            >
              <ToggleGroupItem value=''>{t('All benchmarks')}</ToggleGroupItem>
              <ToggleGroupItem value='candy'>{t('Candy test')}</ToggleGroupItem>
              <ToggleGroupItem value='pelican'>
                {t('Pelican test')}
              </ToggleGroupItem>
            </ToggleGroup>
            <Button variant='outline' render={<Link to='/tools' />}>
              {t('Run your own crowdtest')}
            </Button>
          </div>

          <CrowdtestList pending={tests.isPending} items={items} />

          {disclaimer ? (
            <p className='text-muted-foreground text-xs leading-5'>
              {disclaimer}
            </p>
          ) : null}
        </div>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}

function CrowdtestList({
  pending,
  items,
}: {
  pending: boolean
  items: Crowdtest[]
}) {
  const { t } = useTranslation()
  if (pending) {
    return (
      <div className='space-y-2'>
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-20 w-full' />
      </div>
    )
  }
  if (items.length === 0) {
    return (
      <Card>
        <CardContent className='p-6 text-sm'>
          {t('No published results yet.')}
        </CardContent>
      </Card>
    )
  }
  return (
    <ul className='divide-y rounded-lg border'>
      {items.map((item) => (
        <CrowdtestCard key={item.public_id} test={item} />
      ))}
    </ul>
  )
}
