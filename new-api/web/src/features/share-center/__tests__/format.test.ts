/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import { formatPublishedAt, postState, voteSummary } from '../lib/format'
import type { SharePost } from '../types'

function post(overrides: Partial<SharePost> = {}): SharePost {
  return {
    id: 1,
    public_id: 'share_1',
    author_id: 2,
    title: '示例分享',
    summary: '',
    content: '内容',
    visibility: 'public',
    status: 'published',
    usable_votes: 0,
    invalid_votes: 0,
    published_at: 0,
    created_at: 0,
    updated_at: 0,
    ...overrides,
  }
}

describe('分享中心状态摘要', () => {
  it('没有投票时归为待观察并明确提示样本不足', () => {
    expect(postState(post())).toBe('observing')
    expect(voteSummary(post())).toEqual({ key: 'No validity feedback yet' })
  })

  it('可用投票超过失效投票时归为仍可用', () => {
    expect(postState(post({ usable_votes: 3, invalid_votes: 1 }))).toBe(
      'usable'
    )
    expect(voteSummary(post({ usable_votes: 3, invalid_votes: 1 }))).toEqual({
      key: 'Still usable · {{count}}',
      values: { count: '3/4' },
    })
  })

  it('相同票数时保持待观察而不是误标为可用', () => {
    expect(postState(post({ usable_votes: 2, invalid_votes: 2 }))).toBe(
      'observing'
    )
    expect(voteSummary(post({ usable_votes: 2, invalid_votes: 2 }))).toEqual({
      key: 'Needs observation · {{count}} votes',
      values: { count: 4 },
    })
  })

  it('同时支持秒级和毫秒级时间戳', () => {
    expect(formatPublishedAt(0, 'en', 'Not published yet')).toBe(
      'Not published yet'
    )
    expect(formatPublishedAt(1_758_787_200, 'zh-CN', '尚未发布')).toBe(
      formatPublishedAt(1_758_787_200_000, 'zh-CN', '尚未发布')
    )
  })
})
