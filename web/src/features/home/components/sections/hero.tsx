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
import { Link } from '@tanstack/react-router'
import { ArrowRight, CircleCheck, Share2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { HeroTerminalDemo } from '../hero-terminal-demo'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

export function Hero(props: HeroProps) {
  const { t } = useTranslation()

  return (
    <section className='relative z-10 overflow-hidden px-4 pt-20 pb-14 sm:px-6 md:pt-28 md:pb-20 lg:pt-32 lg:pb-24'>
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 opacity-45 dark:opacity-20'
        style={{
          background: [
            'radial-gradient(ellipse 55% 45% at 12% 10%, oklch(0.72 0.13 226 / 45%) 0%, transparent 72%)',
            'radial-gradient(ellipse 45% 38% at 88% 18%, oklch(0.72 0.11 160 / 35%) 0%, transparent 72%)',
          ].join(', '),
        }}
      />
      <div
        aria-hidden
        className='absolute inset-0 -z-10 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_65%_55%_at_50%_20%,black_10%,transparent_100%)] bg-[size:4rem_4rem] opacity-[0.07]'
      />

      <div className='mx-auto grid max-w-6xl items-center gap-12 lg:grid-cols-[minmax(0,1fr)_minmax(0,0.92fr)] lg:gap-14'>
        <div className='min-w-0'>
          <div className='landing-animate-fade-up border-border/60 bg-background/70 text-muted-foreground inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-medium opacity-0 shadow-xs backdrop-blur-sm'>
            <Share2 className='size-3.5 text-emerald-600 dark:text-emerald-400' />
            {t('Shared AI API gateway')}
          </div>

          <h1
            className='landing-animate-fade-up mt-6 max-w-3xl text-[clamp(2.45rem,6vw,4.65rem)] leading-[1.02] font-semibold tracking-[-0.045em] opacity-0'
            style={{ animationDelay: '60ms' }}
          >
            {t('One platform key.')}
            <span className='text-muted-foreground block'>
              {t('Every shared AI model.')}
            </span>
          </h1>

          <p
            className='landing-animate-fade-up text-muted-foreground mt-6 max-w-2xl text-base leading-7 opacity-0 md:text-lg'
            style={{ animationDelay: '120ms' }}
          >
            {t(
              'Browse public, private, and shared resources from one model plaza. APIGX selects an authorized source using reliability, latency, throughput, and available capacity.'
            )}
          </p>

          <div
            className='landing-animate-fade-up mt-8 flex flex-col gap-3 opacity-0 sm:flex-row sm:flex-wrap'
            style={{ animationDelay: '180ms' }}
          >
            <Button
              className='group h-11 justify-center rounded-lg px-5 text-sm font-medium'
              render={<Link to='/pricing' />}
            >
              {t('Browse model plaza')}
              <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
            </Button>
            {props.isAuthenticated ? (
              <Button
                variant='outline'
                className='h-11 justify-center rounded-lg px-5 text-sm font-medium'
                render={<Link to='/share' />}
              >
                {t('Share an API')}
              </Button>
            ) : (
              <Button
                variant='outline'
                className='h-11 justify-center rounded-lg px-5 text-sm font-medium'
                render={<Link to='/sign-up' />}
              >
                {t('Create account')}
              </Button>
            )}
          </div>

          <div
            className='landing-animate-fade-up text-muted-foreground mt-6 flex flex-col gap-2 text-xs opacity-0 sm:flex-row sm:items-center sm:gap-5'
            style={{ animationDelay: '240ms' }}
          >
            <span className='flex items-center gap-2'>
              <CircleCheck className='size-4 text-emerald-600 dark:text-emerald-400' />
              {t('Free sharing')}
            </span>
            <span className='flex items-center gap-2'>
              <CircleCheck className='size-4 text-emerald-600 dark:text-emerald-400' />
              {t('Usage and estimated cost are recorded')}
            </span>
          </div>
        </div>

        <div
          className='landing-animate-fade-up min-w-0 opacity-0'
          style={{ animationDelay: '300ms' }}
        >
          <HeroTerminalDemo />
        </div>
      </div>
    </section>
  )
}
