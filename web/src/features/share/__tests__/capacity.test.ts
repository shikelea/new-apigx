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
*/import { describe, expect, it } from 'vitest'

import { buildCapacityPolicyPayload } from '../lib/capacity'

const unset = { mode: 0 as const, value: '' }

describe('容量策略载荷构造', () => {
  it('三个维度都带上模式（后端要求至少一个维度）', () => {
    const payload = buildCapacityPolicyPayload({
      concurrency: { mode: 4, value: '5' },
      rpm: unset,
      tpm: unset,
    })

    expect(payload.concurrency_mode).toBe(4)
    expect(payload.rpm_mode).toBe(0)
    expect(payload.tpm_mode).toBe(0)
  })

  it('只有"明确值"模式才带数值', () => {
    const payload = buildCapacityPolicyPayload({
      concurrency: { mode: 3, value: '100' },
      rpm: { mode: 4, value: '60' },
      tpm: unset,
    })

    expect(payload.concurrency_value).toBeUndefined()
    expect(payload.rpm_limit).toBe(60)
  })

  it('明确值但数值非法时不带数值（让后端拒绝，而不是我们猜 0）', () => {
    const payload = buildCapacityPolicyPayload({
      concurrency: { mode: 4, value: '' },
      rpm: { mode: 4, value: 'abc' },
      tpm: unset,
    })

    expect(payload.concurrency_value).toBeUndefined()
    expect(payload.rpm_limit).toBeUndefined()
  })
})