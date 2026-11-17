/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import {
  PROBE_SOURCE_MANUAL,
  PROBE_TARGET_DEFAULTS,
  requestNameForTarget,
  toProbeTarget,
} from '../lib/probe-target'

// deepseek-flash 的正式名是 deepseek-v4.1-flash：同一个型号，两个请求名。
const deepseek = {
  request: 'deepseek-flash',
  aliases: ['deepseek-v4.1-flash'],
}

describe('dilution request name selection', () => {
  it('prefers the canonical name when the target serves it', () => {
    expect(requestNameForTarget(deepseek, ['deepseek-flash', 'kimi-k3'])).toBe(
      'deepseek-flash'
    )
  })

  it('falls back to an alias the target does serve', () => {
    expect(
      requestNameForTarget(deepseek, ['deepseek-v4.1-flash', 'kimi-k3'])
    ).toBe('deepseek-v4.1-flash')
  })

  it('matches served names case-insensitively and trimmed', () => {
    expect(requestNameForTarget(deepseek, [' DeepSeek-Flash '])).toBe(
      'deepseek-flash'
    )
  })

  it('keeps the canonical name when the served list is unknown or unreadable', () => {
    expect(requestNameForTarget(deepseek, null)).toBe('deepseek-flash')
    expect(requestNameForTarget(deepseek, [])).toBe('deepseek-flash')
  })

  it('returns null when the target offers none of the names', () => {
    expect(requestNameForTarget(deepseek, ['gpt-6-sol'])).toBeNull()
    expect(
      requestNameForTarget({ request: 'kimi-k3' }, ['gpt-6-sol'])
    ).toBeNull()
  })

  it('accepts a model without aliases', () => {
    expect(requestNameForTarget({ request: 'kimi-k3' }, ['kimi-k3'])).toBe(
      'kimi-k3'
    )
  })
})

describe('probe target mapping', () => {
  it('sends the baseline alongside the editable request name', () => {
    const input = toProbeTarget({
      ...PROBE_TARGET_DEFAULTS,
      source: PROBE_SOURCE_MANUAL,
      base_url: ' https://relay.example ',
      key: ' sk-x ',
      model: ' deepseek-v4.1-flash ',
      baseline: ' deepseek-flash ',
      tier: 'high',
    })
    expect(input).toEqual({
      base_url: 'https://relay.example',
      key: 'sk-x',
      model: 'deepseek-v4.1-flash',
      baseline: 'deepseek-flash',
      tier: 'high',
      concurrency: 2,
    })
  })

  it('omits an empty baseline so the server judges by the request name', () => {
    const input = toProbeTarget({
      ...PROBE_TARGET_DEFAULTS,
      source: PROBE_SOURCE_MANUAL,
      base_url: 'https://relay.example',
      key: 'sk-x',
      model: 'gpt-6-sol',
      baseline: '   ',
    })
    expect(input).not.toHaveProperty('baseline')
    expect(input.model).toBe('gpt-6-sol')
  })
})
