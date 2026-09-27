import type { AuditCheckStatus } from '../api'

export const DEFAULT_TIME_ZONE = 'Asia/Shanghai'

/** Parses an `HH:MM` value from `<input type="time">`; returns null when invalid or empty. */
export function parseTimeOfDay(value: string): number | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec(value.trim())
  if (!match) return null
  const hours = Number(match[1])
  const minutes = Number(match[2])
  if (hours > 23 || minutes > 59) return null
  return hours * 60 + minutes
}

export function formatMinuteOfDay(minute: number): string {
  const safe = Number.isFinite(minute)
    ? Math.min(Math.max(Math.trunc(minute), 0), 1439)
    : 0
  const hours = String(Math.floor(safe / 60)).padStart(2, '0')
  const minutes = String(safe % 60).padStart(2, '0')
  return `${hours}:${minutes}`
}

/** The browser's IANA time zone, falling back to the platform default. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || DEFAULT_TIME_ZONE
  } catch {
    return DEFAULT_TIME_ZONE
  }
}

export function formatEpoch(epoch: number | undefined): string {
  return epoch ? new Date(epoch * 1000).toLocaleString() : '—'
}

export type BadgeTone = 'default' | 'secondary' | 'destructive' | 'outline'

// Keys are English source strings passed through t() by the caller.
export const auditStatusMeta: Record<
  AuditCheckStatus,
  { label: string; variant: BadgeTone }
> = {
  passed: { label: 'Passed', variant: 'secondary' },
  warning: { label: 'Needs review', variant: 'outline' },
  failed: { label: 'High-risk signal', variant: 'destructive' },
  skipped: { label: 'Skipped', variant: 'outline' },
  inconclusive: { label: 'Inconclusive', variant: 'outline' },
}

export function auditStatus(status: string) {
  return (
    auditStatusMeta[status as AuditCheckStatus] ?? auditStatusMeta.inconclusive
  )
}

// The backend emits Chinese risk levels; map them to i18n source keys.
const riskLevelKeys: Record<string, string> = {
  高风险信号: 'High-risk signal',
  需复核: 'Needs review',
  证据不足: 'Insufficient evidence',
  未发现明显异常: 'No obvious anomaly',
}

export function riskLevelKey(level: string): string {
  return riskLevelKeys[level] ?? level
}

export function riskLevelVariant(level: string): BadgeTone {
  if (level === '高风险信号') return 'destructive'
  if (level === '未发现明显异常') return 'secondary'
  return 'outline'
}
