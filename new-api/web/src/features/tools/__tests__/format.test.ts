/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import {
  auditStatus,
  formatEpoch,
  formatMinuteOfDay,
  parseTimeOfDay,
  riskLevelKey,
  riskLevelVariant,
} from '../lib/format'

describe('tool center formatting', () => {
  it('parses valid local check-in times and rejects invalid values', () => {
    expect(parseTimeOfDay(' 9:07 ')).toBe(547)
    expect(parseTimeOfDay('23:59')).toBe(1439)
    expect(parseTimeOfDay('24:00')).toBeNull()
    expect(parseTimeOfDay('12:60')).toBeNull()
    expect(parseTimeOfDay('noon')).toBeNull()
  })

  it('formats minute values within the supported daily range', () => {
    expect(formatMinuteOfDay(547)).toBe('09:07')
    expect(formatMinuteOfDay(-1)).toBe('00:00')
    expect(formatMinuteOfDay(1440)).toBe('23:59')
    expect(formatMinuteOfDay(Number.NaN)).toBe('00:00')
  })

  it('formats only present epoch values', () => {
    expect(formatEpoch(0)).toBe('—')
    expect(formatEpoch(undefined)).toBe('—')
    expect(formatEpoch(1)).not.toBe('—')
  })

  it('maps known audit outcomes and falls back for unknown values', () => {
    expect(auditStatus('passed').label).toBe('Passed')
    expect(auditStatus('future-status')).toEqual(auditStatus('inconclusive'))
    expect(riskLevelKey('需复核')).toBe('Needs review')
    expect(riskLevelKey('unknown')).toBe('unknown')
    expect(riskLevelVariant('高风险信号')).toBe('destructive')
    expect(riskLevelVariant('未发现明显异常')).toBe('secondary')
  })
})
