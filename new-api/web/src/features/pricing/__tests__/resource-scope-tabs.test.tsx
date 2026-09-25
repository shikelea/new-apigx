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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { ResourceScopeTabs } from '../components/resource-scope-tabs'

it('keeps the narrow resource-scope list content-height and selectable', async () => {
  const onValueChange = vi.fn()
  render(
    <ResourceScopeTabs
      value='public'
      counts={{ public: 13, private: 0, shared: 1 }}
      onValueChange={onValueChange}
    />
  )

  const tabList = screen.getByRole('tablist')
  expect(tabList).toHaveClass('h-auto!')
  expect(screen.getAllByRole('tab')).toHaveLength(3)

  await userEvent.click(screen.getByRole('tab', { name: /plaza.scope.shared/ }))
  expect(onValueChange).toHaveBeenCalledWith('shared')
})
