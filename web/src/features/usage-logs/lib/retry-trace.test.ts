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
import { describe, expect, it } from 'vitest'

import { formatRetryTrace, retryAttemptCount } from './retry-trace'

describe('retry trace formatting', () => {
  it('groups same-channel key attempts before the channel switch', () => {
    const attempts = [
      { channel_id: 32, key_number: 1 },
      { channel_id: 32, key_number: 2 },
      { channel_id: 32, key_number: 3 },
      { channel_id: 31, key_number: 1 },
      { channel_id: 31, key_number: 2 },
    ]

    expect(formatRetryTrace([], attempts, 'Key')).toBe(
      '#32 [Key 1, 2, 3] → #31 [Key 1, 2]'
    )
    expect(retryAttemptCount([], attempts)).toBe(5)
  })

  it('compresses legacy repeated channel ids when key details are absent', () => {
    const channels = ['32', '32', '32', '31', '31']

    expect(formatRetryTrace(channels, [], 'Key')).toBe('#32 × 3 → #31 × 2')
    expect(retryAttemptCount(channels, [])).toBe(5)
  })
})
