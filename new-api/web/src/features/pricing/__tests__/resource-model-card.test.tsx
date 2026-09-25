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
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { ResourceModelCard } from '../components/resource-model-card'
import type { ResourceModelSummary } from '../lib/apigx-sources'

const model = (overrides: Partial<ResourceModelSummary> = {}) =>
  ({
    modelID: 'example-model',
    description: 'Shared model',
    sources: [
      {
        offering_public_id: 'offering-1',
        model: 'example-model',
        channel_public_id: 'channel-1',
        owner_user_id: 1,
        access_kind: 'public',
        updated_at: 1,
      },
    ],
    quality: {
      allInsufficient: false,
      bestReliabilityLowerBoundPpm: 980_000,
      lowestLatencyMsP50: 80,
      highestOutputTokensPerSecond: 55,
    },
    ...overrides,
  }) satisfies ResourceModelSummary

describe('resource model card quality summary', () => {
  it('shows six-hour reliability, best latency and best throughput', () => {
    render(<ResourceModelCard model={model()} onOpen={() => undefined} />)

    expect(
      screen.getByText('Reliability (6h)').parentElement
    ).toHaveTextContent('98.00%')
    expect(
      screen.getByText('plaza.detail.latency').parentElement
    ).toHaveTextContent('80ms')
    expect(
      screen.getByText('Throughput short').parentElement
    ).toHaveTextContent('55.0t/s')
  })

  it('labels insufficient reliability and keeps unknown metrics neutral', () => {
    render(
      <ResourceModelCard
        model={model({
          quality: {
            allInsufficient: true,
          },
        })}
        onOpen={() => undefined}
      />
    )

    expect(
      screen.getByText('Reliability (6h)').parentElement
    ).toHaveTextContent('Not enough samples')
    expect(
      screen.getByText('plaza.detail.latency').parentElement
    ).toHaveTextContent('—')
    expect(
      screen.getByText('Throughput short').parentElement
    ).toHaveTextContent('—')
  })

  it('opens model source details from the card action', async () => {
    const onOpen = vi.fn()
    render(<ResourceModelCard model={model()} onOpen={onOpen} />)

    await userEvent.click(screen.getByRole('button', { name: 'View details' }))
    expect(onOpen).toHaveBeenCalledOnce()
  })
})
