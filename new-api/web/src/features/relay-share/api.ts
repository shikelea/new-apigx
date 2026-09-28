import { api } from '@/lib/api'

export type RelayKind = 'charity' | 'commercial'
export type RelayStatus = 'unknown' | 'healthy' | 'degraded' | 'failed'

export type RelayShare = {
  public_id: string
  title: string
  base_url: string
  kind: RelayKind
  protocol: string
  content: string
  status: number
  check_status: RelayStatus
  check_reason: string
  last_checked_at: number
  latency_ms: number
  model_count: number
  vote_usable: number
  vote_unavailable: number
  owner_name: string
  created_at: number
  updated_at: number
  credential_configured?: boolean
  key_suffix?: string
  /** Present only for signed-in viewers who already voted. */
  my_vote?: boolean
}

export const relayQueryKeys = {
  public: ['relay-shares'] as const,
  // Shared with the tool center relay picker.
  mine: ['relay-shares-mine'] as const,
  samples: (id: string) => ['relay-share-samples', id] as const,
  checks: (id: string) => ['relay-share-checks', id] as const,
}

/** Continuous-check settings. Visibility: 0 off, 1 owner only, 2 public. */
export type RelayCheckSettings = {
  availability_mode: number
  availability_interval: number
  degrade_mode: number
  degrade_interval: number
  degrade_models: string[]
  dilution_mode: number
  dilution_interval: number
  dilution_models: string[]
  paused: boolean
  intervals: number[]
}

export type RelayProbeSample = {
  kind: 'availability' | 'degrade' | 'dilution' | 'speed'
  model: string
  status: RelayStatus
  score: number
  latency_ms: number
  first_token_ms: number
  output_tps: number
  evidence: string
  probed_at: number
}

type Envelope<T> = { success: boolean; data: T; message?: string }

export async function listRelayShares() {
  const response =
    await api.get<Envelope<{ items: RelayShare[] }>>('/api/relay-shares')
  return response.data
}

export async function listMyRelayShares() {
  const response = await api.get<Envelope<{ items: RelayShare[] }>>(
    '/api/relay-shares/mine'
  )
  return response.data
}

export type RelayCheckInput = Partial<
  Omit<RelayCheckSettings, 'intervals' | 'paused'>
> & { paused?: boolean }

export type RelayCreateInput = {
  title: string
  base_url: string
  kind: RelayKind
  protocol: 'new-api' | 'sub2api'
  content: string
  key: string
  checks?: RelayCheckInput
}

export async function getRelayChecks(id: string) {
  const response = await api.get<Envelope<RelayCheckSettings>>(
    `/api/relay-shares/${encodeURIComponent(id)}/checks`
  )
  return response.data
}

export async function setRelayChecks(id: string, input: RelayCheckInput) {
  const response = await api.put<Envelope<RelayCheckSettings>>(
    `/api/relay-shares/${encodeURIComponent(id)}/checks`,
    input
  )
  return response.data
}

export async function listRelaySamples(id: string, kind?: string) {
  const query = kind ? `?kind=${encodeURIComponent(kind)}` : ''
  const response = await api.get<
    Envelope<{ items: RelayProbeSample[]; viewer_is_owner: boolean }>
  >(`/api/relay-shares/${encodeURIComponent(id)}/samples${query}`)
  return response.data
}

export async function createRelayShare(input: RelayCreateInput) {
  const response = await api.post<
    Envelope<{
      relay: RelayShare
      credential_configured: boolean
      key_suffix: string
    }>
  >('/api/relay-shares', input)
  return response.data
}

export async function checkRelayShare(id: string) {
  const response = await api.post<
    Envelope<{
      status: RelayStatus
      reason: string
      latency_ms: number
      model_count: number
      models: string[]
    }>
  >(`/api/relay-shares/${encodeURIComponent(id)}/check`)
  return response.data
}

export async function voteRelayShare(id: string, usable: boolean) {
  const response = await api.post<
    Envelope<{ voted: boolean; usable: boolean }>
  >(`/api/relay-shares/${encodeURIComponent(id)}/vote`, { usable })
  return response.data
}
