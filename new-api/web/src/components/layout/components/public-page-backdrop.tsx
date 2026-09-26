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
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

export function PublicPageBackdrop({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'relative min-h-[calc(100svh-4rem)] overflow-hidden',
        className
      )}
    >
      <div
        aria-hidden
        className='pointer-events-none absolute inset-x-0 top-0 h-[620px] opacity-20 dark:opacity-[0.1]'
        style={{
          background: [
            'radial-gradient(ellipse 60% 50% at 16% 14%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
            'radial-gradient(ellipse 52% 42% at 84% 10%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
            'radial-gradient(ellipse 42% 38% at 52% 68%, oklch(0.7 0.12 280 / 40%) 0%, transparent 70%)',
          ].join(', '),
          maskImage: 'linear-gradient(to bottom, black 42%, transparent 100%)',
          WebkitMaskImage:
            'linear-gradient(to bottom, black 42%, transparent 100%)',
        }}
      />
      <div className='relative'>{children}</div>
    </div>
  )
}
