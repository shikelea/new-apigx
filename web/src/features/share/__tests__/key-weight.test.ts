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

import { parseKeyWeight } from '../lib/key-weight'

describe('Key 权重解析', () => {
  it('只接受 1 到 1000 的整数', () => {
    expect(parseKeyWeight('1')).toBe(1)
    expect(parseKeyWeight('1000')).toBe(1000)
    expect(parseKeyWeight('0')).toBeNull()
    expect(parseKeyWeight('1001')).toBeNull()
    expect(parseKeyWeight('1.5')).toBeNull()
  })

  it('校验值与实际提交值保持一致', () => {
    expect(parseKeyWeight('1e2')).toBe(100)
    expect(parseKeyWeight('abc')).toBeNull()
    expect(parseKeyWeight('')).toBeNull()
  })
})
