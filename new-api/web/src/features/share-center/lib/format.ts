/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { SharePost } from '../types'

export type ShareFilter = 'all' | 'usable' | 'observing' | 'invalid'

export function voteSummary(post: SharePost): {
  key: string
  values?: { count: string | number }
} {
  const total = post.usable_votes + post.invalid_votes
  if (total === 0) return { key: 'No validity feedback yet' }
  if (post.usable_votes > post.invalid_votes) {
    return {
      key: 'Still usable · {{count}}',
      values: { count: `${post.usable_votes}/${total}` },
    }
  }
  if (post.invalid_votes > post.usable_votes) {
    return {
      key: 'May be unavailable · {{count}}',
      values: { count: `${post.invalid_votes}/${total}` },
    }
  }
  return {
    key: 'Needs observation · {{count}} votes',
    values: { count: total },
  }
}

export function postState(post: SharePost): ShareFilter {
  if (post.usable_votes === 0 && post.invalid_votes === 0) return 'observing'
  if (post.usable_votes > post.invalid_votes) return 'usable'
  if (post.invalid_votes > post.usable_votes) return 'invalid'
  return 'observing'
}

export function formatPublishedAt(
  timestamp: number,
  locale: string,
  unpublishedLabel: string
) {
  if (!timestamp) return unpublishedLabel
  const value = timestamp > 1_000_000_000_000 ? timestamp : timestamp * 1000
  return new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
  }).format(new Date(value))
}
