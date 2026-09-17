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
// 「任务中心」的对外数据结构（字段与后端 `GET /api/task/self` 一致）。

/** 一个异步任务。 */
export interface UserTask {
  id: number
  created_at: number
  updated_at: number
  task_id: string
  platform: string
  channel_id: number
  quota: number
  /** 任务类型（如 text2video） */
  action: string
  /** 状态：厂商词表 NOT_START / SUBMITTED / QUEUED / IN_PROGRESS / SUCCESS / FAILURE */
  status: string
  fail_reason: string
  submit_time: number
  start_time: number
  finish_time: number
  progress: string
}

export interface UserTaskListResponse {
  success: boolean
  message?: string
  data?: {
    items: UserTask[] | null
    total: number
    page: number
    page_size: number
  }
}