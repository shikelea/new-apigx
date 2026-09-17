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

import { isTaskTerminal, taskStatusLabelKey } from '../lib/status'

describe('任务状态文案映射', () => {
  it('厂商词表的六个状态都有对应文案键', () => {
    expect(taskStatusLabelKey('NOT_START')).toBe('taskCenter.status.notStart')
    expect(taskStatusLabelKey('SUBMITTED')).toBe('taskCenter.status.submitted')
    expect(taskStatusLabelKey('QUEUED')).toBe('taskCenter.status.queued')
    expect(taskStatusLabelKey('IN_PROGRESS')).toBe('taskCenter.status.inProgress')
    expect(taskStatusLabelKey('SUCCESS')).toBe('taskCenter.status.success')
    expect(taskStatusLabelKey('FAILURE')).toBe('taskCenter.status.failure')
  })

  it('未知状态返回空串（由界面显示原值，不静默变成空白）', () => {
    expect(taskStatusLabelKey('SOMETHING_NEW')).toBe('')
    expect(taskStatusLabelKey('')).toBe('')
  })
})

describe('任务终态判定', () => {
  it('成功与失败是终态', () => {
    expect(isTaskTerminal('SUCCESS')).toBe(true)
    expect(isTaskTerminal('FAILURE')).toBe(true)
  })

  it('进行中的状态不是终态（不得据此显示结果）', () => {
    expect(isTaskTerminal('QUEUED')).toBe(false)
    expect(isTaskTerminal('IN_PROGRESS')).toBe(false)
    expect(isTaskTerminal('SUBMITTED')).toBe(false)
    expect(isTaskTerminal('NOT_START')).toBe(false)
  })
})