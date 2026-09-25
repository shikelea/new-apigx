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

export const MAX_UPSTREAM_KEYS_PER_BATCH = 100

export type CredentialLines = {
  keys: string[]
  duplicateCount: number
  nonEmptyCount: number
  exceedsLimit: boolean
}

export function parseCredentialLines(value: string): CredentialLines {
  const keys: string[] = []
  const seen = new Set<string>()
  let duplicateCount = 0
  let nonEmptyCount = 0
  for (const line of value.split(/\r?\n/)) {
    const key = line.trim()
    if (key === '') continue
    nonEmptyCount += 1
    if (seen.has(key)) {
      duplicateCount += 1
      continue
    }
    seen.add(key)
    keys.push(key)
  }
  return {
    keys,
    duplicateCount,
    nonEmptyCount,
    exceedsLimit: nonEmptyCount > MAX_UPSTREAM_KEYS_PER_BATCH,
  }
}

export function isAccessSecretPairOrRelayKey(value: string): boolean {
  if (value.startsWith('sk-')) return true
  const separator = value.indexOf('|')
  return (
    separator > 0 &&
    separator === value.lastIndexOf('|') &&
    value.slice(0, separator).trim() !== '' &&
    value.slice(separator + 1).trim() !== ''
  )
}
