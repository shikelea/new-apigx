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
import type { PlazaOfferingItem } from '../types'

/** 测试用最小 fixture：只给用例关心的字段，其余取稳定默认值。 */
export function makeItem(overrides: Partial<PlazaOfferingItem> = {}): PlazaOfferingItem {
  return {
    offering_public_id: 'off_default',
    model: 'glm-5.3-flash',
    channel_public_id: 'ch_default',
    owner_user_id: 1,
    updated_at: 1789640000,
    ...overrides,
  }
}
