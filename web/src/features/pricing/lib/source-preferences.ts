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
import type { PlatformKeyPreferencesPayload } from '@/features/keys/types'

export type ModelSourceStrategy =
  | 'auto'
  | 'latency'
  | 'throughput'
  | 'reliability'
  | 'pinned'

export function buildModelSourcePreferences(
  strategy: ModelSourceStrategy,
  sourcePublicID: string,
  allowFallback: boolean
): PlatformKeyPreferencesPayload {
  if (strategy === 'pinned') {
    return {
      scope_mode: 2,
      pinned_offering_public_id: sourcePublicID,
      default_route_mode: 'auto',
      allow_fallback: allowFallback,
    }
  }
  return {
    scope_mode: 0,
    pinned_offering_public_id: '',
    default_route_mode: strategy,
    allow_fallback: allowFallback,
  }
}
