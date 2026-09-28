import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { BrainCircuit, RefreshCw, Square } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import {
  cancelIntelligenceTest,
  createIntelligenceTest,
  getIntelligenceTest,
  getManxueCommunity,
  getManxueMonitors,
  isIntelligenceTerminal,
  toolsQueryKeys,
  type IntelligenceTest,
} from '../api'
import {
  INTELLIGENCE_HTML_MAX_BYTES,
  toIntelligenceInput,
  type IntelligenceFormValues,
} from '../lib/intelligence'

const POLL_INTERVAL_MS = 3000
// 上游可能一直不落终态；超过该次数就停止轮询，交给用户手动刷新，避免无限后台请求。
const POLL_MAX_TICKS = 100

type FormValues = IntelligenceFormValues

function intelligenceSchema(t: (key: string) => string) {
  return z
    .object({
      mode: z.enum(['candy', 'pelican-api', 'pelican-html']),
      base_url: z.string().trim().max(512, t('URL is too long')),
      api_key: z.string().trim().max(512, t('Credential is too long')),
      model: z.string().trim().max(191, t('Model ID is too long')),
      html: z.string(),
      consent: z.boolean(),
    })
    .superRefine((values, ctx) => {
      if (values.mode === 'pelican-html') {
        if (!values.html.trim()) {
          ctx.addIssue({
            code: 'custom',
            path: ['html'],
            message: t('Paste the Pelican HTML to check'),
          })
        } else if (new Blob([values.html]).size > INTELLIGENCE_HTML_MAX_BYTES) {
          ctx.addIssue({
            code: 'custom',
            path: ['html'],
            message: t('HTML exceeds 2 MiB'),
          })
        }
        return
      }
      if (!/^https?:\/\/\S+$/.test(values.base_url)) {
        ctx.addIssue({
          code: 'custom',
          path: ['base_url'],
          message: t('Enter an http(s) URL'),
        })
      }
      if (!values.api_key) {
        ctx.addIssue({
          code: 'custom',
          path: ['api_key'],
          message: t('Enter a credential'),
        })
      }
      if (!values.consent) {
        ctx.addIssue({
          code: 'custom',
          path: ['consent'],
          message: t('Confirm that the key is sent to manxue.ai'),
        })
      }
    })
}

function MonitorTile({ title, detail }: { title: string; detail: string }) {
  return (
    <div className='border-border rounded-lg border p-3 text-sm'>
      <div className='font-medium'>{title}</div>
      <div className='text-muted-foreground mt-1'>{detail}</div>
    </div>
  )
}

type MonitorState = { running?: boolean; configured?: boolean }
type MonitorsResponse = {
  candy?: MonitorState
  pelican_monitor?: MonitorState
}

