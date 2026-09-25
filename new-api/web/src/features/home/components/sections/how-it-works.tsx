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
import { ArrowRight, BarChart3, KeyRound, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'
import { Button } from '@/components/ui/button'

export function HowItWorks() {
  const { t } = useTranslation()
  const steps = [
    {
      num: '01',
      title: t('Find and compare a model'),
      desc: t(
        'Browse public resources, your private resources, and resources shared with you.'
      ),
      icon: Search,
    },
    {
      num: '02',
      title: t('Create one platform key'),
      desc: t(
        'Choose its resource scope, then use automatic routing or pin a specific shared source.'
      ),
      icon: KeyRound,
    },
    {
      num: '03',
      title: t('Call and inspect usage'),
      desc: t(
        'Use the compatible endpoint for the capability and review requests, usage, quality, and estimated cost.'
      ),
      icon: BarChart3,
    },
  ]

  return (
    <section className='border-border/60 bg-muted/10 relative z-10 border-y px-4 py-20 sm:px-6 md:py-28'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='flex flex-col gap-6 md:flex-row md:items-end md:justify-between'>
          <div className='max-w-2xl'>
            <p className='text-muted-foreground mb-3 text-xs font-medium tracking-widest uppercase'>
              {t('From discovery to first request')}
            </p>
            <h2 className='text-2xl font-semibold tracking-tight md:text-4xl'>
              {t('Three steps, one clear resource boundary')}
            </h2>
          </div>
          <Button
            variant='outline'
            className='group w-full justify-center md:w-auto'
            render={<Link to='/pricing' />}
          >
            {t('Start with the model plaza')}
            <ArrowRight className='ml-1.5 size-4 transition-transform group-hover:translate-x-0.5' />
          </Button>
        </AnimateInView>

        <div className='mt-12 grid gap-4 md:grid-cols-3'>
          {steps.map((step, index) => {
            const Icon = step.icon
            return (
              <AnimateInView
                animation='fade-up'
                className='bg-background border-border/60 relative overflow-hidden rounded-2xl border p-6 md:p-7'
                delay={index * 100}
                key={step.num}
              >
                <span className='text-muted-foreground/50 absolute top-5 right-5 font-mono text-xs'>
                  {step.num}
                </span>
                <div className='bg-muted flex size-10 items-center justify-center rounded-xl'>
                  <Icon
                    aria-hidden
                    className='text-foreground/75 size-5'
                    strokeWidth={1.6}
                  />
                </div>
                <h3 className='mt-8 text-base font-semibold'>{step.title}</h3>
                <p className='text-muted-foreground mt-2 text-sm leading-6'>
                  {step.desc}
                </p>
              </AnimateInView>
            )
          })}
        </div>
      </div>
    </section>
  )
}
