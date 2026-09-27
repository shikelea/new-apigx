/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import { initialConnectivityTestModel } from '../lib/connectivity-test'

describe('channel connectivity test model selection', () => {
  it('selects the first testable model before a user choice', () => {
    expect(initialConnectivityTestModel('model-a', false)).toBe('model-a')
  })

  it('keeps automatic selection after the user explicitly clears the model', () => {
    expect(initialConnectivityTestModel('model-a', true)).toBeNull()
  })

  it('waits when no testable model is available', () => {
    expect(initialConnectivityTestModel('', false)).toBeNull()
  })
})
