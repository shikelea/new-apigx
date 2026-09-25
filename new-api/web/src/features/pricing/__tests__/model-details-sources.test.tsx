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
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ModelDetailsSources } from '../components/model-details-sources'

const useApigxModelSources = vi.hoisted(() => vi.fn())

vi.mock('../hooks/use-apigx-model-sources', () => ({
  useApigxModelSources,
}))

vi.mock('@/features/keys/api', () => ({
  getApiKeys: vi.fn().mockResolvedValue({ data: { items: [] } }),
  getPlatformKeyPreferences: vi.fn(),
  setPlatformKeyPreferences: vi.fn(),
}))

vi.mock('@/features/plaza/api', () => ({
  getRouteExplain: vi.fn().mockResolvedValue({ data: { candidates: [] } }),
}))

describe('model details source identity', () => {
  it('uses the channel name as the source title and places the sharer below it', async () => {
    useApigxModelSources.mockReturnValue({
      items: [
        {
          offering_public_id: 'offering-1',
          model: 'shared-model',
          group_name: '公益分组',
          channel_public_id: 'channel-1',
          channel_name: '公益线路',
          owner_user_id: 1,
          owner_display_name: '冰祈',
          access_kind: 'public',
          updated_at: 1,
          insufficient: true,
        },
      ],
    })
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ModelDetailsSources modelName='shared-model' />
      </QueryClientProvider>
    )

    const channelName = await screen.findByText('公益线路')
    const sharer = screen.getByText('identity.sharer: 冰祈')
    expect(
      channelName.compareDocumentPosition(sharer) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(screen.queryByText(/公益分组/)).not.toBeInTheDocument()
  })
})
