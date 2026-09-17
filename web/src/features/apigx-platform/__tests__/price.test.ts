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

import { canSubmitManualPrice, parseUsdPerMillion, PRICE_SCOPE_OPTIONS } from '../lib/price'

describe('每百万 token 价格解析', () => {
  it('合法数字被接受', () => {
    expect(parseUsdPerMillion('2.5')).toBe(2.5)
    expect(parseUsdPerMillion(' 0 ')).toBe(0)
  })

  it('空串与非法输入返回 null（绝不回退成 0）', () => {
    expect(parseUsdPerMillion('')).toBeNull()
    expect(parseUsdPerMillion('   ')).toBeNull()
    expect(parseUsdPerMillion('abc')).toBeNull()
    expect(parseUsdPerMillion('-1')).toBeNull()
    expect(parseUsdPerMillion('NaN')).toBeNull()
  })
})

describe('人工覆盖价格可提交性', () => {
  const base = { scopeType: 0, scopeId: '12', inputPrice: '1', outputPrice: '2' }

  it('作用域与两个价格都合法时可提交', () => {
    expect(canSubmitManualPrice(base)).toBe(true)
  })

  it('作用域 ID 缺失或非正数时不可提交', () => {
    expect(canSubmitManualPrice({ ...base, scopeId: '' })).toBe(false)
    expect(canSubmitManualPrice({ ...base, scopeId: '0' })).toBe(false)
    expect(canSubmitManualPrice({ ...base, scopeId: 'abc' })).toBe(false)
  })

  it('任一价格无效时不可提交', () => {
    expect(canSubmitManualPrice({ ...base, inputPrice: '' })).toBe(false)
    expect(canSubmitManualPrice({ ...base, outputPrice: 'x' })).toBe(false)
  })
})

describe('价格作用域选项', () => {
  it('与后端的三种作用域一一对应', () => {
    expect(PRICE_SCOPE_OPTIONS.map((option) => option.value)).toEqual([0, 1, 2])
  })
})