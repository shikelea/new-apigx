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
  RadioTower,
  ShieldCheck,
  XCircle,
} from 'lucide-react'
import { useState } from 'react'
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
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  checkRelayShare,
  createRelayShare,
  listMyRelayShares,
  listRelayShares,
  type RelayCreateInput,
  type RelayShare,
  type RelayStatus,
  voteRelayShare,
} from './api'

function statusLabel(status: RelayStatus) {
  return {
    healthy: '检测通过',
    degraded: '部分可用',
    failed: '检测失败',
    unknown: '尚未检测',
  }[status]
}
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
  onCheck,
  onVote,
}: {
  share: RelayShare
  mine?: boolean
  onCheck?: () => void
  onVote?: (usable: boolean) => void
}) {
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
            {share.kind === 'charity' ? '公益站' : '商业站'}
          </Badge>
        </div>
        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
          <span>{share.protocol === 'sub2api' ? 'Sub2API' : 'New API'}</span>
          <span className='inline-flex items-center gap-1'>
            {statusIcon(share.check_status)}
            {statusLabel(share.check_status)}
          </span>
        </div>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground line-clamp-3 text-sm leading-6 whitespace-pre-wrap'>
          {share.content || '分享者未填写说明。'}
        </p>
        <div className='grid grid-cols-3 gap-2 text-xs'>
          <Metric
            label='模型'
            value={share.model_count ? `${share.model_count} 个` : '—'}
          />
          <Metric
            label='延迟'
            value={share.latency_ms ? `${share.latency_ms} ms` : '—'}
          />
          <Metric label='最近检测' value={formatTime(share.last_checked_at)} />
        </div>
        <p className='text-muted-foreground text-xs'>
          {share.check_reason || '平台尚未完成检测。'}
          {share.updated_at ? ` · 更新于 ${formatTime(share.updated_at)}` : ''}
        </p>
        {mine && (
          <div className='bg-muted/30 flex items-center justify-between rounded-lg border p-3 text-xs'>
            <span className='inline-flex items-center gap-1.5'>
              <KeyRound className='size-3.5' />
              {share.credential_configured
                ? `检测 Key 已配置 · 尾号 ${share.key_suffix}`
                : '未配置检测 Key'}
            </span>
            <Button size='sm' variant='outline' onClick={onCheck}>
              立即检测
            </Button>
          </div>
        )}
        {!mine && onVote && (
          <div className='flex gap-2 border-t pt-3'>
            <Button size='sm' variant='outline' onClick={() => onVote(true)}>
              仍可用 · {share.vote_usable}
            </Button>
            <Button size='sm' variant='ghost' onClick={() => onVote(false)}>
              已失效 · {share.vote_unavailable}
            </Button>
          </div>
        )}
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

export function RelayShare() {
  const auth = useAuthStore((state) => state.auth)
  const client = useQueryClient()
  const [kind, setKind] = useState<'all' | 'charity' | 'commercial'>('all')
  const [showForm, setShowForm] = useState(false)
  const publicQuery = useQuery({
    queryKey: ['relay-shares'],
    queryFn: listRelayShares,
  })
  const mineQuery = useQuery({
    queryKey: ['relay-shares-mine'],
    queryFn: listMyRelayShares,
    enabled: !!auth?.user,
  })
  const check = useMutation({
    mutationFn: checkRelayShare,
    onSuccess: () => {
      toast.success('检测已完成')
      void client.invalidateQueries({ queryKey: ['relay-shares'] })
      void client.invalidateQueries({ queryKey: ['relay-shares-mine'] })
    },
    onError: (error) => handleServerError(error),
  })
  const vote = useMutation({
    mutationFn: ({ id, usable }: { id: string; usable: boolean }) =>
      voteRelayShare(id, usable),
    onSuccess: () => {
      toast.success('感谢反馈')
      void client.invalidateQueries({ queryKey: ['relay-shares'] })
    },
    onError: (error) => handleServerError(error),
  })
  const create = useMutation({
    mutationFn: createRelayShare,
    onSuccess: () => {
      toast.success('中转站已提交并开始检测')
      setShowForm(false)
      void client.invalidateQueries({ queryKey: ['relay-shares'] })
      void client.invalidateQueries({ queryKey: ['relay-shares-mine'] })
    },
    onError: (error) => handleServerError(error),
  })
  if (publicQuery.isPending)
    return (
      <PublicLayout>
        <LoadingState />
      </PublicLayout>
    )
  if (publicQuery.isError)
    return (
      <PublicLayout>
        <ErrorState onRetry={() => void publicQuery.refetch()} />
      </PublicLayout>
    )
  const items = (publicQuery.data?.data?.items ?? []).filter(
    (item) => kind === 'all' || item.kind === kind
  )
  const mine = mineQuery.data?.data?.items ?? []
  return (
    <PublicLayout showMainContainer={false}>
      <PublicPageBackdrop>
        <div className='mx-auto w-full max-w-[1280px] space-y-8 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div className='flex flex-wrap items-end justify-between gap-4'>
            <div>
              <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
                社区资源
              </p>
              <h1 className='mt-1 text-3xl font-semibold tracking-tight'>
                中转站分享
              </h1>
              <p className='text-muted-foreground mt-2 max-w-2xl text-sm leading-6'>
                只分公益站和商业站。分享者提交自己的检测 Key
                后，平台会定期做低成本协议检查；公开页面只展示脱敏状态和投票，不展示
                Key。
              </p>
            </div>
            <Button onClick={() => setShowForm((value) => !value)}>
              <RadioTower />
              分享中转站
            </Button>
          </div>
          {showForm && auth?.user ? (
            <RelayEditor
              onSubmit={(input) => create.mutate(input)}
              pending={create.isPending}
              onCancel={() => setShowForm(false)}
            />
          ) : null}
          {showForm && !auth?.user ? (
            <Card>
              <CardContent className='flex items-center justify-between gap-4 p-5 text-sm'>
                <span>登录后才能提交中转站和检测 Key。</span>
                <Button render={<Link to='/sign-in' />}>登录</Button>
              </CardContent>
            </Card>
          ) : null}
          {auth?.user && mine.length > 0 ? (
            <section className='space-y-4'>
              <div className='flex items-center gap-2'>
                <ShieldCheck className='text-primary size-5' />
                <h2 className='text-lg font-semibold'>我的中转站</h2>
              </div>
              <div className='grid gap-4 md:grid-cols-2'>
                {mine.map((share) => (
                  <RelayCard
                    key={share.public_id}
                    share={share}
                    mine
                    onCheck={() => check.mutate(share.public_id)}
                  />
                ))}
              </div>
            </section>
          ) : null}
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div>
              <h2 className='text-lg font-semibold'>公开资源</h2>
              <p className='text-muted-foreground mt-1 text-sm'>
                按最近更新时间浏览，不做综合排名。
              </p>
            </div>
            <div className='flex gap-2'>
              {(['all', 'charity', 'commercial'] as const).map((value) => (
                <button
                  key={value}
                  type='button'
                  onClick={() => setKind(value)}
                  className={`rounded-full border px-3 py-1.5 text-xs ${kind === value ? 'border-primary bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}
                >
                  {value === 'all'
                    ? '全部'
                    : value === 'charity'
                      ? '公益站'
                      : '商业站'}
                </button>
              ))}
            </div>
          </div>
          {items.length === 0 ? (
            <EmptyState
              title='还没有公开中转站'
              description='你可以成为第一个提交者。'
            />
          ) : (
            <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-3'>
              {items.map((share) => (
                <RelayCard
                  key={share.public_id}
                  share={share}
                  onVote={(usable) =>
                    vote.mutate({ id: share.public_id, usable })
                  }
                />
              ))}
            </div>
          )}
        </div>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}

function RelayEditor({
  onSubmit,
  pending,
  onCancel,
}: {
  onSubmit: (input: RelayCreateInput) => void
  pending: boolean
  onCancel: () => void
}) {
  const [title, setTitle] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [key, setKey] = useState('')
  const [content, setContent] = useState('')
  const [kind, setKind] = useState<'charity' | 'commercial'>('charity')
  const [protocol, setProtocol] = useState<'new-api' | 'sub2api'>('new-api')
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>分享中转站</CardTitle>
        <p className='text-muted-foreground text-sm'>
          Key 只用于服务端检测，保存后不会再次显示。请确认你有权提供此
          Key，检测可能产生少量上游用量。
        </p>
      </CardHeader>
      <CardContent className='grid gap-4 md:grid-cols-2'>
        <div className='space-y-2'>
          <Label>名称</Label>
          <Input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder='例如：我的公益 New API'
          />
        </div>
        <div className='space-y-2'>
          <Label>站点地址</Label>
          <Input
            value={baseURL}
            onChange={(event) => setBaseURL(event.target.value)}
            placeholder='https://example.com'
          />
        </div>
        <div className='space-y-2'>
          <Label>站点类型</Label>
          <select
            value={kind}
            onChange={(event) => setKind(event.target.value as typeof kind)}
            className='border-input bg-background h-10 w-full rounded-md border px-3 text-sm'
          >
            <option value='charity'>公益站</option>
            <option value='commercial'>商业站</option>
          </select>
        </div>
        <div className='space-y-2'>
          <Label>协议</Label>
          <select
            value={protocol}
            onChange={(event) =>
              setProtocol(event.target.value as typeof protocol)
            }
            className='border-input bg-background h-10 w-full rounded-md border px-3 text-sm'
          >
            <option value='new-api'>New API</option>
            <option value='sub2api'>Sub2API</option>
          </select>
        </div>
        <div className='space-y-2 md:col-span-2'>
          <Label>检测 Key</Label>
          <Input
            type='password'
            value={key}
            onChange={(event) => setKey(event.target.value)}
            placeholder='只提交用于检测的 Key'
          />
        </div>
        <div className='space-y-2 md:col-span-2'>
          <Label>介绍（支持 Markdown）</Label>
          <Textarea
            value={content}
            onChange={(event) => setContent(event.target.value)}
            rows={6}
            placeholder='写清楚站点特色、限制、适合什么模型和最后验证时间。'
          />
        </div>
        <div className='flex justify-end gap-2 md:col-span-2'>
          <Button variant='outline' onClick={onCancel}>
            取消
          </Button>
          <Button
            disabled={
              pending || !title.trim() || !baseURL.trim() || !key.trim()
            }
            onClick={() =>
              onSubmit({
                title,
                base_url: baseURL,
                kind,
                protocol,
                content,
                key,
              })
            }
          >
            提交并检测
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
