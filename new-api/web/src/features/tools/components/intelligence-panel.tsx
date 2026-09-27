import { useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { BrainCircuit, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { createIntelligenceTest, getIntelligenceTest, getManxueCommunity, getManxueMonitors, type IntelligenceInput, type IntelligenceTest } from '../api'

export function IntelligencePanel() {
  const { t } = useTranslation()
  const [benchmark, setBenchmark] = useState<'candy' | 'pelican'>('candy')
  const [htmlMode, setHtmlMode] = useState(false)
  const [baseURL, setBaseURL] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState('')
  const [html, setHTML] = useState('')
  const [result, setResult] = useState<IntelligenceTest | null>(null)
  const [polling, setPolling] = useState(false)
  const monitors = useQuery({ queryKey: ['manxue-monitors'], queryFn: getManxueMonitors, staleTime: 30_000 })
  const community = useQuery({ queryKey: ['manxue-community', benchmark], queryFn: () => getManxueCommunity(benchmark), staleTime: 30_000 })
  const submit = useMutation({ mutationFn: createIntelligenceTest, onError: (error) => handleServerError(error), onSuccess: (value) => { setResult(value); toast.success(t('Test submitted')); setPolling(value.status !== 'succeeded' && value.status !== 'failed' && value.status !== 'cancelled') } })

  useEffect(() => {
    if (!polling || !result?.id) return
    const timer = window.setInterval(() => { void getIntelligenceTest(result.id).then((value) => { setResult(value); if (['succeeded', 'failed', 'cancelled'].includes(value.status)) setPolling(false) }).catch((error) => { setPolling(false); handleServerError(error) }) }, 3000)
    return () => window.clearInterval(timer)
  }, [polling, result?.id])

  const submitForm = () => {
    const input: IntelligenceInput = htmlMode ? { benchmark: 'pelican', html } : { benchmark, base_url: baseURL, api_key: apiKey, model: model || undefined }
    if (htmlMode ? !html.trim() : !baseURL.trim() || !apiKey.trim()) { toast.error(t('Please complete the required fields')); return }
    submit.mutate(input)
  }
  const quality = result?.assessment?.quality
  return <Card>
    <CardHeader><CardTitle className='flex items-center gap-2 text-base'><BrainCircuit className='text-primary size-5' />{t('Intelligence test')}<Badge variant='outline'>{t('Manxue public API')}</Badge></CardTitle></CardHeader>
    <CardContent className='space-y-4'>
      <p className='text-muted-foreground text-sm'>{t('Run a Candy or Pelican quality check. Your upstream key is sent only for this request and is not stored.')}</p>
      <div className='flex flex-wrap gap-2'><Button type='button' variant={benchmark === 'candy' ? 'default' : 'outline'} onClick={() => { setBenchmark('candy'); setHtmlMode(false) }}>{t('Candy')}</Button><Button type='button' variant={benchmark === 'pelican' && !htmlMode ? 'default' : 'outline'} onClick={() => { setBenchmark('pelican'); setHtmlMode(false) }}>{t('Pelican API')}</Button><Button type='button' variant={htmlMode ? 'default' : 'outline'} onClick={() => { setBenchmark('pelican'); setHtmlMode(true) }}>{t('Pelican HTML')}</Button></div>
      {htmlMode ? <Textarea value={html} onChange={(event) => setHTML(event.target.value)} placeholder={t('Paste Pelican HTML (up to 2 MiB)')} rows={6} /> : <div className='grid gap-3 md:grid-cols-3'><Input value={baseURL} onChange={(event) => setBaseURL(event.target.value)} placeholder={t('Upstream base URL')} type='url' /><Input value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder={t('Upstream API key')} type='password' autoComplete='off' /><Input value={model} onChange={(event) => setModel(event.target.value)} placeholder={t('Model (optional)')} /></div>}
      <Button onClick={submitForm} disabled={submit.isPending || polling}>{submit.isPending || polling ? <RefreshCw className='animate-spin' /> : <BrainCircuit />}{polling ? t('Testing...') : t('Start test')}</Button>
      {result ? <div className='border-border bg-muted/30 space-y-2 rounded-lg border p-3 text-sm'><div className='flex flex-wrap items-center gap-2'><Badge>{result.status}</Badge>{result.phase ? <span>{result.phase}</span> : null}{quality ? <Badge variant={quality === 'degraded' ? 'destructive' : 'secondary'}>{quality}</Badge> : null}</div>{result.assessment?.reason ? <p>{result.assessment.reason}</p> : null}{result.candy?.answer ? <pre className='max-h-48 overflow-auto whitespace-pre-wrap'>{result.candy.answer}</pre> : null}{result.error ? <p className='text-destructive'>{result.error}</p> : null}</div> : null}
      <div className='grid gap-3 md:grid-cols-3'>
        <div className='border-border rounded-lg border p-3 text-sm'><div className='font-medium'>{t('Candy monitor')}</div><div className='text-muted-foreground mt-1'>{String((monitors.data as { candy?: { running?: boolean; configured?: boolean } } | undefined)?.candy?.running ? t('Running') : t('Idle'))} · {String((monitors.data as { candy?: { configured?: boolean } } | undefined)?.candy?.configured ? t('Configured') : t('Not configured'))}</div></div>
        <div className='border-border rounded-lg border p-3 text-sm'><div className='font-medium'>{t('Pelican monitor')}</div><div className='text-muted-foreground mt-1'>{String((monitors.data as { pelican_monitor?: { running?: boolean; configured?: boolean } } | undefined)?.pelican_monitor?.running ? t('Running') : t('Idle'))} · {String((monitors.data as { pelican_monitor?: { configured?: boolean } } | undefined)?.pelican_monitor?.configured ? t('Configured') : t('Not configured'))}</div></div>
        <div className='border-border rounded-lg border p-3 text-sm'><div className='font-medium'>{t('Public visitor results')}</div><div className='text-muted-foreground mt-1'>{String(((community.data as { items?: unknown[] } | undefined)?.items ?? []).length)} {t('recent records')}</div></div>
      </div>
    </CardContent>
  </Card>
}
