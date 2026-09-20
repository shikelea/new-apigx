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
import { CopyButton } from '@/components/copy-button'
import { sideDrawerContentClassName } from '@/components/drawer-layout'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import type { ResourceModelSummary } from '../lib/apigx-sources'
import { ModelDetailsSources } from './model-details-sources'

export function ResourceModelDrawer(props: {
  model: ResourceModelSummary | null
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Sheet open={Boolean(props.model)} onOpenChange={props.onOpenChange}>
      <SheetContent
        className={sideDrawerContentClassName('sm:max-w-4xl')}
        side='right'
      >
        {props.model ? (
          <div className='flex min-h-0 flex-1 flex-col'>
            <SheetHeader className='border-b'>
              <div className='flex items-start gap-3'>
                <div className='min-w-0 flex-1'>
                  <SheetTitle className='font-mono break-all'>
                    {props.model.modelID}
                  </SheetTitle>
                  <SheetDescription className='mt-2'>
                    {props.model.description}
                  </SheetDescription>
                </div>
                <CopyButton value={props.model.modelID} />
              </div>
            </SheetHeader>
            <div className='min-h-0 flex-1 overflow-y-auto p-4 sm:p-6'>
              <ModelDetailsSources modelName={props.model.modelID} />
            </div>
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
