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

import { retentionDeadlineUnix } from '../lib/retention'

const DAY = 24 * 60 * 60

describe('保留到期时刻', () => {
  it('从完成时刻起算，按天数顺延', () => {
    expect(retentionDeadlineUnix(1_789_000_000, 7)).toBe(1_789_000_000 + 7 * DAY)
  })

  it('天数为 0 表示不作承诺（返回 null，界面留空而不是显示"立刻过期"）', () => {
    expect(retentionDeadlineUnix(1_789_000_000, 0)).toBeNull()
    expect(retentionDeadlineUnix(1_789_000_000, -1)).toBeNull()
  })

  it('任务尚未完成（没有完成时刻）时返回 null', () => {
    expect(retentionDeadlineUnix(undefined, 7)).toBeNull()
    expect(retentionDeadlineUnix(0, 7)).toBeNull()
  })
})