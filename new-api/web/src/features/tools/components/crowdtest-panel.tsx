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
import { Beaker, Loader2, Share2 } from 'lucide-react'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import type { Crowdtest, ProbeTargetInput, RelayOption } from '../api'
import { checkStatus, formatEpoch } from '../lib/format'
import {
  PROBE_TARGET_DEFAULTS,
  probeTargetSchema,
  toProbeTarget,
  type ProbeTargetValues,
} from '../lib/probe-target'
import { ProbeTargetFields } from './probe-target-fields'

// A crowdtest is a personal run you may optionally publish. Publishing shares
// the score only — never the address or the key — so nobody can re-run someone
// else's target and spend their quota.
export type CrowdtestValues = ProbeTargetValues & {
  benchmark: 'candy' | 'pelican'
  publish: boolean
}

export function CrowdtestForm({
  relays,
  pending,
  onSubmit,
}: {
  relays: RelayOption[]
  pending: boolean
  onSubmit: (
    input: ProbeTargetInput & {
      benchmark: 'candy' | 'pelican'
      publish: boolean
    }
  ) => void
}) {
  const { t } = useTranslation()
  const schema = useMemo(
    () =>
      z.intersection(
        probeTargetSchema(t),
        z.object({
          benchmark: z.enum(['candy', 'pelican']),
          publish: z.boolean(),
        })
      ),
    [t]
  )
  const form = useForm<CrowdtestValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      ...PROBE_TARGET_DEFAULTS,
      benchmark: 'candy',
      publish: false,
    },
  })
  const source = form.watch('source')

  return (
    <Form {...form}>
      <form
        noValidate
        className='bg-muted/20 grid gap-3 rounded-lg border p-3'
        onSubmit={form.handleSubmit((values) =>
          onSubmit({
            ...toProbeTarget(values),
            benchmark: values.benchmark,
            publish: values.publish,
          })
        )}
      >
        <FormField
          control={form.control}
          name='benchmark'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Benchmark')}</FormLabel>
              <FormControl>
                <NativeSelect className='w-full' {...field}>
                  <NativeSelectOption value='candy'>
                    {t('Candy test (multi-step arithmetic)')}
                  </NativeSelectOption>
                  <NativeSelectOption value='pelican'>
                    {t('Pelican test (SVG drawing)')}
                  </NativeSelectOption>
                </NativeSelect>
              </FormControl>
              <FormDescription className='text-xs'>
                {t(
                  'Candy problems are generated fresh each run, so a memorised answer cannot pass. Pelican results are scored on SVG structure only.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <ProbeTargetFields
          control={form.control}
          source={source}
          relays={relays}
        />
        <FormField
          control={form.control}
          name='publish'
          render={({ field }) => (
            <FormItem>
              <div className='flex items-start gap-2'>
                <FormControl>
                  <Checkbox
                    className='mt-0.5'
                    checked={field.value}
                    onCheckedChange={(checked) =>
                      field.onChange(checked === true)
                    }
                  />
                </FormControl>
                <FormLabel className='text-muted-foreground text-xs leading-5 font-normal'>
                  {t(
                    'Publish the result. Only the model name, score and notes are shown; the address and key are never published, and publishing cannot be undone.'
                  )}
                </FormLabel>
              </div>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type='submit' disabled={pending}>
          {pending ? <Loader2 className='animate-spin' /> : <Beaker />}
          {pending ? t('Testing, up to 2 minutes') : t('Run crowdtest')}
        </Button>
      </form>
    </Form>
  )
}

/** Renders a pelican SVG inside a sandboxed frame so published markup cannot run. */
function PelicanArtifact({ markup }: { markup: string }) {
  const { t } = useTranslation()
  return (
    <iframe
      // The backend already rejects script, event handlers and external
      // references, but published markup is third-party content, so render it
      // sandboxed with no allow-* flags as a second line of defence.
      sandbox=''
      title={t('Pelican drawing')}
      className='bg-background h-48 w-full rounded-md border'
      srcDoc={`<!doctype html><html><body style="margin:0;display:grid;place-items:center;height:100%">${markup}</body></html>`}
    />
  )
}

export function CrowdtestCard({
  test,
  onPublish,
  publishing,
}: {
  test: Crowdtest
  onPublish?: (publicID: string) => void
  publishing?: boolean
}) {
  const { t } = useTranslation()
  const tone = checkStatus(test.status)
  return (
    <li className='space-y-2 p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-sm font-medium'>{test.model}</span>
          <Badge variant='outline'>
            {test.benchmark === 'candy' ? t('Candy test') : t('Pelican test')}
          </Badge>
          <Badge variant={tone.variant}>{t(tone.label)}</Badge>
          {test.status === 'scored' ? (
            <span className='text-muted-foreground text-xs tabular-nums'>
              {test.score}/100
            </span>
          ) : null}
        </div>
        <span className='text-muted-foreground text-xs'>
          {formatEpoch(test.created_at)}
        </span>
      </div>
      {test.submitter ? (
        <p className='text-muted-foreground text-xs'>
          {t('Submitted by {{name}}', { name: test.submitter })}
        </p>
      ) : null}
      <p className='text-muted-foreground text-xs leading-5'>{test.evidence}</p>
      {test.artifact ? <PelicanArtifact markup={test.artifact} /> : null}
      {onPublish && !test.published ? (
        <Button
          size='sm'
          variant='outline'
          disabled={publishing}
          onClick={() => onPublish(test.public_id)}
        >
          {publishing ? <Loader2 className='animate-spin' /> : <Share2 />}
          {t('Publish result')}
        </Button>
      ) : null}
      {test.published ? (
        <Badge variant='secondary'>{t('Published')}</Badge>
      ) : null}
    </li>
  )
}
