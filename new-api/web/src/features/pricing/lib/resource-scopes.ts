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
import { KeyRound, LockKeyhole, Share2 } from 'lucide-react'

import type { ResourceAccessKind } from './apigx-sources'

export const RESOURCE_SCOPES: Array<{
  value: ResourceAccessKind
  labelKey: string
  descriptionKey: string
  icon: typeof Share2
}> = [
  {
    value: 'public',
    labelKey: 'plaza.scope.public',
    descriptionKey: 'plaza.scope.public.description',
    icon: Share2,
  },
  {
    value: 'private',
    labelKey: 'plaza.scope.private',
    descriptionKey: 'plaza.scope.private.description',
    icon: LockKeyhole,
  },
  {
    value: 'shared',
    labelKey: 'plaza.scope.shared',
    descriptionKey: 'plaza.scope.shared.description',
    icon: KeyRound,
  },
]
