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
import { Loader2, Play } from 'lucide-react'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

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

import type { ProbeTargetInput, RelayOption } from '../api'
import {
  PROBE_TARGET_DEFAULTS,
  type ProbeTargetValues,
  probeTargetSchema,
  toProbeTarget,
} from '../lib/probe-target'
import { ProbeTargetFields } from './probe-target-fields'

export type AuditFormValues = ProbeTargetValues & {
  consent: boolean
}

function auditSchema(t: (key: string) => string) {
  return z.intersection(
    probeTargetSchema(t),
    z.object({
      consent: z.boolean().refine((value) => value, {
        message: t('Confirm the audit cost before running'),
      }),
    })
  )
}

export function AuditForm({
  relays,
  pending,
  onSubmit,
}: {
  relays: RelayOption[]
  pending: boolean
  onSubmit: (input: ProbeTargetInput) => void
}) {
  const { t } = useTranslation()
  const schema = useMemo(() => auditSchema(t), [t])
  const form = useForm<AuditFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { ...PROBE_TARGET_DEFAULTS, consent: false },
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
        <FormField
          control={form.control}
          name='consent'
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
                    'I understand the audit sends about 10 small requests and will use a little upstream quota.'
                  )}
                </FormLabel>
              </div>
              <FormDescription className='sr-only'>
                {t('Required before running an audit')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type='submit' disabled={pending}>
          {pending ? <Loader2 className='animate-spin' /> : <Play />}
          {pending ? t('Auditing, up to 2.5 minutes') : t('Run audit')}
        </Button>
      </form>
    </Form>
  )
}
