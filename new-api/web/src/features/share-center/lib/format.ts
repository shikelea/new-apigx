/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { SharePost } from '../types'

export type ShareFilter = 'all' | 'usable' | 'observing' | 'invalid'

export function voteSummary(post: SharePost) {
  const total = post.usable_votes + post.invalid_votes
  if (total === 0) return '暂无有效性样本'
  if (post.usable_votes > post.invalid_votes) {
    return `仍可用 · ${post.usable_votes}/${total}`
  }
  if (post.invalid_votes > post.usable_votes) {
    return `可能失效 · ${post.invalid_votes}/${total}`
  }
  return `状态待观察 · ${total} 票`
}

export function postState(post: SharePost): ShareFilter {
  if (post.usable_votes === 0 && post.invalid_votes === 0) return 'observing'
  if (post.usable_votes > post.invalid_votes) return 'usable'
  if (post.invalid_votes > post.usable_votes) return 'invalid'
  return 'observing'
}

export function formatPublishedAt(timestamp: number) {
  if (!timestamp) return '尚未发布'
  const value = timestamp > 1_000_000_000_000 ? timestamp : timestamp * 1000
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
  }).format(new Date(value))
}
