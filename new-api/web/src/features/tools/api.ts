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

export type SpeedSample = {
  ttft_ms: number
  total_ms: number
  output_tokens: number
  output_tps: number
  content_chunks: number
  tokens_estimated: boolean
}

export type SpeedReport = {
  model: string
  samples: SpeedSample[]
  ttft_ms: number
  ttft_spread_ms: number
  output_tps: number
  tps_spread: number
  pseudostream: boolean
  tokens_exact: boolean
  note: string
  failed_samples: number
  disclaimer?: string
}

export type DilutionVerdict = 'match' | 'mismatch' | 'suspect' | 'inconclusive'

export type DilutionFinding = {
  id: string
  name: string
  verdict: DilutionVerdict
  evidence: string
  weight: number
}

export type DilutionReport = {
  model: string
  family: string
  findings: DilutionFinding[]
  verdict: DilutionVerdict
  score: number
  disclaimer: string
}

export type Crowdtest = {
  public_id: string
  benchmark: 'candy' | 'pelican'
  model: string
  status: string
  score: number
  evidence: string
  artifact?: string
  published: boolean
  created_at: number
  visibility?: number
  submitter?: string
}

// A probe target is either one of your own relays (the stored encrypted key is
// used server-side) or a one-off base URL plus key that is never persisted.
export type ProbeTargetInput = {
  relay_share_public_id?: string
  base_url?: string
  protocol?: string
  key?: string
  model: string
}

type Envelope<T> = { success: boolean; data: T; message?: string }

export const toolsQueryKeys = {
  audits: ['apigx-audit-runs'] as const,
  relayOptions: ['relay-shares-mine'] as const,
  checkinSites: ['apigx-checkin-sites'] as const,
  myCrowdtests: ['apigx-crowdtests-mine'] as const,
  publicCrowdtests: ['apigx-crowdtests-public'] as const,
}

export async function runSpeedTest(input: ProbeTargetInput) {
  const res = await api.post<Envelope<SpeedReport>>(
    '/api/tools/speed-test',
    input
  )
  return res.data
}

export async function runDilutionTest(input: ProbeTargetInput) {
  const res = await api.post<Envelope<DilutionReport>>(
    '/api/tools/dilution-test',
    input
  )
  return res.data
}

export async function createCrowdtest(
  input: ProbeTargetInput & { benchmark: 'candy' | 'pelican'; publish: boolean }
) {
  const res = await api.post<Envelope<Crowdtest>>('/api/crowdtests', input)
  return res.data
}

export async function publishCrowdtest(publicID: string) {
  const res = await api.post<Envelope<{ public_id: string }>>(
    `/api/crowdtests/${publicID}/publish`
  )
  return res.data
}

export async function listMyCrowdtests() {
  const res = await api.get<
    Envelope<{ items: Crowdtest[]; daily_limit: number }>
  >('/api/crowdtests/mine')
  return res.data
}

export async function listPublicCrowdtests(benchmark?: string) {
  const query = benchmark ? `?benchmark=${encodeURIComponent(benchmark)}` : ''
  const res = await api.get<
    Envelope<{ items: Crowdtest[]; disclaimer: string }>
  >(`/api/crowdtests${query}`)
  return res.data
}

export async function createAuditRun(input: ProbeTargetInput) {
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
