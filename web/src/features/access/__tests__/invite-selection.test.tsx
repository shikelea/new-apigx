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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { AccessInviteCard } from '../components/invite-card'

afterEach(() => {
  vi.restoreAllMocks()
})

it('uses the shared accessible Select and issues an invite for the chosen group', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [] } },
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: { id: 5, token: 'invite-once', token_prefix: 'invite' },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const user = userEvent.setup()
  const view = render(
    <QueryClientProvider client={client}>
      <AccessInviteCard
        groups={[
          {
            id: 23,
            public_id: 'group-public-23',
            name: 'Friends only',
            description: '',
            default_visibility: 1,
            publish_status: 2,
            updated_at: 0,
          },
        ]}
      />
    </QueryClientProvider>
  )

  expect(view.container.querySelector('select')).not.toBeInTheDocument()
  await user.click(screen.getByRole('combobox', { name: 'Share group' }))
  await user.click(await screen.findByRole('option', { name: 'Friends only' }))
  await user.click(screen.getByRole('button', { name: 'Issue invite' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/apigx/access/invites',
      expect.objectContaining({
        target_scope_type: 1,
        target_scope_id: 23,
        max_uses: 1,
        revoke_grants: true,
      })
    )
  )
  expect(await screen.findByText('invite-once')).toBeVisible()
})

it('does not offer revoke again after an invite has already been revoked', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 9,
            creator_user_id: 1,
            target_scope_type: 1,
            target_scope_id: 23,
            token_prefix: 'revoked',
            max_uses: 1,
            used_count: 0,
            expires_at: 0,
            status: 1,
            revoke_grants: true,
            created_at: 0,
            updated_at: 0,
          },
        ],
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })

  render(
    <QueryClientProvider client={client}>
      <AccessInviteCard
        groups={[
          {
            id: 23,
            public_id: 'group-public-23',
            name: 'Friends only',
            description: '',
            default_visibility: 1,
            publish_status: 2,
            updated_at: 0,
          },
        ]}
      />
    </QueryClientProvider>
  )

  expect(await screen.findAllByText('Revoked')).not.toHaveLength(0)
  expect(
    screen.queryByRole('button', { name: 'Revoke' })
  ).not.toBeInTheDocument()
})
