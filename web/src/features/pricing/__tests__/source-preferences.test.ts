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

import { buildModelSourcePreferences } from '../lib/source-preferences'

describe('模型来源偏好', () => {
  it('指定来源只提交公开供给 ID', () => {
    expect(
      buildModelSourcePreferences('pinned', 'offering-public', false)
    ).toEqual({
      scope_mode: 2,
      pinned_offering_public_id: 'offering-public',
      default_route_mode: 'auto',
      allow_fallback: false,
    })
  })

  it('自动策略清除原固定来源并保存质量偏好', () => {
    expect(
      buildModelSourcePreferences('reliability', 'stale-source', true)
    ).toEqual({
      scope_mode: 0,
      pinned_offering_public_id: '',
      default_route_mode: 'reliability',
      allow_fallback: true,
    })
  })
})
