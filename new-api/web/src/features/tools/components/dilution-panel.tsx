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
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  AlertTriangle,
  Brain,
  CheckCircle2,
  Clock,
  Code2,
  FileCheck2,
  Fingerprint,
  FlaskConical,
  HelpCircle,
  Layers,
  Loader2,
  Sparkles,
  XCircle,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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

import {
  listModelRegistry,
  listTargetModels,
  toolsQueryKeys,
  type DilutionReport,
  type DilutionVerdict,
  type ProbeTargetInput,
  type RelayOption,
} from '../api'
import {
  PROBE_SOURCE_RELAY,
  PROBE_TARGET_DEFAULTS,
  probeTargetSchema,
  requestNameForTarget,
  toProbeTarget,
  type ProbeTargetValues,
} from '../lib/probe-target'
import { ProbeTargetFields } from './probe-target-fields'

const VENDOR_CN_MAP: Record<string, string> = {
  DeepSeek: 'DeepSeek (深度求索)',
  OpenAI: 'OpenAI',
  Anthropic: 'Anthropic',
  Moonshot: '月之暗面 (Kimi)',
  '智谱 / Z.ai': '智谱 AI (GLM)',
  阿里云百炼: '阿里云百炼 (通义千问)',
  '小米 MiMo': '小米 MiMo',
}

const MODEL_CN_MAP: Record<string, string> = {
  'deepseek-flash': 'DeepSeek Flash',
  'deepseek-v4.1-flash': 'DeepSeek V4.1 Flash',
  'deepseek-v3': 'DeepSeek V3',
  'deepseek-chat': 'DeepSeek V3 (Chat)',
  'gpt-6-sol': 'GPT-6 Sol',
  'gpt-6-luna': 'GPT-6 Luna',
  'gpt-6-astra': 'GPT-6 Astra',
  'gpt-5.6-terra': 'GPT-5.6 Terra',
  'claude-sonnet-5': 'Claude Sonnet 5',
  'claude-haiku-4.5': 'Claude Haiku 4.5',
  'claude-opus-5.5': 'Claude Opus 5.5',
  'claude-fable-5.1': 'Claude Fable 5.1',
  'kimi-k3': 'Kimi K3',
  'glm-5.3': 'GLM-5.3',
  'qwen3.8-max': '通义千问 Qwen 3.8 Max',
}

