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

import { UsageSummaryCard } from './usage-summary-card'

afterEach(() => {
  vi.restoreAllMocks()
})

it('shows measured usage and keeps unknown prices separate from estimated USD cost', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        group_by: 'channel',
        start: 1,
        end: 2,
        page: 1,
        page_size: 10,
        has_more: true,
        items: [
          {
            group_id: 11,
            group_key: '11',
            name: 'Primary share',
            request_count: 2,
            attempt_count: 3,
            retry_count: 1,
            succeeded_attempt_count: 2,
            failed_attempt_count: 1,
            unfinished_attempt_count: 0,
            ttft_ms_sum: 240,
            ttft_samples: 2,
            estimated_cost_usd_micro: 3500,
            estimated_cost_event_count: 1,
            pending_fx_event_count: 1,
            unpriced_event_count: 1,
            explicit_zero_event_count: 1,
            usage: [
              {
                capability: 3,
                dimension: 5,
                unit: 'image',
                quantity_milli: 2000,
                upstream_reported_milli: 0,
                local_estimated_milli: 2000,
                unknown_event_count: 0,
                unavailable_event_count: 0,
              },
            ],
          },
          {
            group_id: 12,
            group_key: '12',
            name: 'Unknown price share',
            request_count: 1,
            attempt_count: 1,
            retry_count: 0,
            succeeded_attempt_count: 1,
            failed_attempt_count: 0,
            unfinished_attempt_count: 0,
            ttft_ms_sum: 0,
            ttft_samples: 0,
            estimated_cost_usd_micro: 0,
            estimated_cost_event_count: 0,
            pending_fx_event_count: 0,
            unpriced_event_count: 1,
            explicit_zero_event_count: 0,
            usage: [],
          },
        ],
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <UsageSummaryCard />
    </QueryClientProvider>
  )

  expect(await screen.findByText('$0.003500')).toBeVisible()
  expect(screen.queryByText('$0.000000')).not.toBeInTheDocument()
  expect(screen.getAllByText('1 unpriced')).toHaveLength(2)
  expect(screen.getByText('1 pending FX')).toBeVisible()
  expect(screen.getByText('1 explicitly free')).toBeVisible()
  expect(screen.getByText('2 image')).toBeVisible()
  expect(screen.getByText('3 attempts · 1 retries')).toBeVisible()

  expect(get).toHaveBeenCalledWith(
    '/api/apigx/usage/summary',
    expect.objectContaining({
      params: expect.objectContaining({
        group_by: 'channel',
        page: 1,
        page_size: 10,
      }),
    })
  )

  await userEvent.click(screen.getByRole('button', { name: 'By key' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/apigx/usage/summary',
      expect.objectContaining({
        params: expect.objectContaining({ group_by: 'key', page: 1 }),
      })
    )
  )

  await userEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/apigx/usage/summary',
      expect.objectContaining({
        params: expect.objectContaining({ group_by: 'key', page: 2 }),
      })
    )
  )
})
