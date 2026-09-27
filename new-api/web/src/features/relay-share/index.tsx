/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  CheckCircle2,
  Clock3,
  KeyRound,
  Loader2,
  RadioTower,
  ShieldCheck,
  XCircle,
} from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout/components/public-layout'
import { PublicPageBackdrop } from '@/components/layout/components/public-page-backdrop'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  checkRelayShare,
  createRelayShare,
  listMyRelayShares,
  listRelayShares,
  relayQueryKeys,
  type RelayCreateInput,
  type RelayShare,
  type RelayStatus,
  voteRelayShare,
} from './api'

const statusLabels: Record<RelayStatus, string> = {
  healthy: 'Check passed',
  degraded: 'Partially available',
  failed: 'Check failed',
  unknown: 'Not checked yet',
}
const kindLabels = {
  all: 'All',
  charity: 'Charity relay',
  commercial: 'Commercial relay',
} as const

function statusIcon(status: RelayStatus) {
  if (status === 'healthy') return <CheckCircle2 className='size-4' />
  if (status === 'failed') return <XCircle className='size-4' />
  return <Clock3 className='size-4' />
}
function formatTime(epoch: number) {
  return epoch ? new Date(epoch * 1000).toLocaleString() : '—'
}

function RelayCard({
  share,
  mine = false,
  checking = false,
  voting = false,
  canVote = false,
  onCheck,
  onVote,
}: {
  share: RelayShare
  mine?: boolean
  checking?: boolean
  voting?: boolean
  canVote?: boolean
  onCheck?: () => void
  onVote?: (usable: boolean) => void
}) {
  const { t } = useTranslation()
  return (
    <Card className='bg-card/90 h-full'>
      <CardHeader className='gap-3'>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <CardTitle className='truncate text-base'>{share.title}</CardTitle>
            <p className='text-muted-foreground mt-1 truncate text-xs'>
              {share.owner_name} · {share.base_url}
            </p>
          </div>
          <Badge variant={share.kind === 'charity' ? 'secondary' : 'outline'}>
            {t(kindLabels[share.kind] ?? kindLabels.commercial)}
          </Badge>
        </div>
        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
          <span>{share.protocol === 'sub2api' ? 'Sub2API' : 'New API'}</span>
          <span className='inline-flex items-center gap-1'>
            {statusIcon(share.check_status)}
            {t(statusLabels[share.check_status] ?? statusLabels.unknown)}
          </span>
        </div>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground line-clamp-3 text-sm leading-6 whitespace-pre-wrap'>
          {share.content || t('The sharer did not add a description.')}
        </p>
        <div className='grid grid-cols-3 gap-2 text-xs'>
          <Metric
            label={t('Models')}
            value={share.model_count ? String(share.model_count) : '—'}
          />
          <Metric
            label={t('Latency')}
            value={share.latency_ms ? `${share.latency_ms} ms` : '—'}
          />
          <Metric
            label={t('Last check')}
            value={formatTime(share.last_checked_at)}
          />
        </div>
        <p className='text-muted-foreground text-xs'>
          {share.check_reason || t('The platform has not finished checking.')}
          {share.updated_at
            ? ` · ${t('Updated {{time}}', { time: formatTime(share.updated_at) })}`
            : ''}
        </p>
        {mine ? (
          <div className='bg-muted/30 flex items-center justify-between gap-2 rounded-lg border p-3 text-xs'>
            <span className='inline-flex items-center gap-1.5'>
              <KeyRound className='size-3.5' />
              {share.credential_configured
                ? t('Detection key configured · ends with {{suffix}}', {
                    suffix: share.key_suffix ?? '****',
                  })
                : t('No detection key configured')}
            </span>
            {share.credential_configured ? (
              <Button
                size='sm'
                variant='outline'
                disabled={checking}
                onClick={onCheck}
              >
                {checking ? <Loader2 className='animate-spin' /> : null}
                {t('Check now')}
              </Button>
            ) : null}
          </div>
        ) : null}
        {!mine ? (
          <VoteBar
            share={share}
            canVote={canVote}
            voting={voting}
            onVote={onVote}
          />
        ) : null}
      </CardContent>
    </Card>
  )
}
function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className='bg-muted/30 rounded-lg p-2'>
      <p className='text-muted-foreground'>{label}</p>
      <p className='mt-1 truncate font-medium'>{value}</p>
    </div>
  )
}

