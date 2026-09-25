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
import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { ModelPricingConfig } from '@/features/model-pricing/api'
import type { PricingOptions } from '@/features/model-pricing/pricing'

import { usePricingBaseline } from '../models/use-pricing-baseline'

function pricingConfig(version: string): ModelPricingConfig {
  return {
    entries: [],
    options: {} as PricingOptions,
    empty_version: version,
  }
}

describe('model pricing baseline', () => {
  it('replaces the displayed baseline when synchronization refetches newer pricing', async () => {
    const first = pricingConfig('before-sync')
    const after = pricingConfig('after-sync')
    const { result, rerender } = renderHook(
      ({ data }: { data?: ModelPricingConfig }) => usePricingBaseline(data),
      { initialProps: { data: first } }
    )
    await waitFor(() => expect(result.current[0]).toBe(first))

    rerender({ data: after })

    await waitFor(() => expect(result.current[0]).toBe(after))
  })
})
