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
import { z } from 'zod'

import type { ProbeTargetInput } from '../api'

// The three tools (speed, dilution, crowdtest) all point at the same kind of
// target, so the shape, schema and request mapping live here once. Two sources
// are allowed:
//   - one of your own shared relays, where the key stays server-side; or
//   - a one-off base URL plus key, used for that single run and never stored.
export const PROBE_SOURCE_RELAY = 'relay'
export const PROBE_SOURCE_MANUAL = 'manual'

export type ProbeSource = typeof PROBE_SOURCE_RELAY | typeof PROBE_SOURCE_MANUAL

export type ProbeTargetValues = {
  source: ProbeSource
  relay_share_public_id: string
  base_url: string
  key: string
  model: string
}

// Default to the one-off target. Most people arrive here to check somebody
// else's endpoint, and defaulting to "one of your own relays" leaves anyone
// without a saved relay staring at an empty dropdown.
export const PROBE_TARGET_DEFAULTS: ProbeTargetValues = {
  source: PROBE_SOURCE_MANUAL,
  relay_share_public_id: '',
  base_url: '',
  key: '',
  model: '',
}

/** Validates whichever source the user picked; the other side stays optional. */
export function probeTargetSchema(t: (key: string) => string) {
  return z
    .object({
      source: z.enum([PROBE_SOURCE_RELAY, PROBE_SOURCE_MANUAL]),
      relay_share_public_id: z.string(),
      base_url: z.string(),
      key: z.string(),
      model: z
        .string()
        .trim()
        .min(1, t('Enter a model ID'))
        .max(191, t('Model ID is too long')),
    })
    .superRefine((values, ctx) => {
      if (values.source === PROBE_SOURCE_RELAY) {
        if (!values.relay_share_public_id) {
          ctx.addIssue({
            code: 'custom',
            path: ['relay_share_public_id'],
            message: t('Select a relay'),
          })
        }
        return
      }
      if (!values.base_url.trim()) {
        ctx.addIssue({
          code: 'custom',
          path: ['base_url'],
          message: t('Enter the upstream base URL'),
        })
      }
      if (!values.key.trim()) {
        ctx.addIssue({
          code: 'custom',
          path: ['key'],
          message: t('Enter an API key'),
        })
      }
    })
}

/** Strips the fields the chosen source does not use so the request stays minimal. */
export function toProbeTarget(values: ProbeTargetValues): ProbeTargetInput {
  if (values.source === PROBE_SOURCE_RELAY) {
    return {
      relay_share_public_id: values.relay_share_public_id,
      model: values.model.trim(),
    }
  }
  return {
    base_url: values.base_url.trim(),
    key: values.key.trim(),
    model: values.model.trim(),
  }
}
