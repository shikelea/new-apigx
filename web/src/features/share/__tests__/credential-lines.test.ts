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

import {
  isAccessSecretPairOrRelayKey,
  MAX_UPSTREAM_KEYS_PER_BATCH,
  parseCredentialLines,
} from '../lib/credential-lines'

describe('credential line parsing', () => {
  it('trims blank lines and duplicates without splitting paired credentials', () => {
    expect(
      parseCredentialLines(
        ' sk-a \r\n\r\naccess-key|secret-key\nsk-a\n access-key|secret-key '
      )
    ).toEqual({
      keys: ['sk-a', 'access-key|secret-key'],
      duplicateCount: 2,
      nonEmptyCount: 4,
      exceedsLimit: false,
    })
  })

  it('counts submitted non-empty rows against the server batch limit', () => {
    const parsed = parseCredentialLines(
      Array.from(
        { length: MAX_UPSTREAM_KEYS_PER_BATCH + 1 },
        (_, index) => `sk-${index}`
      ).join('\n')
    )
    expect(parsed.exceedsLimit).toBe(true)
  })

  it('validates direct credential pairs as one line', () => {
    expect(isAccessSecretPairOrRelayKey('access|secret')).toBe(true)
    expect(isAccessSecretPairOrRelayKey('sk-relay')).toBe(true)
    expect(isAccessSecretPairOrRelayKey('access')).toBe(false)
    expect(isAccessSecretPairOrRelayKey('access|secret|region')).toBe(false)
  })
})