function VoteBar({
  share,
  canVote,
  voting,
  onVote,
}: {
  share: RelayShare
  canVote: boolean
  voting: boolean
  onVote?: (usable: boolean) => void
}) {
  const { t } = useTranslation()
  const usableText = t('Still works · {{count}}', { count: share.vote_usable })
  const brokenText = t('Stopped working · {{count}}', {
    count: share.vote_unavailable,
  })
  if (!canVote || !onVote) {
    // Anonymous visitors see the tallies but cannot vote (the API requires login).
    return (
      <div className='text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-t pt-3 text-xs'>
        <span>{usableText}</span>
        <span>{brokenText}</span>
        <Link to='/sign-in' className='text-primary hover:underline'>
          {t('Sign in to vote')}
        </Link>
      </div>
    )
  }
  return (
    <div
      className='flex flex-wrap items-center gap-2 border-t pt-3'
      role='group'
      aria-label={t('Is this relay still usable?')}
    >
      <Button
        size='sm'
        variant={share.my_vote === true ? 'default' : 'outline'}
        aria-pressed={share.my_vote === true}
        disabled={voting}
        onClick={() => onVote(true)}
      >
        {usableText}
      </Button>
      <Button
        size='sm'
        variant={share.my_vote === false ? 'default' : 'ghost'}
        aria-pressed={share.my_vote === false}
        disabled={voting}
        onClick={() => onVote(false)}
      >
        {brokenText}
      </Button>
      {share.my_vote !== undefined ? (
        <span className='text-muted-foreground text-xs'>
          {t('You can change your vote')}
        </span>
      ) : null}
    </div>
  )
}

