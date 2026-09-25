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
import type { RelayRetryAttempt } from '../types'

type RetryGroup = {
  channelId: string
  count: number
  keyNumbers: number[]
}

function groupConsecutiveAttempts(
  channelIds: string[],
  attempts: RelayRetryAttempt[]
): RetryGroup[] {
  const source =
    attempts.length > 0
      ? attempts
          .filter((attempt) => String(attempt.channel_id).trim() !== '')
          .map((attempt) => ({
            channelId: String(attempt.channel_id),
            keyNumber:
              typeof attempt.key_number === 'number' &&
              Number.isFinite(attempt.key_number) &&
              attempt.key_number > 0
                ? attempt.key_number
                : undefined,
          }))
      : channelIds.map((channelId) => ({
          channelId,
          keyNumber: undefined,
        }))

  const groups: RetryGroup[] = []
  for (const attempt of source) {
    const previous = groups.at(-1)
    if (previous?.channelId === attempt.channelId) {
      previous.count += 1
      if (attempt.keyNumber !== undefined) {
        previous.keyNumbers.push(attempt.keyNumber)
      }
      continue
    }
    groups.push({
      channelId: attempt.channelId,
      count: 1,
      keyNumbers: attempt.keyNumber === undefined ? [] : [attempt.keyNumber],
    })
  }
  return groups
}

export function retryAttemptCount(
  channelIds: string[],
  attempts: RelayRetryAttempt[]
): number {
  return attempts.length > 0 ? attempts.length : channelIds.length
}

export function formatRetryTrace(
  channelIds: string[],
  attempts: RelayRetryAttempt[],
  keyLabel: string
): string | undefined {
  const groups = groupConsecutiveAttempts(channelIds, attempts)
  if (groups.length === 0) return undefined

  return groups
    .map((group) => {
      if (group.keyNumbers.length > 0) {
        return `#${group.channelId} [${keyLabel} ${group.keyNumbers.join(', ')}]`
      }
      return group.count > 1
        ? `#${group.channelId} × ${group.count}`
        : `#${group.channelId}`
    })
    .join(' → ')
}
