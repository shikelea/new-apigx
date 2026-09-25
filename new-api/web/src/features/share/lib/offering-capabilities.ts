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

const capabilityOrder = ['streaming', 'vision', 'tools', 'reasoning'] as const

export function normalizeOfferingCapabilities(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.filter(
      (capability): capability is string =>
        typeof capability === 'string' && capability.trim().length > 0
    )
  }
  if (!value || typeof value !== 'object') return []

  const capabilities = value as Record<string, unknown>
  return capabilityOrder.filter(
    (capability) => capabilities[capability] === true
  )
}
