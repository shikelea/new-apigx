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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'

import { ChannelActions } from '../components/channel-actions'

afterEach(cleanup)

describe('capacity policy layout', () => {
  it('keeps concurrency, RPM, and TPM in separate labelled controls', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <ChannelActions channelId={1} />
      </QueryClientProvider>
    )

    const concurrency = screen.getByRole('group', {
      name: 'share.actions.concurrency',
    })
    expect(
      within(concurrency).getByRole('combobox', {
        name: 'share.actions.concurrency',
      })
    ).toBeVisible()
    expect(
      screen.getByRole('group', { name: 'share.actions.rpm' })
    ).toBeVisible()
    expect(
      screen.getByRole('group', { name: 'share.actions.tpm' })
    ).toBeVisible()

    const user = userEvent.setup()
    await user.click(within(concurrency).getByRole('combobox'))
    await user.click(
      screen.getByRole('option', { name: 'share.limitMode.value' })
    )
    expect(
      within(concurrency).getByRole('textbox', {
        name: 'share.actions.concurrencyValue',
      })
    ).toBeVisible()
  })
})