// 一张卡片就是一个基准型号：判定按 baseline 走，发出去的请求名是 requestName（该目标
// 实际提供的规范名或别名之一）。candidates 用于请求名输入框的备选提示。
type DilutionModelOption = {
  baseline: string
  name: string
  request: string
  requestName: string
  candidates: string[]
  ready_tiers: string[]
}

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
    defaultValues: {
      ...PROBE_TARGET_DEFAULTS,
      baseline: 'deepseek-flash', // 默认选中已收录基准的代表型号
      model: 'deepseek-flash',
      tier: 'high',
      concurrency: 2,
    },
  })
  const source = form.watch('source')
  const selectedTier = form.watch('tier') || 'high'
  const selectedBaseline = form.watch('baseline')
  const relayID = form.watch('relay_share_public_id')
  const baseURL = form.watch('base_url')
  const manualKey = form.watch('key')
  // 请求名可以手改：改过之后就不再被「基准型号的默认名」覆盖，否则用户填的中转名会被
  // 每次重渲染冲掉。
  const [nameDirty, setNameDirty] = useState(false)

  // 收录名单一律问后端：前端自备副本会和基准脱节，之前正是这么长出十几张「点了必然全部失败」
  // 的卡片的。后端只列该档确有校准基准的型号。
  const registryQuery = useQuery({
    queryKey: toolsQueryKeys.modelRegistry,
    queryFn: listModelRegistry,
  })

  // 目标接口实际提供的型号名。地址填全了才去问，且要等输入停下 600ms 再问：边打字边查会
  // 让每敲一个字符都真的打到上游一次。缓存键里不放 Key，它不该出现在 QueryClient 的键里。
  const targetSignature =
    source === PROBE_SOURCE_RELAY ? `relay:${relayID}` : `url:${baseURL.trim()}`
  const [settledSignature, setSettledSignature] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setSettledSignature(targetSignature), 600)
    return () => clearTimeout(timer)
  }, [targetSignature])
  const targetComplete =
    source === PROBE_SOURCE_RELAY
      ? !!relayID
      : !!baseURL.trim() && !!manualKey.trim()
  const targetReady = targetComplete && settledSignature === targetSignature
  const servedQuery = useQuery({
    queryKey: ['apigx-target-models', targetSignature],
    queryFn: () =>
      listTargetModels(
        source === PROBE_SOURCE_RELAY
          ? { relay_share_public_id: relayID }
          : { base_url: baseURL.trim(), key: manualKey.trim() }
      ),
    enabled: targetReady,
    retry: 0,
    staleTime: 5 * 60 * 1000,
  })

  // 可选型号 = 有校准基准 ∩ 该中转真的提供（规范名或别名），再按所选档位过滤。
  const vendorGroups = useMemo(() => {
    const served = servedQuery.data
    const servedList = served && served.length > 0 ? served : null
    const map = new Map<string, DilutionModelOption[]>()
    for (const item of registryQuery.data ?? []) {
      if (item.baseline_state !== 'ready') {
        continue
      }
      const readyTiers =
        item.ready_tiers && item.ready_tiers.length > 0 ? item.ready_tiers : []
      if (!readyTiers.includes(selectedTier)) {
        continue
      }
      const names =
        item.request_names && item.request_names.length > 0
          ? item.request_names
          : [item.id]
      const candidates = [...names.slice(1), ...(item.aliases ?? [])]
      const requestName = requestNameForTarget(
        { request: names[0], aliases: candidates },
        servedList
      )
      // 该目标一个名字都不提供（规范名和别名都没有）时，这张卡片点了只会几百次全部失败。
      if (requestName === null) {
        continue
      }
      const vendor = VENDOR_CN_MAP[item.vendor] || item.vendor || '其他'
      const bucket = map.get(vendor) ?? []
      bucket.push({
        baseline: item.id,
        name:
          MODEL_CN_MAP[names[0]] ||
          MODEL_CN_MAP[item.id] ||
          item.display_name ||
          item.id,
        request: names[0],
        requestName,
        candidates: [names[0], ...candidates],
        ready_tiers: readyTiers,
      })
      map.set(vendor, bucket)
    }
    // 读不到目标清单（中转不支持 /models）时不清空列表：开跑前那一次预检会给明确原因。
    return [...map.entries()].map(([vendor, models]) => ({ vendor, models }))
  }, [registryQuery.data, servedQuery.data, selectedTier])

  const selectableModels = useMemo(
    () => vendorGroups.flatMap((group) => group.models),
    [vendorGroups]
  )

  const pickedModel = selectableModels.find(
    (item) => item.baseline === selectedBaseline
  )

  // 型号卡片选基准，请求名跟着基准的默认名走；用户手改过就不再覆盖。当前所选不在有效
  // 列表内（换了档位、换了目标）时自适应切到该档位首选。
  useEffect(() => {
    if (selectableModels.length === 0) {
      return
    }
    const picked =
      selectableModels.find(
        (item) => item.baseline === form.getValues('baseline')
      ) ?? selectableModels[0]
    if (picked.baseline !== form.getValues('baseline')) {
      form.setValue('baseline', picked.baseline)
      form.setValue('model', picked.requestName)
      return
    }
    if (!nameDirty) {
      form.setValue('model', picked.requestName)
    }
  }, [form, selectableModels, selectedBaseline, nameDirty])

  // 一句人话告诉用户「为什么这个能选／为什么现在一个都选不了」。读不到目标清单时不能假装
  // 筛过了——列表此时只按基准过滤，真正兜底的是开跑前那一次预检。
  let modelHint: string
  if (targetReady && servedQuery.isFetching) {
    modelHint = t('Reading the model list of this target…')
  } else if (servedQuery.isError) {
    modelHint = t(
      'Could not read the model list of this target (no /v1/models, or too many requests). The list was not filtered by what it actually serves; one preflight request will still confirm the name you picked.'
    )
  } else if (selectableModels.length === 0) {
    modelHint = targetReady
      ? t(
          'This target serves no model we have a calibrated baseline for (aliases included). Try another model name or another target.'
        )
      : t(
          'Fill in the target first: only models it really serves and we have a baseline for will be listed.'
        )
  } else {
    modelHint = t(
      'Baseline models comparable at tier {{tier}}: {{count}}. Names outside this list cannot produce a verdict, because there is no baseline for the same protocol and tier.',
      { tier: selectedTier, count: selectableModels.length }
    )
  }

  // 请求名默认跟着所选基准型号走；中转把这个型号叫成别的名字时，用户可以直接改成它实际
  // 提供的名字（判定仍按基准型号）。备选来自基准的规范名与别名。
  const requestNameHint = t(
    'The name sent upstream. Change it when this relay serves the model under a different name; detection still follows the baseline model.'
  )

  return (
    <Form {...form}>
      <form
        noValidate
        className='bg-muted/20 grid gap-3.5 rounded-lg border p-3.5'
        onSubmit={form.handleSubmit((values) =>
          onSubmit(toProbeTarget(values))
        )}
      >
        <ProbeTargetFields
          control={form.control}
          source={source}
          relays={relays}
          hideModel
        />

        {/* 挡位与并发数配置 */}
        <div className='grid grid-cols-1 gap-3.5 sm:grid-cols-2'>
          {/* 挡位选择器 */}
          <FormField
            control={form.control}
            name='tier'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Detection Tier') || '检测挡位'}</FormLabel>
                <FormControl>
                  <NativeSelect className='w-full' {...field}>
                    <NativeSelectOption value='high'>
                      {t('High / Accurate (Recommended)') ||
                        '高档 / 精准 (推荐)'}
                    </NativeSelectOption>
                    <NativeSelectOption value='medium'>
                      {t('Medium / Balanced') || '中档 / 平衡'}
                    </NativeSelectOption>
                    <NativeSelectOption value='low'>
                      {t('Low / Fast') || '低档 / 快速'}
                    </NativeSelectOption>
                  </NativeSelect>
                </FormControl>
                <FormDescription className='text-xs'>
                  不同挡位对应不同采样题量，可用模型将随所选挡位自动筛选。
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {/* 并发数设置 */}
          <FormField
            control={form.control}
            name='concurrency'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Concurrency') || '并发数'}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    max={8}
                    className='w-full'
                    {...field}
                    value={field.value ?? 2}
                    onChange={(e) => {
                      const val = Number.parseInt(e.target.value, 10)
                      field.onChange(
                        Number.isNaN(val) ? 2 : Math.max(1, Math.min(8, val))
                      )
                    }}
                  />
                </FormControl>
                <FormDescription className='text-xs'>
                  并发探针请求数（默认 2，建议 1～4；并发过高易触碰上游 429
                  限流）。
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>

        {/* 基准型号卡片：判定按它走；请求名单独可编辑，因为中转常把同一个型号叫成别的名字 */}
        <FormField
          control={form.control}
          name='baseline'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Baseline model')}</FormLabel>
              <FormControl>
                <NativeSelect
                  className='w-full'
                  {...field}
                  onChange={(event) => {
                    field.onChange(event)
                    setNameDirty(false)
                  }}
                >
                  {vendorGroups.map((group) => (
                    <optgroup key={group.vendor} label={group.vendor}>
                      {group.models.map((m) => (
                        <NativeSelectOption key={m.baseline} value={m.baseline}>
                          {m.name} ({m.request})
                        </NativeSelectOption>
                      ))}
                    </optgroup>
                  ))}
                </NativeSelect>
              </FormControl>
              <FormDescription className='text-xs'>{modelHint}</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name='model'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Request model name')}</FormLabel>
              <FormControl>
                <div>
                  <Input
                    autoComplete='off'
                    list='dilution-request-names'
                    placeholder={t('e.g. gpt-4o')}
                    {...field}
                    onChange={(event) => {
                      field.onChange(event)
                      setNameDirty(true)
                    }}
                  />
                  <datalist id='dilution-request-names'>
                    {(pickedModel?.candidates ?? []).map((name) => (
                      <option key={name} value={name} />
                    ))}
                  </datalist>
                </div>
              </FormControl>
              <FormDescription className='text-xs'>
                {requestNameHint}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <Button
          type='submit'
          disabled={pending || selectableModels.length === 0}
          className='w-full sm:w-auto'
        >
          {pending ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <FlaskConical className='size-4' />
          )}
          {pending
            ? t('Checking, up to 10 minutes') ||
              '正在进行全项检测，至多需要数分钟...'
            : t('Run substitution check') || '开始掺水检测'}
        </Button>
      </form>
    </Form>
  )
}

