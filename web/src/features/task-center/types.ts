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
// 「任务中心」的对外数据结构。
//
// **刻意复用底座的 `TaskLog`**：`/api/task/self` 返回的就是底座的任务行，
// 结果渲染（`TaskArtifactsCell`）也认这个类型 —— 再定义一份等价类型只会造成两处漂移。
import type { TaskLog } from '@/features/usage-logs/types'

export type UserTask = TaskLog

export interface UserTaskListResponse {
  success: boolean
  message?: string
  data?: {
    items: TaskLog[] | null
    total: number
    page: number
    page_size: number
  }
}