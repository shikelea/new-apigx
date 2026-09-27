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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import type { AuditCheck, AuditRun } from '../api'
import {
  auditStatus,
  formatEpoch,
  riskLevelKey,
  riskLevelVariant,
} from '../lib/format'

function CheckRow({ check }: { check: AuditCheck }) {
  const { t } = useTranslation()
  const meta = auditStatus(check.status)
  return (
    <li className='space-y-1 border-t pt-2 first:border-t-0 first:pt-0'>
      <div className='flex items-center justify-between gap-2'>
        <span className='font-medium'>{check.name}</span>
        <Badge variant={meta.variant}>{t(meta.label)}</Badge>
      </div>
      <p className='text-muted-foreground text-xs leading-5 break-words'>
        {check.evidence || t('No evidence recorded')}
      </p>
    </li>
  )
}

function AuditRunCard({ run }: { run: AuditRun }) {
  const { t } = useTranslation()
  const checks = run.checks ?? []
  const failed = run.status === 'failed'
  return (
    <article className='rounded-lg border p-3 text-sm'>
      <header className='flex flex-wrap items-start justify-between gap-2'>
        <div className='min-w-0'>
          <p className='truncate font-medium'>{run.model}</p>
          <p className='text-muted-foreground truncate text-xs'>
            {run.relay_title || t('Deleted relay')} ·{' '}
            {formatEpoch(run.finished_at || run.started_at)}
          </p>
        </div>
        <Badge
          variant={failed ? 'destructive' : riskLevelVariant(run.risk_level)}
        >
          {failed
            ? t('Audit failed')
            : t(riskLevelKey(run.risk_level || '证据不足'))}
        </Badge>
      </header>
      {failed ? (
        <p className='text-destructive mt-2 text-xs'>
          {run.error_message || t('The audit did not finish')}
        </p>
      ) : null}
      {checks.length > 0 ? (
        <details className='mt-3'>
          <summary className='text-primary cursor-pointer text-xs'>
            {t('View {{count}} checks', { count: checks.length })}
          </summary>
          <ul className='mt-3 space-y-2'>
            {checks.map((check) => (
              <CheckRow key={check.id} check={check} />
            ))}
          </ul>
        </details>
      ) : null}
    </article>
  )
}

export function AuditResults({
  audits,
  loading,
}: {
  audits: AuditRun[]
  loading: boolean
}) {
  const { t } = useTranslation()
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>{t('Recent audits')}</CardTitle>
        <p className='text-muted-foreground text-xs leading-5'>
          {t(
            'Results are signals, not verdicts. Upstream versions, routing and sampling all affect them; review manually.'
          )}
        </p>
      </CardHeader>
      <CardContent className='space-y-3' aria-busy={loading}>
        {audits.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {loading ? t('Loading...') : t('No audits yet.')}
          </p>
        ) : (
          audits.map((run) => <AuditRunCard key={run.public_id} run={run} />)
        )}
      </CardContent>
    </Card>
  )
}
