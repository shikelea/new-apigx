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
import { ArrowRight, Check, KeyRound, ScanSearch, Send } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'
import { Button } from '@/components/ui/button'

interface CTAProps {
  className?: string
  isAuthenticated?: boolean
}

export function CTA(props: CTAProps) {
  const { t } = useTranslation()
  const steps = [
    { icon: KeyRound, text: t('Add one or multiple upstream keys') },
    { icon: ScanSearch, text: t('Fetch models and match system prices') },
    { icon: Send, text: t('Submit ready models for review') },
  ]

  return (
    <section className='relative z-10 overflow-hidden px-4 py-20 sm:px-6 md:py-28'>
      <AnimateInView
        animation='scale-in'
        className='border-border/60 bg-card relative mx-auto grid max-w-6xl overflow-hidden rounded-3xl border p-6 shadow-sm md:grid-cols-[minmax(0,1fr)_minmax(280px,0.72fr)] md:gap-12 md:p-10 lg:p-12'
      >
        <div
          aria-hidden
          className='pointer-events-none absolute inset-0 opacity-30 dark:opacity-15'
          style={{
            background:
              'radial-gradient(circle at 88% 12%, oklch(0.76 0.12 160 / 55%) 0%, transparent 38%)',
          }}
        />
        <div className='relative min-w-0'>
          <p className='text-muted-foreground text-xs font-medium tracking-widest uppercase'>
            {t('For sharers')}
          </p>
          <h2 className='mt-3 max-w-2xl text-2xl leading-tight font-semibold tracking-tight md:text-4xl'>
            {t('Have an API to share?')}
          </h2>
          <p className='text-muted-foreground mt-4 max-w-2xl text-sm leading-6 md:text-base'>
            {t(
              'Add one or multiple upstream keys, fetch models, match the current system price, and submit the ready models for review.'
            )}
          </p>

          <div className='mt-7 flex flex-col gap-3 sm:flex-row sm:flex-wrap'>
            <Button
              className='group justify-center'
              render={
                <Link to={props.isAuthenticated ? '/share' : '/sign-up'} />
              }
            >
              {props.isAuthenticated
                ? t('Add upstream API')
                : t('Create account')}
              <ArrowRight className='ml-1.5 size-4 transition-transform group-hover:translate-x-0.5' />
            </Button>
            {props.isAuthenticated ? (
              <Button
                variant='outline'
                className='justify-center'
                render={<Link to='/access' />}
              >
                {t('Manage access')}
              </Button>
            ) : (
              <Button
                variant='outline'
                className='justify-center'
                render={<Link to='/sign-in' />}
              >
                {t('Sign In')}
              </Button>
            )}
          </div>

          <div className='text-muted-foreground mt-7 flex flex-wrap gap-x-5 gap-y-2 text-xs'>
            {[t('Public'), t('Invite or specific users'), t('Private')].map(
              (scope) => (
                <span className='flex items-center gap-1.5' key={scope}>
                  <Check className='size-3.5 text-emerald-600 dark:text-emerald-400' />
                  {scope}
                </span>
              )
            )}
          </div>
        </div>

        <div className='relative mt-10 space-y-2 md:mt-0'>
          {steps.map(({ icon: Icon, text }, index) => (
            <div
              className='border-border/60 bg-background/75 flex items-center gap-3 rounded-xl border p-3.5 backdrop-blur-sm'
              key={text}
            >
              <span className='bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg'>
                <Icon className='size-4' strokeWidth={1.7} />
              </span>
              <span className='min-w-0 text-sm font-medium'>{text}</span>
              <span className='text-muted-foreground/50 ml-auto font-mono text-[10px]'>
                0{index + 1}
              </span>
            </div>
          ))}
        </div>
      </AnimateInView>
    </section>
  )
}
