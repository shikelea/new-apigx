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
import { render, screen } from '@testing-library/react'
import { forwardRef, type ComponentProps, type PropsWithChildren } from 'react'
import { expect, it, vi } from 'vitest'

import { DEFAULT_SYSTEM_NAME } from '@/lib/constants'

import { CTA } from '../components/sections/cta'
import { Features } from '../components/sections/features'
import { Hero } from '../components/sections/hero'
import { HowItWorks } from '../components/sections/how-it-works'
import { Stats } from '../components/sections/stats'

it('uses APIGX as the default product brand', () => {
  expect(DEFAULT_SYSTEM_NAME).toBe('APIGX')
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

vi.mock('@tanstack/react-router', () => ({
  Link: forwardRef<
    HTMLAnchorElement,
    PropsWithChildren<{ to: string } & ComponentProps<'a'>>
  >(({ to, children, ...props }, ref) => (
    <a ref={ref} href={to} {...props}>
      {children}
    </a>
  )),
}))

vi.mock('@/components/animate-in-view', () => ({
  AnimateInView: ({
    children,
    ...props
  }: PropsWithChildren<ComponentProps<'div'>>) => (
    <div {...props}>{children}</div>
  ),
}))

it('presents the shared-resource workflow and real product entry points', () => {
  const view = render(
    <>
      <Hero isAuthenticated />
      <Stats />
      <Features />
      <HowItWorks />
      <CTA isAuthenticated />
    </>
  )

  expect(
    screen.getByRole('heading', {
      level: 1,
      name: 'One platform key.Every shared AI model.',
    })
  ).toBeVisible()
  expect(
    screen.getAllByRole('button', { name: /model plaza/i })[0]
  ).toHaveAttribute('href', '/pricing')
  expect(
    screen.getAllByRole('button', { name: 'Share an API' })[0]
  ).toHaveAttribute('href', '/share')
  expect(
    screen.getByRole('button', { name: /Add upstream API/i })
  ).toHaveAttribute('href', '/share')
  expect(screen.getByRole('button', { name: 'Manage access' })).toHaveAttribute(
    'href',
    '/access'
  )
  expect(
    screen.getByText('Multi-key failover inside each channel')
  ).toBeVisible()
  expect(screen.getByText('Usage without financial settlement')).toBeVisible()
  expect(view.container).not.toHaveTextContent('upstream services integrated')
  expect(view.container).not.toHaveTextContent('Pay-as-you-go')
})

it('routes visitors to registration while keeping the plaza discoverable', () => {
  render(
    <>
      <Hero isAuthenticated={false} />
      <CTA isAuthenticated={false} />
    </>
  )

  expect(
    screen.getAllByRole('button', { name: 'Create account' })
  ).toHaveLength(2)
  for (const link of screen.getAllByRole('button', {
    name: 'Create account',
  })) {
    expect(link).toHaveAttribute('href', '/sign-up')
  }
  expect(screen.getByRole('button', { name: 'Sign In' })).toHaveAttribute(
    'href',
    '/sign-in'
  )
  expect(
    screen.getByRole('button', { name: /Browse model plaza/i })
  ).toHaveAttribute('href', '/pricing')
})