export function IntelligencePanel({ signedIn }: { signedIn: boolean }) {
  const { t } = useTranslation()
  const schema = useMemo(() => intelligenceSchema(t), [t])
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      mode: 'candy',
      base_url: '',
      api_key: '',
      model: '',
      html: '',
      consent: false,
    },
  })
  const mode = form.watch('mode')
  const [result, setResult] = useState<IntelligenceTest | null>(null)
  const [polling, setPolling] = useState(false)
  const ticks = useRef(0)

  const monitors = useQuery({
    queryKey: toolsQueryKeys.monitors,
    queryFn: getManxueMonitors,
    staleTime: 30_000,
    retry: false,
  })
  const benchmark = mode === 'candy' ? 'candy' : 'pelican'
  const community = useQuery({
    queryKey: [...toolsQueryKeys.community, benchmark],
    queryFn: () => getManxueCommunity(benchmark),
    staleTime: 30_000,
    retry: false,
  })

  const startPolling = (value: IntelligenceTest) => {
    setResult(value)
    ticks.current = 0
    setPolling(!isIntelligenceTerminal(value.status))
  }

  const submit = useMutation({
    mutationFn: createIntelligenceTest,
    onSuccess: (value) => {
      toast.success(t('Test submitted'))
      startPolling(value)
      // 不保留凭据，避免停留在页面上。
      form.resetField('api_key')
      form.resetField('consent')
    },
    onError: (error) => handleServerError(error),
  })
  const cancel = useMutation({
    mutationFn: cancelIntelligenceTest,
    onSuccess: (value) => {
      setPolling(false)
      setResult(value)
      toast.success(t('Test cancelled'))
    },
    onError: (error) => handleServerError(error),
  })

  const testId = result?.id ?? ''
  useEffect(() => {
    if (!polling || !testId) return
    const timer = window.setInterval(() => {
      ticks.current += 1
      if (ticks.current > POLL_MAX_TICKS) {
        setPolling(false)
        return
      }
      void getIntelligenceTest(testId)
        .then((value) => {
          setResult(value)
          if (isIntelligenceTerminal(value.status)) setPolling(false)
        })
        .catch((error) => {
          setPolling(false)
          handleServerError(error)
        })
    }, POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [polling, testId])

  const monitorData = monitors.data as MonitorsResponse | undefined
  const describeMonitor = (state?: MonitorState) =>
    `${state?.running ? t('Running') : t('Idle')} · ${state?.configured ? t('Configured') : t('Not configured')}`
  const communityCount = (
    (community.data as { items?: unknown[] } | undefined)?.items ?? []
  ).length
  const quality = result?.assessment?.quality
  const busy = submit.isPending || polling

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex flex-wrap items-center gap-2 text-base'>
          <BrainCircuit className='text-primary size-5' />
          {t('Intelligence test')}
          <Badge variant='outline'>{t('Powered by manxue.ai')}</Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Runs the public Candy or Pelican check through manxue.ai. In API mode your upstream key is forwarded to manxue.ai for this run only and is never stored here; results come from a third party and are signals, not verdicts.'
          )}
        </p>

        {signedIn ? (
          <Form {...form}>
            <form
              noValidate
              className='bg-muted/20 grid gap-3 rounded-lg border p-3'
              onSubmit={form.handleSubmit((values) =>
                submit.mutate(toIntelligenceInput(values))
              )}
            >
              <FormField
                control={form.control}
                name='mode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Check type')}</FormLabel>
                    <FormControl>
                      <NativeSelect className='w-full' {...field}>
                        <NativeSelectOption value='candy'>
                          {t('Candy (API)')}
                        </NativeSelectOption>
                        <NativeSelectOption value='pelican-api'>
                          {t('Pelican (API)')}
                        </NativeSelectOption>
                        <NativeSelectOption value='pelican-html'>
                          {t('Pelican (paste HTML)')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {mode === 'pelican-html' ? (
                <FormField
                  control={form.control}
                  name='html'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Pelican HTML')}</FormLabel>
                      <FormControl>
                        <Textarea rows={6} {...field} />
                      </FormControl>
                      <FormDescription>
                        {t('Up to 2 MiB. No key is sent in this mode.')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ) : (
                <>
                  <div className='grid gap-3 md:grid-cols-3'>
                    <FormField
                      control={form.control}
                      name='base_url'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Upstream base URL')}</FormLabel>
                          <FormControl>
                            <Input
                              autoComplete='off'
                              inputMode='url'
                              placeholder='https://api.example.com/v1'
                              {...field}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name='api_key'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Upstream API key')}</FormLabel>
                          <FormControl>
                            <Input
                              autoComplete='off'
                              type='password'
                              {...field}
                            />
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
                          <FormLabel>{t('Model (optional)')}</FormLabel>
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
                  </div>
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
                              'I agree to send this base URL and key to the third-party service manxue.ai for one check, and I am allowed to share them.'
                            )}
                          </FormLabel>
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </>
              )}

              <div className='flex flex-wrap gap-2'>
                <Button type='submit' disabled={busy}>
                  {busy ? (
                    <RefreshCw className='animate-spin' />
                  ) : (
                    <BrainCircuit />
                  )}
                  {polling ? t('Testing...') : t('Start test')}
                </Button>
                {polling && testId ? (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={cancel.isPending}
                    onClick={() => cancel.mutate(testId)}
                  >
                    <Square />
                    {t('Cancel test')}
                  </Button>
                ) : null}
              </div>
            </form>
          </Form>
        ) : (
          <div className='bg-muted/20 flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 text-sm'>
            <span>{t('Sign in to run an intelligence test.')}</span>
            <Button size='sm' render={<Link to='/sign-in' />}>
              {t('Sign in')}
            </Button>
          </div>
        )}

        {result ? (
          <div className='border-border bg-muted/30 space-y-2 rounded-lg border p-3 text-sm'>
            <div className='flex flex-wrap items-center gap-2'>
              <Badge>{result.status}</Badge>
              {result.phase ? (
                <span className='text-muted-foreground'>{result.phase}</span>
              ) : null}
              {quality ? (
                <Badge
                  variant={quality === 'degraded' ? 'destructive' : 'secondary'}
                >
                  {quality}
                </Badge>
              ) : null}
              {!polling && !isIntelligenceTerminal(result.status) ? (
                <span className='text-muted-foreground text-xs'>
                  {t('Polling stopped; check again later.')}
                </span>
              ) : null}
            </div>
            {result.assessment?.reason ? (
              <p>{result.assessment.reason}</p>
            ) : null}
            {result.candy?.answer ? (
              <pre className='max-h-48 overflow-auto whitespace-pre-wrap'>
                {result.candy.answer}
              </pre>
            ) : null}
            {result.error ? (
              <p className='text-destructive'>{result.error}</p>
            ) : null}
          </div>
        ) : null}

        <div className='grid gap-3 md:grid-cols-3'>
          <MonitorTile
            title={t('Candy monitor')}
            detail={
              monitors.isError
                ? t('Unavailable')
                : describeMonitor(monitorData?.candy)
            }
          />
          <MonitorTile
            title={t('Pelican monitor')}
            detail={
              monitors.isError
                ? t('Unavailable')
                : describeMonitor(monitorData?.pelican_monitor)
            }
          />
          <MonitorTile
            title={t('Public visitor results')}
            detail={
              community.isError
                ? t('Unavailable')
                : t('{{count}} recent records', { count: communityCount })
            }
          />
        </div>
      </CardContent>
    </Card>
  )
}
