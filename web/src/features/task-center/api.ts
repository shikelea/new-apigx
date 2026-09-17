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
*/import { api } from '@/lib/api'

import type { UserTaskListResponse } from './types'

// 任务中心数据访问层。
//
// 走**用户侧**接口 `/api/task/self`（底座里管理员用的是 `/api/task`）——
// 这正是本项目此前缺的那一块：普通用户看不到自己的异步任务。
export async function getMyTasks(page = 1, pageSize = 50): Promise<UserTaskListResponse> {
  const res = await api.get('/api/task/self', { params: { p: page, page_size: pageSize } })
  return res.data
}