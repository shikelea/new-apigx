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

import { ChannelCreateDialog } from '../components/channel-create-dialog'
import type { ChannelRecipe, ShareGroup } from '../types'

const groups: ShareGroup[] = [
  {
    id: 7,
    public_id: 'group-public-7',
    name: 'Shared lab',
    description: '',
    default_visibility: 0,
    publish_status: 0,
    updated_at: 0,
  },
]

const recipes: ChannelRecipe[] = [
  {
    id: 'openai-compatible',
    label: 'OpenAI compatible',
    protocol: 'openai',
    default_base_url: 'https://api.openai.com',
    capability_hints: ['chat', 'image', 'audio', 'embedding', 'rerank'],
    requires_base_url: true,
    supports_model_fetch: true,
  },
  {
    id: 'new-api',
    label: 'New API instance',
    protocol: 'new-api',
    default_base_url: '',
    capability_hints: ['chat', 'image', 'embedding'],
    requires_base_url: true,
    supports_model_fetch: true,
  },
]

afterEach(() => {
  vi.restoreAllMocks()
})

function renderDialog() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ChannelCreateDialog
        groups={groups}
        open
        onOpenChange={vi.fn()}
        onSaved={vi.fn()}
      />
    </QueryClientProvider>
  )
}

it('loads recipes from the server and submits only the selected stable recipe id', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: recipes } },
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { id: 19, public_id: 'channel-public-19' } },
  })
  const user = userEvent.setup()
  renderDialog()

  const recipeSelect = await screen.findByRole('combobox', {
    name: 'Upstream type',
  })
  await waitFor(() =>
    expect(recipeSelect).toHaveTextContent('OpenAI compatible')
  )
  await user.click(recipeSelect)
  await user.click(
    await screen.findByRole('option', { name: 'New API instance' })
  )
  expect(
    screen.getByText('Capabilities: Chat, Image, Embeddings')
  ).toBeVisible()
  expect(
    screen.getByText(
      'Use this only when the upstream address is another New API deployment.'
    )
  ).toBeVisible()

  await user.click(screen.getByRole('combobox', { name: 'Share group' }))
  await user.click(await screen.findByRole('option', { name: 'Shared lab' }))
  await user.type(screen.getByLabelText('Name'), 'Community New API')
  await user.type(
    screen.getByLabelText('Upstream address'),
    'https://gateway.example/v1'
  )
  await user.type(screen.getByLabelText('Your upstream key'), 'sk-community')
  await user.click(screen.getByRole('button', { name: 'Create' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/apigx/channels', {
      share_group_id: 7,
      name: 'Community New API',
      recipe_id: 'new-api',
      base_url: 'https://gateway.example/v1',
      upstream_key: 'sk-community',
    })
  )
  expect(post.mock.calls[0]?.[1]).not.toHaveProperty('protocol')
})

it('disables creation while the recipe catalog is loading', () => {
  vi.spyOn(api, 'get').mockReturnValue(new Promise(() => undefined))
  renderDialog()

  expect(screen.getByText('Loading...')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
})

it('shows a retryable error when the recipe catalog cannot be loaded', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('catalog offline'))
  renderDialog()

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Could not load channel recipes'
  )
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
})

it('keeps creation disabled when the server returns an empty recipe catalog', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [] } },
  })
  renderDialog()

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'No channel recipes are available'
  )
  expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
})
