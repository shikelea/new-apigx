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

import { healthLabelKey, keyPoolModeLabelKey, visibilityLabelKey } from '../lib/labels'

describe('分享枚举文案映射', () => {
  it('轮询与随机分别映射到各自文案键', () => {
    expect(keyPoolModeLabelKey(0)).toBe('share.keyPool.polling')
    expect(keyPoolModeLabelKey(1)).toBe('share.keyPool.random')
  })

  it('未知 Key 池模式回退为带原值的键（不静默显示空文案）', () => {
    expect(keyPoolModeLabelKey(9)).toBe('share.keyPool.unknown:9')
  })

  it('四种可见性分别映射', () => {
    expect(visibilityLabelKey(0)).toBe('share.visibility.public')
    expect(visibilityLabelKey(1)).toBe('share.visibility.invite')
    expect(visibilityLabelKey(2)).toBe('share.visibility.selected')
    expect(visibilityLabelKey(3)).toBe('share.visibility.private')
  })

  it('未知可见性回退为带原值的键', () => {
    expect(visibilityLabelKey(7)).toBe('share.visibility.unknown:7')
  })

  it('健康状态三态映射，未知回退保留原值', () => {
    expect(healthLabelKey(0)).toBe('share.health.healthy')
    expect(healthLabelKey(1)).toBe('share.health.degraded')
    expect(healthLabelKey(2)).toBe('share.health.unavailable')
    expect(healthLabelKey(5)).toBe('share.health.unknown:5')
  })
})