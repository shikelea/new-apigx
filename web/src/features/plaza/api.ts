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

import type { GetPlazaParams, PlazaListResponse } from './types'

// 模型广场数据访问层。
//
// 只做请求与类型，不含任何展示逻辑；错误按项目约定交给 Query/Mutation 的失败回调
// （见 web/AGENTS.md 3.9），因此这里不吞异常、也不弹提示。
export async function getPlaza(params: GetPlazaParams = {}): Promise<PlazaListResponse> {
  const res = await api.get('/api/apigx/plaza', { params })
  return res.data
}