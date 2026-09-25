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
import { useEffect, useState } from 'react'

import type { ModelPricingConfig } from '@/features/model-pricing/api'

/** 让可编辑价格表始终跟随 React Query 最新快照，同时允许保存流程局部替换基线。 */
export function usePricingBaseline(queryData?: ModelPricingConfig) {
  const [baseline, setBaseline] = useState<ModelPricingConfig | null>(null)
  useEffect(() => {
    if (queryData) setBaseline(queryData)
  }, [queryData])
  return [baseline, setBaseline] as const
}
