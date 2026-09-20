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
  manualPriceItemsAreValid,
  manualPriceItemsForCandidate,
  microUSDToDecimal,
  offeringAllowsManualPrice,
} from '../lib/manual-price'

describe('manual offering price validation', () => {
  it('only allows the owner workflow to edit draft or rejected offerings', () => {
    expect(offeringAllowsManualPrice({ status: 0 })).toBe(true)
    expect(offeringAllowsManualPrice({ status: 3 })).toBe(true)
    expect(offeringAllowsManualPrice({ status: 1 })).toBe(false)
    expect(offeringAllowsManualPrice({ status: 2 })).toBe(false)
    expect(offeringAllowsManualPrice(undefined)).toBe(false)
  })

  it('converts locked price dimensions back to exact editable USD strings', () => {
    expect(microUSDToDecimal(0)).toBe('0')
    expect(microUSDToDecimal(12_500_000)).toBe('12.5')
    expect(
      manualPriceItemsForCandidate({
        offering_id: 9,
        upstream_model_id: 'priced-model',
        normalized_model_id: 'priced-model',
        status: 'manual_override',
        confirmable: false,
        dimensions: [
          {
            dimension: 0,
            unit_scale: 0,
            unit_price_micro: 10_000_000,
            usd_unit_price_micro: 10_000_000,
            currency: 'USD',
          },
          {
            dimension: 1,
            unit_scale: 0,
            unit_price_micro: 50_000_001,
            usd_unit_price_micro: 50_000_001,
            currency: 'USD',
          },
        ],
      })
    ).toEqual([
      { dimension: 0, unit_scale: 0, usd_unit_price: '10' },
      { dimension: 1, unit_scale: 0, usd_unit_price: '50.000001' },
    ])
  })

  it('accepts capability-specific units and explicit zero prices', () => {
    expect(
      manualPriceItemsAreValid([
        { dimension: 0, unit_scale: 0, usd_unit_price: '1.25' },
        { dimension: 5, unit_scale: 2, usd_unit_price: '0' },
        { dimension: 8, unit_scale: 4, usd_unit_price: '4.400001' },
      ])
    ).toBe(true)
  })

  it('rejects duplicate dimensions, incompatible units, and unsafe decimals', () => {
    expect(
      manualPriceItemsAreValid([
        { dimension: 0, unit_scale: 0, usd_unit_price: '1' },
        { dimension: 0, unit_scale: 1, usd_unit_price: '2' },
      ])
    ).toBe(false)
    expect(
      manualPriceItemsAreValid([
        { dimension: 5, unit_scale: 0, usd_unit_price: '1' },
      ])
    ).toBe(false)
    expect(
      manualPriceItemsAreValid([
        { dimension: 9, unit_scale: 5, usd_unit_price: '1.0000001' },
      ])
    ).toBe(false)
  })
})
