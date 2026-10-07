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
  tier?: string
  concurrency?: number
}

export type ModelRegistryEntry = {
  id: string
  display_name: string
  vendor: string
  request_names: string[]
  baseline_state: 'ready' | 'pending'
  baseline_meta?: {
    measured_at: string
    source: string
    samples_per_probe: number
  }
  ready_tiers?: string[]
  threshold?: number
}

type Envelope<T> = { success: boolean; data: T; message?: string }

export const toolsQueryKeys = {
  audits: ['apigx-audit-runs'] as const,
  relayOptions: ['relay-shares-mine'] as const,
  checkinSites: ['apigx-checkin-sites'] as const,
  myCrowdtests: ['apigx-crowdtests-mine'] as const,
  publicCrowdtests: ['apigx-crowdtests-public'] as const,
  modelRegistry: ['apigx-model-registry'] as const,
}

export async function listModelRegistry(): Promise<ModelRegistryEntry[]> {
  const res = await api.get<Envelope<{ models: ModelRegistryEntry[] }>>(
    '/api/tools/dilution-models'
  )
  const models = res.data?.data?.models
  return Array.isArray(models) ? models : []
}

// listTargetModels 问目标接口「你提供哪些型号名」。掺水检测能测的型号要同时满足两个条件：
// 我们测过它的基准，并且这台中转真的提供这个名字。只看前者就会摆出一堆点了必然全部失败的
// 卡片——2026-10-07 实测某国产中转，界面上的 gpt-6-sol／claude-sonnet-5 一律回「模型不可用」。
export async function listTargetModels(
  input: Omit<ProbeTargetInput, 'model'>
): Promise<string[]> {
  const res = await api.post<Envelope<{ models: string[] }>>(
    '/api/tools/target-models',
    input,
    { timeout: 20000 }
  )
  const models = res.data?.data?.models
  return Array.isArray(models) ? models : []
}

export async function runSpeedTest(input: ProbeTargetInput) {
  const res = await api.post<Envelope<SpeedReport>>(
    '/api/tools/speed-test',
    input,
    {
      timeout: 150000, // 150s：后端 runSpeedTest 的预算是 speedSamples/2 × 3 × 45s = 135s，
      // 比它短的客户端超时会把还在正常跑的检测先掐掉，报成网络错误。
    }
  )
  return res.data
}

export async function runDilutionTest(input: ProbeTargetInput) {
  const res = await api.post<Envelope<DilutionReport>>(
    '/api/tools/dilution-test',
    input,
    {
      timeout: 1200000, // 20min：后端 Meow 采样 540s + 其余维度重新起算 600s（见 relayprobe/dilution.go）
    }
  )
  return res.data
}

export async function createCrowdtest(
  input: ProbeTargetInput & { benchmark: 'candy' | 'pelican'; publish: boolean }
) {
  const res = await api.post<Envelope<Crowdtest>>('/api/crowdtests', input, {
    timeout: 600000, // 10min, same as dilution test
  })
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
  const res = await api.post<Envelope<AuditRun>>('/api/apigx/audit-runs', input, {
    timeout: 180000, // 180s, backend audit timeout is 150s
  })
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
