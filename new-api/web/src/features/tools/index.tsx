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
import { ClipboardCheck, Clock3, KeyRound, ShieldCheck } from 'lucide-react'
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
  listAuditRuns,
  listCheckinSites,
  listRelayOptions,
  runCheckinSite,
  toolsQueryKeys,
} from './api'
import { AuditForm } from './components/audit-panel'
import { AuditResults } from './components/audit-results'
import { CheckinForm, CheckinList } from './components/checkin-panel'
import { IntelligencePanel } from './components/intelligence-panel'
import { riskLevelKey } from './lib/format'

export function ToolsPage() {
  const { t } = useTranslation()
  const auth = useAuthStore((state) => state.auth)
  const signedIn = !!auth?.user
  const client = useQueryClient()
  const [auditOpen, setAuditOpen] = useState(false)
  const [checkinOpen, setCheckinOpen] = useState(false)

  const audits = useQuery({
    queryKey: toolsQueryKeys.audits,
    queryFn: listAuditRuns,
    enabled: signedIn,
  })
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
    // A failed run is still recorded server-side, so refresh either way.
    onSettled: () =>
      void client.invalidateQueries({ queryKey: toolsQueryKeys.audits }),
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
  const createSite = useMutation({
    mutationFn: createCheckinSite,
    onSuccess: () => {
      toast.success(t('Check-in site saved'))
      void client.invalidateQueries({ queryKey: toolsQueryKeys.checkinSites })
      setCheckinOpen(false)
    },
    onError: (error) => handleServerError(error),
  })

  const relayItems = relays.data?.data?.items ?? []
  const auditItems = audits.data?.data?.items ?? []
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
                'Tools run on the server under strict limits and only keep masked summaries. A "needs review" result is a lead, not proof that a model is diluted or a relay rewrites traffic.'
              )}
            </p>
          </div>

          <div className='grid gap-4 md:grid-cols-2'>
            <Card>
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
                    'Checks protocol and model list, token usage consistency, prompt injection and override, model identity, SSE stream integrity and error-response leakage. No jailbreak or attack payloads are sent.'
                  )}
                </p>
                <Button
                  aria-expanded={auditOpen}
                  onClick={() => setAuditOpen((value) => !value)}
                  disabled={!signedIn}
                >
                  <ClipboardCheck />
                  {t('Start audit')}
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

          <IntelligencePanel signedIn={signedIn} />

          {signedIn ? (
            <div className='grid gap-6 lg:grid-cols-2'>
              <AuditResults audits={auditItems} loading={audits.isPending} />
              <CheckinList
                sites={siteItems}
                loading={sites.isPending}
                runningId={runningId}
                onRun={(id) => run.mutate(id)}
              />
            </div>
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

          <Card>
            <CardHeader>
              <CardTitle className='text-base'>
                {t('Audit boundaries')}
              </CardTitle>
            </CardHeader>
            <CardContent className='grid gap-3 text-sm leading-6 md:grid-cols-3'>
              <p>
                {t(
                  'Only status, sample counts, latency and masked summaries are stored; your key never reaches the browser.'
                )}
              </p>
              <p>
                {t(
                  'Self-reported identity can be forged by a system prompt, and small models may ignore instructions, so single results need review.'
                )}
              </p>
              <p>
                {t(
                  'Methods follow public relay-audit research (api-relay-audit, veridrop, modelprint) and are re-implemented server-side.'
                )}
              </p>
            </CardContent>
          </Card>
        </div>
      </PublicPageBackdrop>
    </PublicLayout>
  )
}