export function RelayShare() {
  const { t } = useTranslation()
  const auth = useAuthStore((state) => state.auth)
  const signedIn = !!auth?.user
  const client = useQueryClient()
  const [kind, setKind] = useState<'all' | 'charity' | 'commercial'>('all')
  const [showForm, setShowForm] = useState(false)
  const publicQuery = useQuery({
    // Votes are per viewer, so the cache entry must not leak across sessions.
    queryKey: [...relayQueryKeys.public, auth?.user?.id ?? 0],
    queryFn: listRelayShares,
  })
  const mineQuery = useQuery({
    queryKey: relayQueryKeys.mine,
    queryFn: listMyRelayShares,
    enabled: signedIn,
  })
  const refreshAll = () => {
    void client.invalidateQueries({ queryKey: relayQueryKeys.public })
    void client.invalidateQueries({ queryKey: relayQueryKeys.mine })
  }
  const check = useMutation({
    mutationFn: checkRelayShare,
    onSuccess: () => toast.success(t('Check finished')),
    onError: (error) => handleServerError(error),
    onSettled: refreshAll,
  })
  const vote = useMutation({
    mutationFn: ({ id, usable }: { id: string; usable: boolean }) =>
      voteRelayShare(id, usable),
    onSuccess: () => toast.success(t('Thanks for the feedback')),
    onError: (error) => handleServerError(error),
    onSettled: () =>
      void client.invalidateQueries({ queryKey: relayQueryKeys.public }),
  })
  const create = useMutation({
    mutationFn: createRelayShare,
    onSuccess: () => {
      toast.success(t('Relay submitted, checking now'))
      setShowForm(false)
      refreshAll()
    },
    onError: (error) => handleServerError(error),
  })
  const items = (publicQuery.data?.data?.items ?? []).filter(
    (item) => kind === 'all' || item.kind === kind
  )
  const mine = mineQuery.data?.data?.items ?? []
  const checkingId = check.isPending ? check.variables : undefined
  const votingId = vote.isPending ? vote.variables?.id : undefined
  return (
    <PublicLayout showMainContainer={false}>
      <PublicPageBackdrop>
        <div className='mx-auto w-full max-w-[1280px] space-y-8 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div className='flex flex-wrap items-end justify-between gap-4'>
            <div>
              <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
                {t('Community resources')}
              </p>
              <h1 className='mt-1 text-3xl font-semibold tracking-tight'>
                {t('Relay sharing')}
              </h1>
              <p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-6'>
                {t(
                  'Only charity and commercial relays. Once a sharer submits a detection key, the platform runs low-cost protocol checks on a schedule; public pages show masked status and votes, never the key.'
                )}
              </p>
            </div>
            <Button
              aria-expanded={showForm}
              onClick={() => setShowForm((value) => !value)}
            >
              <RadioTower />
              {t('Share a relay')}
            </Button>
          </div>
          {showForm && signedIn ? (
            <RelayEditor
              onSubmit={(input) => create.mutate(input)}
              pending={create.isPending}
              onCancel={() => setShowForm(false)}
            />
          ) : null}
          {showForm && !signedIn ? (
            <Card>
              <CardContent className='flex items-center justify-between gap-4 p-5 text-sm'>
                <span>{t('Sign in to submit a relay and detection key.')}</span>
                <Button render={<Link to='/sign-in' />}>{t('Sign in')}</Button>
              </CardContent>
            </Card>
          ) : null}
          {signedIn && mine.length > 0 ? (
            <section className='space-y-4'>
              <div className='flex items-center gap-2'>
                <ShieldCheck className='text-primary size-5' />
                <h2 className='text-lg font-semibold'>{t('My relays')}</h2>
              </div>
              <div className='grid gap-4 md:grid-cols-2'>
                {mine.map((share) => (
                  <RelayCard
                    key={share.public_id}
                    share={share}
                    mine
                    checking={checkingId === share.public_id}
                    onCheck={() => check.mutate(share.public_id)}
                  />
                ))}
              </div>
            </section>
          ) : null}
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div>
              <h2 className='text-lg font-semibold'>{t('Public relays')}</h2>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t('Sorted by last update, without an overall ranking.')}
              </p>
            </div>
            <div
              className='flex gap-2'
              role='group'
              aria-label={t('Filter by relay type')}
            >
              {(['all', 'charity', 'commercial'] as const).map((value) => (
                <button
                  key={value}
                  type='button'
                  aria-pressed={kind === value}
                  onClick={() => setKind(value)}
                  className={`focus-visible:ring-ring/50 rounded-full border px-3 py-1.5 text-xs outline-none focus-visible:ring-3 ${kind === value ? 'border-primary bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}
                >
                  {t(kindLabels[value])}
                </button>
              ))}
            </div>
          </div>
          <PublicRelayList
            query={publicQuery}
            items={items}
            canVote={signedIn}
            votingId={votingId}
            onVote={(id, usable) => vote.mutate({ id, usable })}
          />{' '}
        </div>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}

function PublicRelayList({
  query,
  items,
  canVote,
  votingId,
  onVote,
}: {
  query: { isPending: boolean; isError: boolean; refetch: () => unknown }
  items: RelayShare[]
  canVote: boolean
  votingId?: string
  onVote: (id: string, usable: boolean) => void
}) {
  const { t } = useTranslation()
  if (query.isPending) {
    return <LoadingState />
  }
  if (query.isError) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  if (items.length === 0) {
    return (
      <EmptyState
        title={t('No public relays yet')}
        description={t('You can be the first to share one.')}
      />
    )
  }
  return (
    <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
      {items.map((share) => (
        <RelayCard
          key={share.public_id}
          share={share}
          canVote={canVote}
          voting={votingId === share.public_id}
          onVote={(usable) => onVote(share.public_id, usable)}
        />
      ))}
    </div>
  )
}

const RELAY_TITLE_MAX = 120
const RELAY_CONTENT_MAX = 20000

function RelayEditor({
  onSubmit,
  pending,
  onCancel,
}: {
  onSubmit: (input: RelayCreateInput) => void
  pending: boolean
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const fieldId = useId()
  const [title, setTitle] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [key, setKey] = useState('')
  const [content, setContent] = useState('')
  const [kind, setKind] = useState<'charity' | 'commercial'>('charity')
  const [protocol, setProtocol] = useState<'new-api' | 'sub2api'>('new-api')
  const ids = {
    title: `${fieldId}-title`,
    base: `${fieldId}-base`,
    kind: `${fieldId}-kind`,
    protocol: `${fieldId}-protocol`,
    key: `${fieldId}-key`,
    content: `${fieldId}-content`,
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>{t('Share a relay')}</CardTitle>
        <p className='text-muted-foreground text-sm'>
          {t(
            'The key is only used for server-side checks and is never shown again. Make sure you are allowed to share it; checks may consume a little upstream quota.'
          )}
        </p>
      </CardHeader>
      <CardContent className='grid gap-4 md:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor={ids.title}>{t('Name')}</Label>
          <Input
            id={ids.title}
            value={title}
            maxLength={RELAY_TITLE_MAX}
            onChange={(event) => setTitle(event.target.value)}
            placeholder={t('e.g. My charity New API')}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={ids.base}>{t('Site URL')}</Label>
          <Input
            id={ids.base}
            type='url'
            value={baseURL}
            maxLength={512}
            onChange={(event) => setBaseURL(event.target.value)}
            placeholder='https://example.com'
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor={ids.kind}>{t('Relay type')}</Label>
          <NativeSelect
            id={ids.kind}
            className='w-full'
            value={kind}
            onChange={(event) => setKind(event.target.value as typeof kind)}
          >
            <NativeSelectOption value='charity'>
              {t('Charity relay')}
            </NativeSelectOption>
            <NativeSelectOption value='commercial'>
              {t('Commercial relay')}
            </NativeSelectOption>
          </NativeSelect>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={ids.protocol}>{t('Protocol')}</Label>
          <NativeSelect
            id={ids.protocol}
            className='w-full'
            value={protocol}
            onChange={(event) =>
              setProtocol(event.target.value as typeof protocol)
            }
          >
            <NativeSelectOption value='new-api'>New API</NativeSelectOption>
            <NativeSelectOption value='sub2api'>Sub2API</NativeSelectOption>
          </NativeSelect>
        </div>
        <div className='space-y-2 md:col-span-2'>
          <Label htmlFor={ids.key}>{t('Detection key')}</Label>
          <Input
            id={ids.key}
            type='password'
            autoComplete='off'
            value={key}
            onChange={(event) => setKey(event.target.value)}
            placeholder={t('Only submit a key meant for checks')}
          />
        </div>
        <div className='space-y-2 md:col-span-2'>
          <Label htmlFor={ids.content}>{t('Description (plain text)')}</Label>
          <Textarea
            id={ids.content}
            value={content}
            maxLength={RELAY_CONTENT_MAX}
            onChange={(event) => setContent(event.target.value)}
            rows={6}
            placeholder={t(
              'Describe features, limits, suitable models and when you last verified it.'
            )}
          />
        </div>
        <div className='flex justify-end gap-2 md:col-span-2'>
          <Button variant='outline' onClick={onCancel}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={
              pending || !title.trim() || !baseURL.trim() || !key.trim()
            }
            onClick={() =>
              onSubmit({
                title: title.trim(),
                base_url: baseURL.trim(),
                kind,
                protocol,
                content: content.trim(),
                key: key.trim(),
              })
            }
          >
            {pending ? <Loader2 className='animate-spin' /> : null}
            {t('Submit and check')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
