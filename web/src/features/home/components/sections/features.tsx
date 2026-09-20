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
import { Activity, KeyRound, LibraryBig, Route } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

export function Features() {
  const { t } = useTranslation()
  const features = [
    {
      id: 'catalog',
      icon: LibraryBig,
      title: t('One catalog, multiple shared sources'),
      desc: t(
        'Resources for the same model are combined in one card. Compare sharers and choose automatic routing or a specific source.'
      ),
      visual: (
        <div className='mt-6 flex flex-wrap gap-2'>
          {[t('Public resources'), t('My private'), t('Shared with me')].map(
            (scope) => (
              <span
                className='border-border/60 bg-background rounded-full border px-3 py-1.5 text-[11px] font-medium'
                key={scope}
              >
                {scope}
              </span>
            )
          )}
        </div>
      ),
    },
    {
      id: 'routing',
      icon: Route,
      title: t('Routing based on real signals'),
      desc: t(
        'Automatic routing considers authorization, capacity, 6-hour reliability, P50 latency, and throughput before sending a request.'
      ),
      visual: (
        <div className='mt-6 grid grid-cols-2 gap-2'>
          {[
            t('6h reliability'),
            t('P50 latency'),
            t('Throughput'),
            t('Available capacity'),
          ].map((signal) => (
            <div
              className='border-border/50 bg-background/70 text-muted-foreground rounded-lg border px-3 py-2 text-[11px]'
              key={signal}
            >
              {signal}
            </div>
          ))}
        </div>
      ),
    },
    {
      id: 'keys',
      icon: KeyRound,
      title: t('Multi-key failover inside each channel'),
      desc: t(
        'Use weighted round robin or weighted random selection. Retriable errors try another key before moving to another authorized source.'
      ),
      visual: (
        <div className='text-muted-foreground mt-6 flex flex-wrap items-center gap-2 font-mono text-[11px]'>
          <span className='border-border/60 bg-background rounded-md border px-2.5 py-1.5'>
            Key 01 · 429
          </span>
          <span aria-hidden>→</span>
          <span className='rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1.5 text-emerald-700 dark:text-emerald-300'>
            Key 02 · 200
          </span>
          <span aria-hidden>→</span>
          <span className='border-border/60 bg-background rounded-md border px-2.5 py-1.5'>
            {t('Next source')}
          </span>
        </div>
      ),
    },
    {
      id: 'usage',
      icon: Activity,
      title: t('Usage without financial settlement'),
      desc: t(
        'Sharing is free. The platform records actual usage and estimated USD cost, while unknown prices stay unknown.'
      ),
      visual: (
        <div className='border-border/50 bg-background/70 mt-6 grid grid-cols-[1fr_auto] items-center gap-3 rounded-lg border px-3 py-2.5 text-[11px]'>
          <span className='text-muted-foreground'>{t('Estimated cost')}</span>
          <span className='font-mono font-semibold'>USD · $0.0184</span>
        </div>
      ),
    },
  ]

  return (
    <section className='relative z-10 px-4 py-20 sm:px-6 md:py-28'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='max-w-2xl'>
          <p className='text-muted-foreground mb-3 text-xs font-medium tracking-widest uppercase'>
            {t('Core workflow')}
          </p>
          <h2 className='text-2xl leading-tight font-semibold tracking-tight md:text-4xl'>
            {t('Built around how you actually use shared models')}
          </h2>
          <p className='text-muted-foreground mt-4 max-w-xl text-sm leading-6 md:text-base'>
            {t(
              'Choose what this platform key can access, then let APIGX route each request within that boundary.'
            )}
          </p>
        </AnimateInView>

        <div className='mt-12 grid gap-4 md:grid-cols-2'>
          {features.map((feature, index) => {
            const Icon = feature.icon
            return (
              <AnimateInView
                animation='fade-up'
                className='border-border/60 bg-muted/10 hover:bg-muted/20 rounded-2xl border p-6 transition-colors md:p-7'
                delay={index * 70}
                key={feature.id}
              >
                <div className='bg-background border-border/60 flex size-10 items-center justify-center rounded-xl border shadow-xs'>
                  <Icon
                    aria-hidden
                    className='text-foreground/75 size-5'
                    strokeWidth={1.6}
                  />
                </div>
                <h3 className='mt-5 text-base font-semibold'>
                  {feature.title}
                </h3>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {feature.desc}
                </p>
                {feature.visual}
              </AnimateInView>
            )
          })}
        </div>
      </div>
    </section>
  )
}
