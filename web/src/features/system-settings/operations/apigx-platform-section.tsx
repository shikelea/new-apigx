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
import { ApigxPlatform } from '@/features/apigx-platform'

/**
 * 平台管理（资源审核 / 模型映射 / 价格规则）—— 作为**系统设置的一个分区**，
 * 与底座的认证、计费、运维等设置并列，不再单独占一个顶级入口。
 */
export function ApigxPlatformSection() {
  return <ApigxPlatform />
}
