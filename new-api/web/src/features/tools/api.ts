import { api } from '@/lib/api'
export type AuditRun = {
  public_id: string
  relay_share_id: number
  model: string
  status: string
  risk_level: string
  sample_count: number
  finished_at: number
  evidence_summary: string
}
export type RelayOption = {
  public_id: string
  title: string
  kind: string
  check_status: string
}
export type CheckinSite = {
  id: number
  name: string
  base_url: string
  enabled: boolean
  timezone: string
  schedule_minute: number
  last_run_at: number
  last_status: string
  last_message: string
  failure_count: number
}
type Envelope<T> = { success: boolean; data: T }
export async function createAuditRun(input: {
  relay_share_public_id: string
  model: string
}) {
  const res = await api.post('/api/apigx/audit-runs', input)
  return res.data
}
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
export async function createCheckinSite(input: {
  name: string
  base_url: string
  key: string
  credential_kind: string
  timezone: string
  schedule_minute: number
}) {
  const res = await api.post('/api/apigx/checkin-sites', input)
  return res.data
}
export async function listCheckinSites() {
  const res = await api.get<Envelope<{ items: CheckinSite[] }>>(
    '/api/apigx/checkin-sites'
  )
  return res.data
}
export async function runCheckinSite(id: number) {
  const res = await api.post(`/api/apigx/checkin-sites/${id}/run`)
  return res.data
}
