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
  Beaker,
  ChevronUp,
  ClipboardCheck,
  Clock3,
  ExternalLink,
  FlaskConical,
  Gauge,
  KeyRound,
  ShieldCheck,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PublicLayout } from '@/components/layout/components/public-layout'
import { PublicPageBackdrop } from '@/components/layout/components/public-page-backdrop'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  createAuditRun,
  createCheckinSite,
  createCrowdtest,
  listCheckinSites,
  listMyCrowdtests,
  listRelayOptions,
  publishCrowdtest,
  runCheckinSite,
  runDilutionTest,
  runSpeedTest,
  toolsQueryKeys,
  type DilutionReport,
  type SpeedReport,
} from './api'
import { AuditForm } from './components/audit-panel'
import { CheckinForm, CheckinList } from './components/checkin-panel'
import { CrowdtestCard, CrowdtestForm } from './components/crowdtest-panel'
import { DilutionForm, DilutionResult } from './components/dilution-panel'
import { SpeedForm, SpeedResult } from './components/speed-panel'
import { riskLevelKey } from './lib/format'

export function ToolsPage() {
  const { t } = useTranslation()
  const auth = useAuthStore((state) => state.auth)
  const signedIn = !!auth?.user
  const client = useQueryClient()
  const [auditOpen, setAuditOpen] = useState(false)
  const [checkinOpen, setCheckinOpen] = useState(false)
  const [speedOpen, setSpeedOpen] = useState(false)
  const [dilutionOpen, setDilutionOpen] = useState(false)
  const [crowdtestOpen, setCrowdtestOpen] = useState(false)
  // Speed and dilution results are not persisted server-side, so the latest
  // report lives here until the user runs another one.
  const [speedReport, setSpeedReport] = useState<SpeedReport | null>(null)
  const [dilutionReport, setDilutionReport] = useState<DilutionReport | null>(
    null
  )

  const relays = useQuery({
    queryKey: toolsQueryKeys.relayOptions,
    queryFn: listRelayOptions,
    enabled: signedIn,
  })
  const sites = useQuery({
    queryKey: toolsQueryKeys.checkinSites,
    queryFn: listCheckinSites,
    enabled: signedIn,
  })

  const audit = useMutation({
    mutationFn: createAuditRun,
    onSuccess: (res) => {
      toast.success(
        t('Audit finished: {{level}}', {
          level: t(riskLevelKey(res.data?.risk_level ?? '')),
        })
      )
      setAuditOpen(false)
    },
    onError: (error) => handleServerError(error),
  })
  const run = useMutation({
    mutationFn: runCheckinSite,
    onSuccess: (res) =>
      toast.success(res.data?.message || t('Check-in request sent')),
    onError: (error) => handleServerError(error),
    // Failures also update last_status/failure_count.
    onSettled: () =>
      void client.invalidateQueries({ queryKey: toolsQueryKeys.checkinSites }),
  })
  const speed = useMutation({
    mutationFn: runSpeedTest,
    onMutate: () => {
      setSpeedReport(null)
    },
    onSuccess: (res) => {
      if (res.data) setSpeedReport(res.data)
    },
    onError: (error) => {
      handleServerError(error)
      setSpeedReport(null)
    },
  })
  const dilution = useMutation({
    mutationFn: runDilutionTest,
    onMutate: () => {
      setDilutionReport(null)
    },
    onSuccess: (res) => {
      if (res.data) setDilutionReport(res.data)
    },
    onError: (error) => {
      handleServerError(error)
      setDilutionReport(null)
    },
  })
  const crowdtest = useMutation({
    mutationFn: createCrowdtest,
    onSuccess: (res) => {
      toast.success(
        t('Crowdtest scored {{score}}/100', { score: res.data?.score ?? 0 })
      )
      setCrowdtestOpen(false)
    },
    onError: (error) => handleServerError(error),
    // A failed run is recorded as a draft so the history has no unexplained gap.
    onSettled: () =>
      void client.invalidateQueries({ queryKey: toolsQueryKeys.myCrowdtests }),
  })
  const publish = useMutation({
    mutationFn: publishCrowdtest,
    onSuccess: () => {
      toast.success(t('Result published'))
      void client.invalidateQueries({ queryKey: toolsQueryKeys.myCrowdtests })
    },
    onError: (error) => handleServerError(error),
  })
  const createSite = useMutation({
    mutationFn: createCheckinSite,
    onSuccess: () => {
      toast.success(t('Check-in site saved'))
      void client.invalidateQueries({ queryKey: toolsQueryKeys.checkinSites })
      setCheckinOpen(false)
    },
    onError: (error) => handleServerError(error),
  })

  const myCrowdtests = useQuery({
    queryKey: toolsQueryKeys.myCrowdtests,
    queryFn: listMyCrowdtests,
    enabled: signedIn,
  })

  const relayItems = relays.data?.data?.items ?? []
  const crowdtestItems = myCrowdtests.data?.data?.items ?? []
  const siteItems = sites.data?.data?.items ?? []
  const runningId = run.isPending ? (run.variables ?? null) : null

  return (
    <PublicLayout showMainContainer={false}>
      <PublicPageBackdrop>
        <div className='mx-auto w-full max-w-[1280px] space-y-8 px-4 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
          <div>
            <p className='text-primary text-xs font-semibold tracking-[0.18em] uppercase'>
              {t('APIGX Tools')}
            </p>
            <h1 className='mt-1 text-3xl font-semibold tracking-tight'>
              {t('Tool Center')}
            </h1>
            <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-6'>
              {t(
                'Everything runs server-side under rate limits, and only masked summaries are kept. Your key never reaches the browser. Treat a "needs review" result as a lead worth chasing, not as proof.'
              )}
            </p>
          </div>

          <div className='grid items-start gap-6 md:grid-cols-2'>
            <Card className='rounded-xl border shadow-sm transition-all'>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <ShieldCheck className='text-primary size-5' />
                  {t('Supplier audit')}
                </CardTitle>
                <Badge variant='outline'>{t('6 checks')}</Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Checks protocol and model list, token usage consistency, prompt injection and override, model identity, SSE stream integrity and error-response leakage. Works on one of your saved relays or on an address you paste in once. No jailbreak or attack payloads are sent.'
                  )}
                </p>
                <Button
                  variant={auditOpen ? 'secondary' : 'default'}
                  aria-expanded={auditOpen}
                  onClick={() => setAuditOpen((value) => !value)}
                  disabled={!signedIn}
                >
                  {auditOpen ? (
                    <ChevronUp className='size-4' />
                  ) : (
                    <ClipboardCheck className='size-4' />
                  )}
                  {auditOpen
                    ? t('Collapse') || '收起审计'
                    : t('Start audit') || '展开审计测试'}
                </Button>
                {auditOpen && signedIn ? (
                  <AuditForm
                    relays={relayItems}
                    pending={audit.isPending}
                    onSubmit={(input) => audit.mutate(input)}
                  />
                ) : null}
              </CardContent>
            </Card>

            <Card className='rounded-xl border shadow-sm transition-all'>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Gauge className='text-primary size-5' />
                  {t('Speed test')}
                </CardTitle>
                <Badge variant='outline'>{t('6 samples, two lengths')}</Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Six streamed samples split between short and long output, with a different prompt each time so upstream caches cannot flatter the result. Reports first-token latency, output tokens per second, and whether the answer arrives as one buffered chunk.'
                  )}
                </p>
                <Button
                  variant={speedOpen ? 'secondary' : 'default'}
                  aria-expanded={speedOpen}
                  onClick={() => setSpeedOpen((value) => !value)}
                  disabled={!signedIn}
                >
                  {speedOpen ? (
                    <ChevronUp className='size-4' />
                  ) : (
                    <Gauge className='size-4' />
                  )}
                  {speedOpen
                    ? t('Collapse') || '收起测速'
                    : t('Open speed test') || '展开速度测试'}
                </Button>
                {speedOpen && signedIn ? (
                  <SpeedForm
                    relays={relayItems}
                    pending={speed.isPending}
                    onSubmit={(input) => speed.mutate(input)}
                  />
                ) : null}
                {speedReport ? <SpeedResult report={speedReport} /> : null}
              </CardContent>
            </Card>

            <Card className='rounded-xl border shadow-sm transition-all'>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <FlaskConical className='text-primary size-5' />
                  {t('Model substitution check')}
                </CardTitle>
                <Badge variant='outline'>{t('8 dimensions')}</Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Pick a model from the supported list, then the check compares protocol fields, knowledge cutoff, answer-choice distribution, reasoning stability, structured output, instruction hierarchy, stream integrity and reasoning effort against what that model actually does.'
                  )}
                </p>
                <Button
                  variant={dilutionOpen ? 'secondary' : 'default'}
                  aria-expanded={dilutionOpen}
                  onClick={() => setDilutionOpen((value) => !value)}
                  disabled={!signedIn}
                >
                  {dilutionOpen ? (
                    <ChevronUp className='size-4' />
                  ) : (
                    <FlaskConical className='size-4' />
                  )}
                  {dilutionOpen
                    ? t('Collapse') || '收起掺水检测'
                    : t('Open substitution check') || '展开掺水检测'}
                </Button>
                {dilutionOpen && signedIn ? (
                  <DilutionForm
                    relays={relayItems}
                    pending={dilution.isPending}
                    onSubmit={(input) => dilution.mutate(input)}
                  />
                ) : null}
                {dilutionReport ? (
                  <DilutionResult report={dilutionReport} />
                ) : null}
              </CardContent>
            </Card>

            <Card className='rounded-xl border shadow-sm transition-all'>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Beaker className='text-primary size-5' />
                  {t('Crowdtest')}
                </CardTitle>
                <Badge variant='outline'>{t('Sharing is optional')}</Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Runs the candy or pelican benchmark against your own target. Publishing is up to you, and published entries carry the score without the address or key.'
                  )}
                </p>
                <div className='flex flex-wrap gap-2.5'>
                  <Button
                    variant={crowdtestOpen ? 'secondary' : 'default'}
                    aria-expanded={crowdtestOpen}
                    onClick={() => setCrowdtestOpen((value) => !value)}
                    disabled={!signedIn}
                  >
                    {crowdtestOpen ? (
                      <ChevronUp className='size-4' />
                    ) : (
                      <Beaker className='size-4' />
                    )}
                    {crowdtestOpen
                      ? t('Collapse') || '收起众测'
                      : t('Open crowdtest') || '展开众测'}
                  </Button>
                  <Button variant='outline' render={<Link to='/crowdtest' />}>
                    <ExternalLink className='size-3.5' />
                    {t('See published results') || '看公开的结果'}
                  </Button>
                </div>
                {crowdtestOpen && signedIn ? (
                  <CrowdtestForm
                    relays={relayItems}
                    pending={crowdtest.isPending}
                    onSubmit={(input) => crowdtest.mutate(input)}
                  />
                ) : null}
                {crowdtestItems.length > 0 ? (
                  <ul className='divide-y rounded-lg border'>
                    {crowdtestItems.slice(0, 5).map((item) => (
                      <CrowdtestCard
                        key={item.public_id}
                        test={item}
                        publishing={
                          publish.isPending &&
                          publish.variables === item.public_id
                        }
                        onPublish={(publicID) => publish.mutate(publicID)}
                      />
                    ))}
                  </ul>
                ) : null}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <Clock3 className='text-primary size-5' />
                  {t('New API check-in')}
                </CardTitle>
                <Badge variant='outline'>
                  {t('Runs daily at a fixed time')}
                </Badge>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm leading-6'>
                  {t(
                    'Add New API sites you are allowed to use and the platform checks in for you at the time you choose. Credentials are stored encrypted and never shown again.'
                  )}
                </p>
                <Button
                  aria-expanded={checkinOpen}
                  onClick={() => setCheckinOpen((value) => !value)}
                  disabled={!signedIn}
                >
                  <KeyRound />
                  {t('Add check-in site')}
                </Button>
                {checkinOpen && signedIn ? (
                  <CheckinForm
                    pending={createSite.isPending}
                    onSubmit={(input) => createSite.mutate(input)}
                  />
                ) : null}
              </CardContent>
            </Card>
          </div>

          {signedIn ? (
            <CheckinList
              sites={siteItems}
              loading={sites.isPending}
              runningId={runningId}
              onRun={(id) => run.mutate(id)}
            />
          ) : (
            <Card>
              <CardContent className='flex items-center justify-between gap-4 p-5 text-sm'>
                <span>
                  {t('Sign in to run audits and manage check-in sites.')}
                </span>
                <Button render={<Link to='/sign-in' />}>{t('Sign in')}</Button>
              </CardContent>
            </Card>
          )}
        </div>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}
