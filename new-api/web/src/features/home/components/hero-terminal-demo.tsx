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
import { ArrowRight, Check, KeyRound, Route, UsersRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

interface HeroTerminalDemoProps {
  className?: string
}

export function HeroTerminalDemo(props: HeroTerminalDemoProps) {
  const { t } = useTranslation()
  const sources = [
    {
      name: t('Public source'),
      reliability: '99.7%',
      latency: '640 ms',
      throughput: '42 tok/s',
      selected: true,
    },
    {
      name: t('plaza.scope.shared'),
      reliability: '99.2%',
      latency: '810 ms',
      throughput: '36 tok/s',
      selected: false,
    },
    {
      name: t('Private source'),
      reliability: '98.9%',
      latency: '920 ms',
      throughput: '31 tok/s',
      selected: false,
    },
  ]

  return (
    <div className={cn('mx-auto w-full max-w-xl', props.className)}>
      <div className='border-border/70 bg-card/95 overflow-hidden rounded-2xl border shadow-[0_24px_70px_-38px_rgba(15,23,42,0.38)] backdrop-blur-sm dark:shadow-[0_24px_70px_-38px_rgba(0,0,0,0.85)]'>
        <div className='border-border/60 flex items-center justify-between gap-4 border-b px-4 py-3.5 sm:px-5'>
          <div className='flex min-w-0 items-center gap-2.5'>
            <span className='flex size-8 shrink-0 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'>
              <Route className='size-4' />
            </span>
            <div className='min-w-0'>
              <p className='truncate text-sm font-semibold'>
                {t('Automatic route preview')}
              </p>
              <p className='text-muted-foreground truncate font-mono text-[11px]'>
                model: gpt-4.1-mini
              </p>
            </div>
          </div>
          <span className='bg-muted text-muted-foreground rounded-full px-2.5 py-1 text-[10px] font-semibold tracking-wide uppercase'>
            {t('Example')}
          </span>
        </div>

        <div className='bg-border/50 grid grid-cols-3 gap-px border-b'>
          {[
            [t('6h reliability'), '99.7%'],
            [t('P50 latency'), '640 ms'],
            [t('Throughput'), '42 tok/s'],
          ].map(([label, value]) => (
            <div className='bg-card min-w-0 px-3 py-3.5 sm:px-4' key={label}>
              <p className='text-muted-foreground truncate text-[10px] sm:text-[11px]'>
                {label}
              </p>
              <p className='mt-1 truncate font-mono text-xs font-semibold sm:text-sm'>
                {value}
              </p>
            </div>
          ))}
        </div>

        <div className='space-y-2 p-3 sm:p-4'>
          {sources.map((source) => (
            <div
              className={cn(
                'grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-xl border px-3 py-3 transition-colors sm:px-4',
                source.selected
                  ? 'border-emerald-500/30 bg-emerald-500/[0.06]'
                  : 'border-border/60 bg-background/60'
              )}
              key={source.name}
            >
              <div className='flex min-w-0 items-center gap-3'>
                <span
                  className={cn(
                    'flex size-8 shrink-0 items-center justify-center rounded-lg',
                    source.selected
                      ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
                      : 'bg-muted text-muted-foreground'
                  )}
                >
                  {source.selected ? (
                    <Check className='size-4' />
                  ) : (
                    <UsersRound className='size-4' />
                  )}
                </span>
                <div className='min-w-0'>
                  <p className='truncate text-xs font-medium sm:text-sm'>
                    {source.name}
                  </p>
                  <p className='text-muted-foreground mt-0.5 truncate font-mono text-[10px] sm:text-[11px]'>
                    {source.reliability} · {source.latency} ·{' '}
                    {source.throughput}
                  </p>
                </div>
              </div>
              <span
                className={cn(
                  'shrink-0 rounded-full px-2 py-1 text-[10px] font-medium',
                  source.selected
                    ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
                    : 'bg-muted text-muted-foreground'
                )}
              >
                {source.selected ? t('Selected source') : t('Available')}
              </span>
            </div>
          ))}
        </div>

        <div className='border-border/60 bg-muted/20 border-t px-4 py-3 sm:px-5'>
          <div className='text-muted-foreground flex min-w-0 flex-wrap items-center gap-1.5 text-[10px] sm:text-[11px]'>
            <KeyRound className='mr-0.5 size-3.5 shrink-0' />
            <span>{t('Retriable error')}</span>
            <ArrowRight className='size-3 shrink-0' />
            <span>{t('Try next key')}</span>
            <ArrowRight className='size-3 shrink-0' />
            <span>{t('Try next authorized source')}</span>
          </div>
        </div>
      </div>
    </div>
  )
}
