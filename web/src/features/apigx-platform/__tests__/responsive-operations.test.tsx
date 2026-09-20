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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { AliasCandidateMobileCards } from '../components/alias-candidates-card'
import { ReviewOfferingMobileCards } from '../index'
import type { AliasCandidate, ReviewOffering } from '../types'

const reviewOffering: ReviewOffering = {
  id: 17,
  public_id: 'offering-demo',
  owner_user_id: 9,
  owner_display_name: 'Alice',
  share_group_id: 3,
  share_group_public_id: 'group-demo',
  share_group_name: 'Community pool',
  group_visibility: 1,
  channel_id: 8,
  channel_public_id: 'channel-demo',
  channel_name: 'Primary route',
  upstream_model_id: 'demo-chat-model',
  status: 1,
  revision: 1,
  price_configured: true,
  created_at: 1,
  updated_at: 1,
}

const aliasCandidate: AliasCandidate = {
  id: 23,
  external_model_id: 'vendor/demo-chat-model',
  normalized_model_id: 'demo-chat-model',
  scope_type: 2,
  scope_id: 8,
  source: 1,
  source_ref: 'channel-demo',
  confirmation_state: 0,
  target_canonical_model_id: 0,
}

it('keeps every review action in the mobile card instead of the desktop table overflow', async () => {
  const user = userEvent.setup()
  const onConfigurePrice = vi.fn()
  const onPublish = vi.fn()
  const onReject = vi.fn()

  render(
    <ReviewOfferingMobileCards
      items={[reviewOffering]}
      reviewPending={false}
      onConfigurePrice={onConfigurePrice}
      onPublish={onPublish}
      onReject={onReject}
    />
  )

  const region = screen.getByRole('region', {
    name: 'apigxPlatform.review.queue',
  })
  expect(region).toHaveClass('md:hidden')

  await user.click(
    within(region).getByRole('button', {
      name: 'apigxPlatform.configurePrice',
    })
  )
  await user.click(
    within(region).getByRole('button', { name: 'apigxPlatform.publish' })
  )
  await user.click(
    within(region).getByRole('button', { name: 'apigxPlatform.reject' })
  )

  expect(onConfigurePrice).toHaveBeenCalledWith(17)
  expect(onPublish).toHaveBeenCalledWith(17)
  expect(onReject).toHaveBeenCalledWith(17)
})

it('keeps all model-catalog actions in the mobile candidate card', async () => {
  const user = userEvent.setup()
  const onConfirm = vi.fn()
  const onBulk = vi.fn()
  const onReject = vi.fn()

  render(
    <AliasCandidateMobileCards
      items={[aliasCandidate]}
      bulkPending={false}
      rejectPending={false}
      onConfirm={onConfirm}
      onBulk={onBulk}
      onReject={onReject}
    />
  )

  const region = screen.getByRole('region', { name: 'Model catalog' })
  expect(region).toHaveClass('md:hidden')

  await user.click(within(region).getByRole('button', { name: 'Confirm' }))
  await user.click(
    within(region).getByRole('button', {
      name: 'Match all with the same name',
    })
  )
  await user.click(within(region).getByRole('button', { name: 'Reject' }))

  expect(onConfirm).toHaveBeenCalledWith(aliasCandidate)
  expect(onBulk).toHaveBeenCalledWith('demo-chat-model')
  expect(onReject).toHaveBeenCalledWith(23)
})
