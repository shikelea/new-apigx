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
import { Check, Loader2, TimerReset } from 'lucide-react'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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

import type { CheckinSite, CheckinSiteInput } from '../api'
import {
  browserTimeZone,
  formatEpoch,
  formatMinuteOfDay,
  parseTimeOfDay,
} from '../lib/format'

function checkinSchema(t: (key: string) => string) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, t('Enter a site name'))
      .max(100, t('Site name is too long')),
    base_url: z
      .string()
      .trim()
      .max(512, t('URL is too long'))
      .refine((value) => /^https?:\/\/\S+$/i.test(value), {
        message: t('Enter an http(s) URL'),
      }),
    credential_kind: z.enum(['bearer', 'session_cookie']),
    key: z.string().trim().min(1, t('Enter a credential')),
    time: z.string().refine((value) => parseTimeOfDay(value) !== null, {
      message: t('Pick a check-in time'),
    }),
  })
}

type CheckinFormValues = z.infer<ReturnType<typeof checkinSchema>>

export function CheckinForm({
  pending,
  onSubmit,
}: {
  pending: boolean
  onSubmit: (input: CheckinSiteInput) => void
}) {
  const { t } = useTranslation()
  const schema = useMemo(() => checkinSchema(t), [t])
  const timeZone = useMemo(() => browserTimeZone(), [])
  const form = useForm<CheckinFormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: '',
      base_url: '',
      credential_kind: 'bearer',
      key: '',
      time: '09:00',
    },
  })
  const kind = form.watch('credential_kind')

  return (
    <Form {...form}>
      <form
        noValidate
        className='bg-muted/20 grid gap-3 rounded-lg border p-3'
        onSubmit={form.handleSubmit((values) =>
          onSubmit({
            name: values.name.trim(),
            base_url: values.base_url.trim(),
            key: values.key.trim(),
            credential_kind: values.credential_kind,
            timezone: timeZone,
            // Guarded by the schema refine, so the fallback never triggers.
            schedule_minute: parseTimeOfDay(values.time) ?? 540,
          })
        )}
      >
        <FormField
          control={form.control}
          name='name'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Site name')}</FormLabel>
              <FormControl>
                <Input autoComplete='off' {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='base_url'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Site URL')}</FormLabel>
              <FormControl>
                <Input
                  type='url'
                  placeholder='https://example.com'
                  autoComplete='off'
                  {...field}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='credential_kind'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Credential type')}</FormLabel>
              <FormControl>
                <NativeSelect className='w-full' {...field}>
                  <NativeSelectOption value='bearer'>
                    {t('Access token')}
                  </NativeSelectOption>
                  <NativeSelectOption value='session_cookie'>
                    {t('Refresh cookie')}
                  </NativeSelectOption>
                </NativeSelect>
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='key'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Credential')}</FormLabel>
              <FormControl>
                <Input type='password' autoComplete='off' {...field} />
              </FormControl>
              <FormDescription>
                {kind === 'bearer'
                  ? t('The system access token from your profile page.')
                  : t(
                      'The value of the new_api_refresh cookie. Rotated cookies are saved automatically.'
                    )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='time'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Daily check-in time')}</FormLabel>
              <FormControl>
                <Input type='time' {...field} />
              </FormControl>
              <FormDescription>
                {t('Time zone: {{zone}}', { zone: timeZone })}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type='submit' disabled={pending}>
          {pending ? <Loader2 className='animate-spin' /> : <Check />}
          {t('Save site')}
        </Button>
      </form>
    </Form>
  )
}

function checkinStatusLabel(status: string) {
  if (status === 'success') return 'Succeeded'
  if (status === 'failed') return 'Failed'
  return 'Not run yet'
}

export function CheckinList({
  sites,
  loading,
  runningId,
  onRun,
}: {
  sites: CheckinSite[]
  loading: boolean
  runningId: number | null
  onRun: (id: number) => void
}) {
  const { t } = useTranslation()
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>{t('My check-in sites')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-3' aria-busy={loading}>
        {sites.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {loading ? t('Loading...') : t('No check-in sites yet.')}
          </p>
        ) : (
          sites.map((site) => {
            const running = runningId === site.id
            return (
              <article key={site.id} className='rounded-lg border p-3 text-sm'>
                <div className='flex items-center justify-between gap-3'>
                  <div className='min-w-0'>
                    <p className='truncate font-medium'>{site.name}</p>
                    <p className='text-muted-foreground truncate text-xs'>
                      {site.base_url}
                    </p>
                  </div>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={runningId !== null || !site.enabled}
                    onClick={() => onRun(site.id)}
                  >
                    {running ? (
                      <Loader2 className='animate-spin' />
                    ) : (
                      <TimerReset />
                    )}
                    {t('Check in now')}
                  </Button>
                </div>
                <div className='text-muted-foreground mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
                  <span>
                    {t('Daily at {{time}} ({{zone}})', {
                      time: formatMinuteOfDay(site.schedule_minute),
                      zone: site.timezone,
                    })}
                  </span>
                  <Badge
                    variant={
                      site.last_status === 'failed' ? 'destructive' : 'outline'
                    }
                  >
                    {t(checkinStatusLabel(site.last_status))}
                  </Badge>
                  <span>
                    {t('Last run')}: {formatEpoch(site.last_run_at)}
                  </span>
                  <span>
                    {t('Next run')}: {formatEpoch(site.next_run_at)}
                  </span>
                </div>
                {site.last_message ? (
                  <p className='text-muted-foreground mt-1 text-xs break-words'>
                    {site.last_message}
                  </p>
                ) : null}
              </article>
            )
          })
        )}
      </CardContent>
    </Card>
  )
}
