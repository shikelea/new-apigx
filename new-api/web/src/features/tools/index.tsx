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
  Check,
  ClipboardCheck,
  Clock3,
  KeyRound,
  Play,
  ShieldCheck,
  TimerReset,
} from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { PublicLayout } from '@/components/layout/components/public-layout'
import { PublicPageBackdrop } from '@/components/layout/components/public-page-backdrop'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  createAuditRun,
  createCheckinSite,
  listAuditRuns,
  listCheckinSites,
  listRelayOptions,
  runCheckinSite,
  type AuditRun,
  type CheckinSite,
  type RelayOption,
} from './api'

function formatTime(epoch: number) {
  return epoch ? new Date(epoch * 1000).toLocaleString() : '—'
}

export function ToolsPage() {
  const auth = useAuthStore((state) => state.auth)
  const client = useQueryClient()
  const [auditOpen, setAuditOpen] = useState(false)
  const [checkinOpen, setCheckinOpen] = useState(false)

  const audits = useQuery({
    queryKey: ['apigx-audit-runs'],
    queryFn: listAuditRuns,
    enabled: !!auth?.user,
  })
  const relays = useQuery({
    queryKey: ['relay-options'],
    queryFn: listRelayOptions,
    enabled: !!auth?.user,
  })
  const sites = useQuery({
    queryKey: ['apigx-checkin-sites'],
    queryFn: listCheckinSites,
    enabled: !!auth?.user,
  })

  const audit = useMutation({
    mutationFn: createAuditRun,
    onSuccess: () => {
      toast.success('审计已完成')
      void client.invalidateQueries({ queryKey: ['apigx-audit-runs'] })
      setAuditOpen(false)
    },
    onError: (error) => handleServerError(error),
  })
  const run = useMutation({
    mutationFn: runCheckinSite,
    onSuccess: () => {
      toast.success('签到请求已执行')
      void client.invalidateQueries({ queryKey: ['apigx-checkin-sites'] })
    },
    onError: (error) => handleServerError(error),
  })
  const createSite = useMutation({
    mutationFn: createCheckinSite,
    onSuccess: () => {
      toast.success('签到站点已保存')
      void client.invalidateQueries({ queryKey: ['apigx-checkin-sites'] })
      setCheckinOpen(false)
    },
    onError: (error) => handleServerError(error),
  })

  const relayItems = relays.data?.data?.items ?? []
  const auditItems = audits.data?.data?.items ?? []
  const siteItems = sites.data?.data?.items ?? []

  return (
    <PublicLayout showMainContainer={false}>
      <PublicPageBackdrop>
        <div className='mx-auto w-full max-w-[1280px] space-y-8 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div>
            <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
              APIGX 工具
            </p>
            <h1 className='mt-1 text-3xl font-semibold tracking-tight'>
              工具中心
            </h1>
            <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-6'>
              工具由服务端受控执行，结果只保留脱敏摘要。安全审计中的“需复核”是风险线索，
              不代表能够绝对证明模型掺水或中转站恶意改写。
            </p>
          </div>

          <div className='grid gap-4 md:grid-cols-2'>
            <Card>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <ShieldCheck className='text-primary size-5' />
                  供应商安全审计
                </CardTitle>
                <Badge variant='outline'>基础审计已接入</Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  先检查协议和模型列表，再按阶段接入 Token
                  用量、流式完整性、错误泄漏和模型身份探针。
                  攻击性探针默认不会自动发送。
                </p>
                <Button
                  onClick={() => setAuditOpen((value) => !value)}
                  disabled={!auth?.user}
                >
                  <ClipboardCheck />
                  开始审计
                </Button>
                {auditOpen && auth?.user ? (
                  <AuditForm
                    relays={relayItems}
                    pending={audit.isPending}
                    onSubmit={(input) => audit.mutate(input)}
                  />
                ) : null}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Clock3 className='text-primary size-5' />
                  New API 签到
                </CardTitle>
                <Badge variant='outline'>固定时间执行</Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  添加你有权使用的 New API
                  站点，平台按你设置的时间代发签到请求。凭据只保存加密引用，
                  页面不会回显。
                </p>
                <Button
                  onClick={() => setCheckinOpen((value) => !value)}
                  disabled={!auth?.user}
                >
                  <KeyRound />
                  添加签到站点
                </Button>
                {checkinOpen && auth?.user ? (
                  <CheckinForm
                    pending={createSite.isPending}
                    onSubmit={(input) => createSite.mutate(input)}
                  />
                ) : null}
              </CardContent>
            </Card>
          </div>

          {auth?.user ? (
            <div className='grid gap-6 lg:grid-cols-2'>
              <AuditList audits={auditItems} />
              <CheckinList sites={siteItems} onRun={(id) => run.mutate(id)} />
            </div>
          ) : (
            <Card>
              <CardContent className='flex items-center justify-between gap-4 p-5 text-sm'>
                <span>登录后可以运行审计并管理签到站点。</span>
                <Button render={<Link to='/sign-in' />}>登录</Button>
              </CardContent>
            </Card>
          )}

          <Card>
            <CardHeader>
              <CardTitle className='text-base'>检测边界</CardTitle>
            </CardHeader>
            <CardContent className='grid gap-3 text-sm leading-6 md:grid-cols-3'>
              <p>只保存状态、样本数、延迟和脱敏摘要。</p>
              <p>第三方检测项目作为隔离参考，不把浏览器变成 Key 执行环境。</p>
              <p>身份替换、越狱、Web3、多模态指纹会在能力接入后单独显示。</p>
            </CardContent>
          </Card>
        </div>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}

function AuditForm({
  relays,
  pending,
  onSubmit,
}: {
  relays: RelayOption[]
  pending: boolean
  onSubmit: (input: { relay_share_public_id: string; model: string }) => void
}) {
  const [share, setShare] = useState('')
  const [model, setModel] = useState('')

  return (
    <div className='bg-muted/20 grid gap-3 rounded-lg border p-3'>
      <div className='space-y-1'>
        <Label>选择中转站</Label>
        <Select value={share} onValueChange={(value) => setShare(value ?? '')}>
          <SelectTrigger className='w-full'>
            <SelectValue placeholder='选择一个已配置检测 Key 的站点' />
          </SelectTrigger>
          <SelectContent>
            {relays.map((item) => (
              <SelectItem key={item.public_id} value={item.public_id}>
                {item.title} · {item.kind === 'charity' ? '公益站' : '商业站'}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className='space-y-1'>
        <Label>模型 ID</Label>
        <Input
          value={model}
          onChange={(event) => setModel(event.target.value)}
          placeholder='例如 deepseek-chat'
        />
      </div>
      <Button
        disabled={pending || !share || !model.trim()}
        onClick={() =>
          onSubmit({ relay_share_public_id: share, model: model.trim() })
        }
      >
        <Play />
        运行
      </Button>
    </div>
  )
}

function CheckinForm({
  pending,
  onSubmit,
}: {
  pending: boolean
  onSubmit: (input: {
    name: string
    base_url: string
    key: string
    credential_kind: string
    timezone: string
    schedule_minute: number
  }) => void
}) {
  const [name, setName] = useState('')
  const [base, setBase] = useState('')
  const [key, setKey] = useState('')
  const [kind, setKind] = useState('bearer')
  const [timeValue, setTimeValue] = useState('09:00')

  return (
    <div className='bg-muted/20 grid gap-3 rounded-lg border p-3'>
      <Input
        value={name}
        onChange={(event) => setName(event.target.value)}
        placeholder='站点名称'
      />
      <Input
        value={base}
        onChange={(event) => setBase(event.target.value)}
        placeholder='https://example.com'
      />
      <div className='space-y-1'>
        <Label>凭据类型</Label>
        <Select
          value={kind}
          onValueChange={(value) => setKind(value ?? 'bearer')}
        >
          <SelectTrigger className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='bearer'>Bearer Token</SelectItem>
            <SelectItem value='session_cookie'>会话 Cookie</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <Input
        type='password'
        value={key}
        onChange={(event) => setKey(event.target.value)}
        placeholder={kind === 'bearer' ? 'Bearer Token' : 'session Cookie 值'}
      />
      <div className='space-y-1'>
        <Label>每日签到时间</Label>
        <Input
          value={timeValue}
          onChange={(event) => setTimeValue(event.target.value)}
          type='time'
        />
      </div>
      <Button
        disabled={pending || !name.trim() || !base.trim() || !key.trim()}
        onClick={() => {
          const [hours, minutes] = timeValue.split(':').map(Number)
          onSubmit({
            name: name.trim(),
            base_url: base.trim(),
            key,
            credential_kind: kind,
            timezone: 'Asia/Shanghai',
            schedule_minute: hours * 60 + minutes,
          })
        }}
      >
        <Check />
        保存站点
      </Button>
    </div>
  )
}

function AuditList({ audits }: { audits: AuditRun[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>最近审计</CardTitle>
      </CardHeader>
      <CardContent className='space-y-3'>
        {audits.length === 0 ? (
          <p className='text-muted-foreground text-sm'>还没有审计记录。</p>
        ) : (
          audits.map((item) => (
            <div key={item.public_id} className='rounded-lg border p-3 text-sm'>
              <div className='flex justify-between gap-3'>
                <span className='font-medium'>{item.model}</span>
                <Badge variant='outline'>{item.risk_level}</Badge>
              </div>
              <p className='text-muted-foreground mt-1'>
                {item.evidence_summary}
              </p>
              <p className='text-muted-foreground mt-2 text-xs'>
                {formatTime(item.finished_at)} · {item.sample_count} 个模型样本
              </p>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  )
}

function CheckinList({
  sites,
  onRun,
}: {
  sites: CheckinSite[]
  onRun: (id: number) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>我的签到站点</CardTitle>
      </CardHeader>
      <CardContent className='space-y-3'>
        {sites.length === 0 ? (
          <p className='text-muted-foreground text-sm'>还没有签到站点。</p>
        ) : (
          sites.map((site) => (
            <div key={site.id} className='rounded-lg border p-3 text-sm'>
              <div className='flex items-center justify-between gap-3'>
                <div className='min-w-0'>
                  <p className='font-medium'>{site.name}</p>
                  <p className='text-muted-foreground truncate text-xs'>
                    {site.base_url}
                  </p>
                </div>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => onRun(site.id)}
                >
                  <TimerReset />
                  立即签到
                </Button>
              </div>
              <p className='text-muted-foreground mt-2 text-xs'>
                每日{' '}
                {String(Math.floor(site.schedule_minute / 60)).padStart(2, '0')}
                :{String(site.schedule_minute % 60).padStart(2, '0')} ·{' '}
                {site.last_status || '尚未执行'} ·{' '}
                {formatTime(site.last_run_at)}
              </p>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  )
}