/** 8 个维度对应的图形与中文简称 */
function getFindingMeta(id: string, name: string) {
  switch (id) {
    case 'protocol':
      return { icon: FileCheck2, title: '协议规范' }
    case 'knowledge':
      return { icon: Clock, title: '知识时效' }
    case 'fingerprint':
      return { icon: Fingerprint, title: '型号指纹' }
    case 'logic':
      return { icon: Brain, title: '逻辑求解' }
    case 'structure':
      return { icon: Code2, title: '结构约束' }
    case 'hierarchy':
      return { icon: Layers, title: '指令层级' }
    case 'streaming':
      return { icon: Activity, title: '流式完整' }
    case 'reasoning':
      return { icon: Sparkles, title: '推理强度' }
    default:
      return { icon: Sparkles, title: name || id }
  }
}

/** 结论徽标与颜色 */
function getVerdictBadge(verdict: DilutionVerdict) {
  switch (verdict) {
    case 'match':
      return {
        label: '一致',
        variant: 'secondary' as const,
        icon: CheckCircle2,
        badgeClass:
          'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30',
      }
    case 'mismatch':
      return {
        label: '不一致',
        variant: 'destructive' as const,
        icon: XCircle,
        badgeClass:
          'bg-rose-500/15 text-rose-700 dark:text-rose-300 border-rose-500/30',
      }
    case 'suspect':
      return {
        label: '待复核',
        variant: 'outline' as const,
        icon: AlertTriangle,
        badgeClass:
          'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30',
      }
    case 'inconclusive':
    default:
      return {
        label: '证据不足',
        variant: 'outline' as const,
        icon: HelpCircle,
        badgeClass: 'text-muted-foreground border-border',
      }
  }
}

