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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { PageTransition } from '@/components/page-transition'
import { Button } from '@/components/ui/button'

import { LoadingSkeleton, SearchBar } from './components'
import { ResourceModelCard } from './components/resource-model-card'
import { ResourceModelDrawer } from './components/resource-model-drawer'
import { ResourceScopeTabs } from './components/resource-scope-tabs'
import { useApigxModelSources } from './hooks/use-apigx-model-sources'
import {
  aggregateResourceModels,
  type ResourceAccessKind,
  type ResourceModelSummary,
} from './lib/apigx-sources'
import { RESOURCE_SCOPES } from './lib/resource-scopes'

export function Pricing() {
  const { t } = useTranslation()
  const [scope, setScope] = useState<ResourceAccessKind>('public')
  const [search, setSearch] = useState('')
  const [selectedModel, setSelectedModel] =
    useState<ResourceModelSummary | null>(null)
  const plaza = useApigxModelSources()

  const counts = useMemo(
    () =>
      Object.fromEntries(
        RESOURCE_SCOPES.map((option) => [
          option.value,
          aggregateResourceModels(plaza.items, option.value).length,
        ])
      ) as Record<ResourceAccessKind, number>,
    [plaza.items]
  )
  const models = useMemo(() => {
    const normalizedSearch = search.trim().toLocaleLowerCase()
    const scoped = aggregateResourceModels(plaza.items, scope)
    if (normalizedSearch === '') return scoped
    return scoped.filter(
      (model) =>
        model.modelID.toLocaleLowerCase().includes(normalizedSearch) ||
        model.canonicalModelID
          ?.toLocaleLowerCase()
          .includes(normalizedSearch) ||
        model.description?.toLocaleLowerCase().includes(normalizedSearch)
    )
  }, [plaza.items, scope, search])
  const activeScope =
    RESOURCE_SCOPES.find((option) => option.value === scope) ??
    RESOURCE_SCOPES[0]
  const ActiveScopeIcon = activeScope.icon

  if (plaza.isLoading) {
    return (
      <PublicLayout showMainContainer={false}>
        <div className='mx-auto w-full max-w-7xl px-4 pt-20 pb-10'>
          <LoadingSkeleton viewMode='card' />
        </div>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout showMainContainer={false}>
      <PageTransition className='mx-auto w-full max-w-7xl px-4 pt-20 pb-10 sm:px-6'>
        <header className='mx-auto max-w-3xl text-center'>
          <p className='text-primary text-sm font-medium'>
            {t('Shared AI resources')}
          </p>
          <h1 className='mt-2 text-3xl font-semibold tracking-tight sm:text-4xl'>
            {t('Model Square')}
          </h1>
          <p className='text-muted-foreground mx-auto mt-3 max-w-2xl text-sm leading-relaxed sm:text-base'>
            {t(
              'Browse models you can actually use, then compare sources and choose routing in model details.'
            )}
          </p>
          <SearchBar
            value={search}
            onChange={setSearch}
            onClear={() => setSearch('')}
            placeholder={t('Search model ID or description')}
            className='mx-auto mt-6 max-w-2xl'
          />
        </header>

        {plaza.isError ? (
          <div className='mt-10'>
            <ErrorState onRetry={() => void plaza.refetch()} />
          </div>
        ) : (
          <main className='mt-8'>
            <ResourceScopeTabs
              value={scope}
              counts={counts}
              onValueChange={setScope}
            />

            <div className='mt-7'>
              <h2 className='text-lg font-semibold'>
                {t(activeScope.labelKey)}
              </h2>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t('{{count}} models available in this scope', {
                  count: counts[scope],
                })}
              </p>
            </div>

            {models.length > 0 ? (
              <div className='mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3'>
                {models.map((model) => (
                  <ResourceModelCard
                    key={model.modelID}
                    model={model}
                    onOpen={() => setSelectedModel(model)}
                  />
                ))}
              </div>
            ) : (
              <div className='mt-4 flex min-h-64 flex-col items-center justify-center rounded-2xl border border-dashed p-8 text-center'>
                <ActiveScopeIcon
                  className='text-muted-foreground size-8'
                  aria-hidden
                />
                <h2 className='mt-4 font-medium'>{t('No resources found')}</h2>
                <p className='text-muted-foreground mt-1 max-w-md text-sm'>
                  {search
                    ? t('Try another model ID or clear the search.')
                    : t('No models are available in this resource scope yet.')}
                </p>
                {search ? (
                  <Button
                    variant='outline'
                    size='sm'
                    className='mt-4'
                    onClick={() => setSearch('')}
                  >
                    {t('Clear search')}
                  </Button>
                ) : null}
              </div>
            )}
          </main>
        )}

        <ResourceModelDrawer
          model={selectedModel}
          onOpenChange={(open) => {
            if (!open) setSelectedModel(null)
          }}
        />
      </PageTransition>
    </PublicLayout>
  )
}
