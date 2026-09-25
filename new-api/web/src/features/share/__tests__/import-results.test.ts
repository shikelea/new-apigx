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

import { classifyImportResults } from '../lib/import-results'

describe('模型批量导入结果', () => {
  it('单个失败不会覆盖已创建与重复结果', () => {
    const results = classifyImportResults(
      ['created-model', 'duplicate-model', 'failed-model'],
      [
        { status: 'fulfilled', value: { success: true } },
        { status: 'rejected', reason: new Error('already exists') },
        { status: 'rejected', reason: new Error('upstream unavailable') },
      ],
      (error) => (error instanceof Error ? error.message : String(error))
    )

    expect(results.map((item) => item.status)).toEqual([
      'created',
      'duplicate',
      'failed',
    ])
    expect(results[2]?.message).toBe('upstream unavailable')
  })

  it('返回 success=false 时按错误信息分类而不是当成成功', () => {
    const results = classifyImportResults(
      ['duplicate-model'],
      [{ status: 'fulfilled', value: { success: false } }],
      () => 'duplicate model'
    )

    expect(results[0]?.status).toBe('duplicate')
  })
})
