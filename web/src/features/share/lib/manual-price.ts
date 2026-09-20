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
import type {
  ManualPriceItemInput,
  Offering,
  PriceMatchCandidate,
} from '../types'

export const MANUAL_PRICE_UNIT_VALUES_BY_DIMENSION: Record<number, number[]> = {
  0: [0, 1],
  1: [0, 1],
  2: [0, 1],
  3: [0, 1],
  4: [0, 1],
  5: [2],
  6: [3, 4],
  7: [3, 4],
  8: [3, 4],
  9: [5],
  10: [6],
  11: [7],
}

export function defaultManualPriceUnit(dimension: number) {
  return MANUAL_PRICE_UNIT_VALUES_BY_DIMENSION[dimension]?.[0] ?? 5
}

export function offeringAllowsManualPrice(
  offering: Pick<Offering, 'status'> | undefined
) {
  return offering?.status === 0 || offering?.status === 3
}

export function microUSDToDecimal(value: number) {
  const whole = Math.trunc(value / 1_000_000)
  const fraction = String(value % 1_000_000)
    .padStart(6, '0')
    .replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : String(whole)
}

export function manualPriceItemsForCandidate(
  candidate: PriceMatchCandidate | null
): ManualPriceItemInput[] {
  if (candidate?.dimensions?.length) {
    return candidate.dimensions.map((item) => ({
      dimension: item.dimension,
      unit_scale: item.unit_scale,
      usd_unit_price: microUSDToDecimal(item.usd_unit_price_micro),
    }))
  }
  const dimensions = candidate?.expression ? [0, 1, 2, 3] : [0, 1]
  return dimensions.map((dimension) => ({
    dimension,
    unit_scale: defaultManualPriceUnit(dimension),
    usd_unit_price: '',
  }))
}

export function manualPriceItemsAreValid(items: ManualPriceItemInput[]) {
  if (items.length === 0) return false
  const dimensions = new Set<number>()
  for (const item of items) {
    if (dimensions.has(item.dimension)) return false
    dimensions.add(item.dimension)
    if (
      !MANUAL_PRICE_UNIT_VALUES_BY_DIMENSION[item.dimension]?.includes(
        item.unit_scale
      )
    ) {
      return false
    }
    const value = item.usd_unit_price.trim()
    if (!/^(?:\d+|\d*\.\d{1,6})$/.test(value)) return false
    const numeric = Number(value)
    if (!Number.isFinite(numeric) || numeric < 0 || numeric > 1000) return false
  }
  return true
}
