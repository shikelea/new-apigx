import { api } from '@/lib/api'

export type AuditCheckStatus =
  | 'passed'
  | 'warning'
  | 'failed'
  | 'skipped'
  | 'inconclusive'

export type AuditCheck = {
  id: string
  name: string
  category: string
  status: AuditCheckStatus
  evidence: string
  confidence: string
  sample_count: number
  latency_ms: number
}

export type AuditRun = {
  public_id: string
  relay_share_id: number
  relay_title: string
  model: string
  status: string
  risk_level: string
  sample_count: number
  started_at: number
  finished_at: number
  evidence_summary: string
  error_message?: string
  checks: AuditCheck[]
}

export type RelayOption = {
  public_id: string
  title: string
  kind: string
  check_status: string
  credential_configured?: boolean
}

export type CheckinSite = {
  id: number
  name: string
  base_url: string
  credential_kind: string
  enabled: boolean
  timezone: string
  schedule_minute: number
  last_run_at: number
  next_run_at: number
  last_status: string
  last_message: string
  failure_count: number
}

export type CheckinSiteInput = {
  name: string
  base_url: string
  key: string
  credential_kind: 'bearer' | 'session_cookie'
  timezone: string
  schedule_minute: number
}

type Envelope<T> = { success: boolean; data: T; message?: string }

export const toolsQueryKeys = {
  audits: ['apigx-audit-runs'] as const,
  relayOptions: ['relay-shares-mine'] as const,
  checkinSites: ['apigx-checkin-sites'] as const,
  monitors: ['manxue-monitors'] as const,
  community: ['manxue-community'] as const,
}

export type IntelligenceInput = {
  benchmark: 'candy' | 'pelican'
  base_url?: string
  api_key?: string
  model?: string
  protocol?: string
  reasoning_effort?: string
  html?: string
}

export type IntelligenceTest = {
  id: string
  benchmark: 'candy' | 'pelican'
  status: string
  phase?: string
  expires_at?: string
  candy?: {
    status?: string
    answer?: string
    duration_ms?: number
    error?: string
  }
  result?: { html?: string; has_html?: boolean }
  assessment?: {
    quality?: string
    label?: string
    reason?: string
    source?: string
  }
  error?: string
}

export async function createIntelligenceTest(input: IntelligenceInput) {
  const res = await api.post<IntelligenceTest>(
    '/api/apigx/intelligence/tests',
    input
  )
  return res.data
}

export async function getIntelligenceTest(id: string) {
  const res = await api.get<IntelligenceTest>(
    `/api/apigx/intelligence/tests/${encodeURIComponent(id)}`
  )
  return res.data
}

export async function cancelIntelligenceTest(id: string) {
  const res = await api.delete<IntelligenceTest>(
    `/api/apigx/intelligence/tests/${encodeURIComponent(id)}`
  )
  return res.data
}

// 终态集合：用于停止轮询与切换按钮状态。
export const intelligenceTerminalStatuses = [
  'succeeded',
  'failed',
  'cancelled',
  'canceled',
  'expired',
] as const

export function isIntelligenceTerminal(status: string) {
  return (intelligenceTerminalStatuses as readonly string[]).includes(status)
}

export async function getManxueMonitors() {
  const res = await api.get<Record<string, unknown>>(
    '/api/manxue/public/api/public/monitors'
  )
  return res.data
}

export async function getManxueCommunity(benchmark: 'candy' | 'pelican') {
  const res = await api.get<Record<string, unknown>>(
    '/api/manxue/public/api/public/community',
    { params: { benchmark, limit: 20 } }
  )
  return res.data
}

export async function createAuditRun(input: {
  relay_share_public_id: string
  model: string
}) {
  const res = await api.post<Envelope<AuditRun>>('/api/apigx/audit-runs', input)
  return res.data
}

// Shares the relay-share "mine" endpoint and cache entry so both pages stay in sync.
export async function listRelayOptions() {
  const res = await api.get<Envelope<{ items: RelayOption[] }>>(
    '/api/relay-shares/mine'
  )
  return res.data
}

export async function listAuditRuns() {
  const res = await api.get<Envelope<{ items: AuditRun[] }>>(
    '/api/apigx/audit-runs'
  )
  return res.data
}

export async function createCheckinSite(input: CheckinSiteInput) {
  const res = await api.post<Envelope<CheckinSite>>(
    '/api/apigx/checkin-sites',
    input
  )
  return res.data
}

export async function listCheckinSites() {
  const res = await api.get<Envelope<{ items: CheckinSite[] }>>(
    '/api/apigx/checkin-sites'
  )
  return res.data
}

export async function runCheckinSite(id: number) {
  const res = await api.post<Envelope<{ status: string; message: string }>>(
    `/api/apigx/checkin-sites/${id}/run`
  )
  return res.data
}