/** 百分比圆环仪表组件 */
function AuthenticityRing({
  score,
  verdict,
}: {
  score: number
  verdict: DilutionVerdict
}) {
  const clamped = Math.max(0, Math.min(100, Math.round(score)))
  const radius = 38
  const stroke = 7
  const normalizedRadius = radius - stroke * 0.5
  const circumference = normalizedRadius * 2 * Math.PI
  const strokeDashoffset = circumference - (clamped / 100) * circumference

  let strokeColor = 'text-emerald-500 stroke-emerald-500'
  let ringBg = 'stroke-emerald-500/15'
  let textColor = 'text-emerald-600 dark:text-emerald-400'

  if (verdict === 'mismatch' || clamped < 60) {
    strokeColor = 'text-rose-500 stroke-rose-500'
    ringBg = 'stroke-rose-500/15'
    textColor = 'text-rose-600 dark:text-rose-400'
  } else if (
    verdict === 'suspect' ||
    verdict === 'inconclusive' ||
    clamped < 80
  ) {
    strokeColor = 'text-amber-500 stroke-amber-500'
    ringBg = 'stroke-amber-500/15'
    textColor = 'text-amber-600 dark:text-amber-400'
  }

  return (
    <div className='relative flex size-24 shrink-0 items-center justify-center sm:size-28'>
      <svg className='size-full -rotate-90 transform' viewBox='0 0 76 76'>
        <circle
          cx='38'
          cy='38'
          r={normalizedRadius}
          className={ringBg}
          strokeWidth={stroke}
          fill='transparent'
        />
        <circle
          cx='38'
          cy='38'
          r={normalizedRadius}
          className={`${strokeColor} transition-all duration-700 ease-out`}
          strokeWidth={stroke}
          strokeDasharray={`${circumference} ${circumference}`}
          style={{ strokeDashoffset }}
          strokeLinecap='round'
          fill='transparent'
        />
      </svg>
      <div className='absolute flex flex-col items-center justify-center text-center'>
        <span
          className={`text-xl font-bold tracking-tight sm:text-2xl ${textColor} tabular-nums`}
        >
          {clamped}%
        </span>
        <span className='text-muted-foreground text-[10px] font-medium sm:text-xs'>
          真实性
        </span>
      </div>
    </div>
  )
}

