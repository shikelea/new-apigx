/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

export function initialConnectivityTestModel(
  firstTestableModel: string,
  manuallySelected: boolean
): string | null {
  if (manuallySelected || !firstTestableModel) return null
  return firstTestableModel
}
