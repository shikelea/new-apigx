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
import { useTranslation } from 'react-i18next'

import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import type { ResourceAccessKind } from '../lib/apigx-sources'
import { RESOURCE_SCOPES } from '../lib/resource-scopes'

export function ResourceScopeTabs(props: {
  value: ResourceAccessKind
  counts: Record<ResourceAccessKind, number>
  onValueChange: (value: ResourceAccessKind) => void
}) {
  const { t } = useTranslation()

  return (
    <Tabs
      value={props.value}
      onValueChange={(value) =>
        props.onValueChange((value ?? 'public') as ResourceAccessKind)
      }
    >
      <TabsList className='grid h-auto! w-full grid-cols-1 gap-2 bg-transparent p-0 sm:grid-cols-3'>
        {RESOURCE_SCOPES.map((option) => {
          const Icon = option.icon
          return (
            <TabsTrigger
              key={option.value}
              value={option.value}
              className='data-[state=active]:bg-card h-auto justify-start gap-3 rounded-xl border px-4 py-3 text-left shadow-none data-[state=active]:shadow-xs'
            >
              <Icon className='size-4 shrink-0' aria-hidden />
              <span className='min-w-0 flex-1'>
                <span className='block font-medium'>{t(option.labelKey)}</span>
                <span className='text-muted-foreground mt-0.5 block text-xs font-normal'>
                  {t(option.descriptionKey)}
                </span>
              </span>
              <span className='bg-muted rounded-full px-2 py-0.5 text-xs tabular-nums'>
                {props.counts[option.value]}
              </span>
            </TabsTrigger>
          )
        })}
      </TabsList>
    </Tabs>
  )
}
