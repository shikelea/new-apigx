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
import i18next from 'i18next'
import { afterEach, beforeAll, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { KeyPoolCard } from '../components/key-pool-card'

beforeAll(() => {
  i18next.addResources('en', 'translation', {
    'share.keyPool': 'Key pool',
    'share.keyPool.description': 'Manage credentials.',
    'share.keyPool.failoverDescription': 'Fail over to the next key.',
    'share.keyPool.empty': 'No upstream key yet',
    'share.key.batchAdd': 'Batch add keys',
    'share.key.batchAddDescription': 'One credential per line.',
    'share.key.credentials': 'Credentials',
    'share.key.batchLineHint': 'Duplicate lines are ignored.',
    'share.key.weight': 'Weight',
    'share.key.savedApplyHint': 'Saved.',
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

it('adds multiple keys in one request and keeps paired credentials intact', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { created: 2 } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <KeyPoolCard channelId={42} keys={[]} />
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Batch add keys' }))
  await user.type(
    screen.getByLabelText('Credentials'),
    'sk-primary{enter}access-key|secret-key{enter}sk-primary'
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/apigx/channels/42/keys/batch', {
      keys: ['sk-primary', 'access-key|secret-key'],
      weight: 1,
    })
  )
})
