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
import { Link } from '@tanstack/react-router'
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
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import type { RelayOption } from '../api'

export type AuditFormValues = {
  relay_share_public_id: string
  model: string
  consent: boolean
}

function auditSchema(t: (key: string) => string) {
  return z.object({
    relay_share_public_id: z.string().min(1, t('Select a relay')),
    model: z
      .string()
      .trim()
      .min(1, t('Enter a model ID'))
      .max(191, t('Model ID is too long')),
    consent: z.boolean().refine((value) => value, {
      message: t('Confirm the audit cost before running'),
    }),
  })
}

export function AuditForm({
  relays,
  pending,
  onSubmit,
}: {
  relays: RelayOption[]
  pending: boolean
  onSubmit: (input: { relay_share_public_id: string; model: string }) => void
}) {
  const { t } = useTranslation()
  const schema = useMemo(() => auditSchema(t), [t])
  const form = useForm<AuditFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { relay_share_public_id: '', model: '', consent: false },
  })
  // Audits decrypt the stored detection key server-side; relays without one would 400.
  const usable = relays.filter((item) => item.credential_configured)

  if (usable.length === 0) {
    return (
      <div className='bg-muted/20 space-y-3 rounded-lg border p-3 text-sm'>
        <p className='text-muted-foreground'>
          {t(
            'Share a relay with a detection key first; audits only run against your own relays.'
          )}
        </p>
        <Button size='sm' variant='outline' render={<Link to='/relay-share' />}>
          {t('Go to relay sharing')}
        </Button>
      </div>
    )
  }

  return (
    <Form {...form}>
      <form
        noValidate
        className='bg-muted/20 grid gap-3 rounded-lg border p-3'
        onSubmit={form.handleSubmit((values) =>
          onSubmit({
            relay_share_public_id: values.relay_share_public_id,
            model: values.model.trim(),
          })
        )}
      >
        <FormField
          control={form.control}
          name='relay_share_public_id'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Relay')}</FormLabel>
              <FormControl>
                <NativeSelect className='w-full' {...field}>
                  <NativeSelectOption value='' disabled>
                    {t('Select a relay with a detection key')}
                  </NativeSelectOption>
                  {usable.map((item) => (
                    <NativeSelectOption
                      key={item.public_id}
                      value={item.public_id}
                    >
                      {item.title} ·{' '}
                      {item.kind === 'charity'
                        ? t('Charity relay')
                        : t('Commercial relay')}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='model'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Model ID')}</FormLabel>
              <FormControl>
                <Input
                  autoComplete='off'
                  placeholder={t('e.g. deepseek-chat')}
                  {...field}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
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
                    'I understand the audit sends about 10 small requests with my detection key and may consume a little upstream quota.'
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
          {pending ? t('Auditing, up to 90 seconds') : t('Run audit')}
        </Button>
      </form>
    </Form>
  )
}
