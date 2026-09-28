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
import { zodResolver } from '@hookform/resolvers/zod'
import { FlaskConical, Loader2 } from 'lucide-react'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import type { DilutionReport, ProbeTargetInput, RelayOption } from '../api'
import { dilutionVerdict } from '../lib/format'
import {
  PROBE_TARGET_DEFAULTS,
  probeTargetSchema,
  toProbeTarget,
  type ProbeTargetValues,
} from '../lib/probe-target'
import { ProbeTargetFields } from './probe-target-fields'

export function DilutionForm({
  relays,
  pending,
  onSubmit,
}: {
  relays: RelayOption[]
  pending: boolean
  onSubmit: (input: ProbeTargetInput) => void
}) {
  const { t } = useTranslation()
  const schema = useMemo(() => probeTargetSchema(t), [t])
  const form = useForm<ProbeTargetValues>({
    resolver: zodResolver(schema),
    defaultValues: PROBE_TARGET_DEFAULTS,
  })
  const source = form.watch('source')

  return (
    <Form {...form}>
      <form
        noValidate
        className='bg-muted/20 grid gap-3 rounded-lg border p-3'
        onSubmit={form.handleSubmit((values) =>
          onSubmit(toProbeTarget(values))
        )}
      >
        <ProbeTargetFields
          control={form.control}
          source={source}
          relays={relays}
        />
        <Button type='submit' disabled={pending}>
          {pending ? <Loader2 className='animate-spin' /> : <FlaskConical />}
          {pending
            ? t('Checking, up to 3 minutes')
            : t('Run substitution check')}
        </Button>
      </form>
    </Form>
  )
}

export function DilutionResult({ report }: { report: DilutionReport }) {
  const { t } = useTranslation()
  const overall = dilutionVerdict(report.verdict)
  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-2'>
        <Badge variant='secondary'>{report.model}</Badge>
        <Badge variant={overall.variant}>{t(overall.label)}</Badge>
        <span className='text-muted-foreground text-xs tabular-nums'>
          {t('Consistency {{score}}/100', { score: report.score })}
        </span>
        {report.family ? (
          <span className='text-muted-foreground text-xs'>
            {t('Matched against {{family}} public traits', {
              family: report.family,
            })}
          </span>
        ) : null}
      </div>
      <ul className='divide-y rounded-lg border'>
        {report.findings.map((finding) => {
          const tone = dilutionVerdict(finding.verdict)
          return (
            <li key={finding.id} className='space-y-1 p-3'>
              <div className='flex items-center justify-between gap-2'>
                <span className='text-sm font-medium'>{finding.name}</span>
                <Badge variant={tone.variant}>{t(tone.label)}</Badge>
              </div>
              <p className='text-muted-foreground text-xs leading-5'>
                {finding.evidence}
              </p>
            </li>
          )
        })}
      </ul>
      {/* The disclaimer is generated server-side and states the hard limit:
          this compares against published traits, it cannot prove which model
          actually answered. */}
      <p className='text-muted-foreground text-xs leading-5'>
        {report.disclaimer}
      </p>
    </div>
  )
}
