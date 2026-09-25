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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { RegistrationInvitesCard } from './registration-invites-card'

beforeEach(() => {
  vi.restoreAllMocks()
})

it('creates and revokes registration invites from authentication settings', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: { en: { translation: {} } } })
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 7,
            invite_type: 0,
            token_prefix: 'existing',
            max_uses: 2,
            used_count: 1,
            status: 0,
            expires_at: 0,
            created_by_user_id: 1,
            created_at: 1,
          },
        ],
      },
    },
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => ({
    data:
      url === '/api/apigx/access/invites'
        ? {
            success: true,
            data: { id: 8, token_prefix: 'new-code', token: 'new-code-full' },
          }
        : { success: true },
  }))

  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <RegistrationInvitesCard />
      </QueryClientProvider>
    </I18nextProvider>
  )

  expect(await screen.findByText('existing…')).toBeVisible()
  await userEvent.click(
    screen.getByRole('button', { name: 'Generate registration invite' })
  )
  expect(post).toHaveBeenCalledWith(
    '/api/apigx/access/invites',
    expect.objectContaining({ invite_type: 0, max_uses: 1 })
  )
  expect(await screen.findByText('new-code-full')).toBeVisible()

  await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))
  expect(post).toHaveBeenCalledWith(
    '/api/apigx/access/invites/7/revoke?invite_type=0'
  )
})