/** 掺水检测结果卡片：仅展示真实性圆环与 8 维状态矩阵，不展示底下嘈杂小字 */
export function DilutionResult({ report }: { report: DilutionReport }) {
  const { t } = useTranslation()
  const overall = getVerdictBadge(report.verdict)

  // 顶栏结论标题
  let overallTitle = '真实一致 · 未见掺水'
  if (report.verdict === 'mismatch') {
    overallTitle = '疑似掺水 · 特征不符'
  } else if (report.verdict === 'suspect') {
    overallTitle = '异常偏差 · 需人工复核'
  } else if (report.verdict === 'inconclusive') {
    overallTitle = '证据不足 · 采样或响应不完整'
  }

  return (
    <div className='bg-card space-y-4 rounded-xl border p-4 shadow-sm sm:p-5'>
      {/* 真实性百分比圆环 + 综合结论 */}
      <div className='flex flex-col items-center gap-4 border-b pb-4 sm:flex-row sm:gap-6'>
        <AuthenticityRing score={report.score} verdict={report.verdict} />

        <div className='flex flex-1 flex-col items-center gap-1.5 text-center sm:items-start sm:text-left'>
          <div className='flex flex-wrap items-center justify-center gap-2 sm:justify-start'>
            <Badge
              variant='outline'
              className='px-2.5 py-0.5 font-mono text-xs font-medium'
            >
              {report.model}
            </Badge>
            <Badge
              variant={overall.variant}
              className={`gap-1 px-2.5 py-0.5 text-xs font-semibold ${overall.badgeClass}`}
            >
              <overall.icon className='size-3.5' />
              {overallTitle}
            </Badge>
          </div>

          <p className='text-muted-foreground mt-0.5 text-xs'>
            {report.family
              ? `对照 ${report.family} 官方公开特征与基线比对`
              : '对照所声称型号特征与指纹基线比对'}
          </p>

          <div className='text-muted-foreground flex items-center gap-3 text-xs'>
            <span className='text-foreground font-medium'>
              综合一致性得分:{' '}
              <span className='font-semibold tabular-nums'>{report.score}</span>{' '}
              / 100
            </span>
          </div>
        </div>
      </div>

      {/* 8 个维度矩阵：紧凑栅格，只展示真实性与状态，无底下小字 */}
      <div className='space-y-2'>
        <div className='text-muted-foreground flex items-center justify-between px-0.5 text-xs'>
          <span className='text-foreground font-medium'>
            多维特征核验 (8项)
          </span>
          <span>状态核验</span>
        </div>

        <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
          {report.findings.map((finding) => {
            const meta = getFindingMeta(finding.id, finding.name)
            const tone = getVerdictBadge(finding.verdict)
            const Icon = meta.icon

            return (
              <div
                key={finding.id}
                title={
                  finding.evidence
                    ? `${finding.name}: ${finding.evidence}`
                    : finding.name
                }
                className='bg-muted/10 hover:bg-muted/20 flex cursor-default flex-col justify-between rounded-lg border p-2.5 transition-colors'
              >
                <div className='flex items-center gap-1.5 text-xs'>
                  <Icon className='text-muted-foreground size-3.5 shrink-0' />
                  <span
                    className='text-foreground truncate font-medium'
                    title={finding.name}
                  >
                    {meta.title}
                  </span>
                </div>
                <div className='mt-2.5 flex items-center justify-end'>
                  <Badge
                    variant={tone.variant}
                    className={`gap-1 px-1.5 py-0.5 text-[11px] ${tone.badgeClass}`}
                  >
                    <tone.icon className='size-3' />
                    {t(tone.label)}
                  </Badge>
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
