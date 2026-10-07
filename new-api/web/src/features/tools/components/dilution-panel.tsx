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
import { useEffect, useMemo } from 'react'
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
  toolsQueryKeys,
  type DilutionReport,
  type DilutionVerdict,
  type ProbeTargetInput,
  type RelayOption,
} from '../api'
import {
  PROBE_TARGET_DEFAULTS,
  probeTargetSchema,
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
  '阿里云百炼': '阿里云百炼 (通义千问)',
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

// 仅收录具备实测特征基线的已知模型（无基准的模型不展示）
const FALLBACK_MODEL_GROUPS: {
  vendor: string
  models: { id: string; name: string; request: string; ready: boolean; ready_tiers: string[] }[]
}[] = [
  {
    vendor: 'DeepSeek (深度求索)',
    models: [
      { id: 'deepseek-flash', name: 'DeepSeek Flash', request: 'deepseek-flash', ready: true, ready_tiers: ['high'] },
      // deepseek-v3（请求名 deepseek-chat）没有实测基准，后端按 baseline_state 过滤时不会
      // 出现，这里的兜底名单也不能宣称它有基准。
      { id: 'deepseek-v4.1-flash', name: 'DeepSeek V4.1 Flash', request: 'deepseek-v4.1-flash', ready: true, ready_tiers: ['high'] },
    ],
  },
  {
    vendor: 'OpenAI',
    models: [
      { id: 'gpt-6-sol', name: 'GPT-6 Sol', request: 'gpt-6-sol', ready: true, ready_tiers: ['low', 'medium', 'high'] },
      { id: 'gpt-6-luna', name: 'GPT-6 Luna', request: 'gpt-6-luna', ready: true, ready_tiers: ['low', 'medium', 'high'] },
      { id: 'gpt-6-astra', name: 'GPT-6 Astra', request: 'gpt-6-astra', ready: true, ready_tiers: ['low', 'medium', 'high'] },
      { id: 'gpt-5.6-terra', name: 'GPT-5.6 Terra', request: 'gpt-5.6-terra', ready: true, ready_tiers: ['low', 'medium', 'high'] },
    ],
  },
  {
    vendor: 'Anthropic',
    models: [
      { id: 'claude-sonnet-5', name: 'Claude Sonnet 5', request: 'claude-sonnet-5', ready: true, ready_tiers: ['low', 'medium', 'high'] },
      { id: 'claude-haiku-4.5', name: 'Claude Haiku 4.5', request: 'claude-haiku-4.5', ready: true, ready_tiers: ['low', 'medium', 'high'] },
      { id: 'claude-opus-5.5', name: 'Claude Opus 5.5', request: 'claude-opus-5.5', ready: true, ready_tiers: ['low', 'medium', 'high'] },
      { id: 'claude-fable-5.1', name: 'Claude Fable 5.1', request: 'claude-fable-5.1', ready: true, ready_tiers: ['low', 'medium', 'high'] },
    ],
  },
  {
    vendor: '阿里云百炼 (通义千问)',
    models: [
      { id: 'qwen3.8-max', name: '通义千问 Qwen 3.8 Max', request: 'qwen3.8-max', ready: true, ready_tiers: ['high'] },
    ],
  },
  {
    vendor: '智谱 AI (GLM)',
    models: [
      { id: 'glm-5.3', name: '智谱 GLM-5.3', request: 'glm-5.3', ready: true, ready_tiers: ['high'] },
    ],
  },
  {
    vendor: '月之暗面 (Kimi)',
    models: [
      { id: 'kimi-k3', name: 'Kimi K3', request: 'kimi-k3', ready: true, ready_tiers: ['high'] },
    ],
  },
]

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
      model: 'deepseek-flash', // 默认选中已收录基准的代表型号
      tier: 'high',
      concurrency: 2,
    },
  })
  const source = form.watch('source')
  const selectedTier = form.watch('tier') || 'high'

  // 获取收录的特征注册表
  const registryQuery = useQuery({
    queryKey: toolsQueryKeys.modelRegistry,
    queryFn: listModelRegistry,
  })

  // 按厂商归类收录项：根据当前选择的档位动态过滤，仅保留在该档位具有有效实测基准的模型
  const vendorGroups = useMemo(() => {
    const raw = registryQuery.data?.data
    const allGroups = (() => {
      if (!raw || raw.length === 0) {
        return FALLBACK_MODEL_GROUPS
      }
      const map = new Map<string, { id: string; name: string; request: string; ready: boolean; ready_tiers: string[] }[]>()
      for (const item of raw) {
        // 只有 baseline_state 为 ready 的模型才收录展示，未通过或无基准的一律忽略
        if (item.baseline_state !== 'ready') {
          continue
        }
        const rawVendor = item.vendor || '其他'
        const vendor = VENDOR_CN_MAP[rawVendor] || rawVendor
        if (!map.has(vendor)) {
          map.set(vendor, [])
        }
        const names = item.request_names && item.request_names.length > 0 ? item.request_names : [item.id]
        const readyTiers = item.ready_tiers && item.ready_tiers.length > 0 ? item.ready_tiers : ['high']
        for (const req of names) {
          const displayName = MODEL_CN_MAP[req] || MODEL_CN_MAP[item.id] || item.display_name || item.id
          map.get(vendor)!.push({
            id: `${item.id}-${req}`,
            name: displayName,
            request: req,
            ready: true,
            ready_tiers: readyTiers,
          })
        }
      }
      const groups = Array.from(map.entries())
        .filter(([_, models]) => models.length > 0)
        .map(([vendor, models]) => ({
          vendor,
          models,
        }))
      return groups.length > 0 ? groups : FALLBACK_MODEL_GROUPS
    })()

    // 联动过滤：只保留支持当前所选档位的模型
    return allGroups
      .map((g) => ({
        vendor: g.vendor,
        models: g.models.filter((m) => m.ready_tiers.includes(selectedTier)),
      }))
      .filter((g) => g.models.length > 0)
  }, [registryQuery.data, selectedTier])

  // 保证当前所选 model 字段不为空且在当前挡位有效列表内，否则自适应切到该档位首选可用模型
  useEffect(() => {
    const current = form.getValues('model')
    const allModels = vendorGroups.flatMap((g) => g.models.map((m) => m.request))
    if (!current || (allModels.length > 0 && !allModels.includes(current))) {
      if (vendorGroups.length > 0 && vendorGroups[0].models.length > 0) {
        form.setValue('model', vendorGroups[0].models[0].request)
      }
    }
  }, [form, vendorGroups])

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
          hideModel={true}
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
                      {t('High / Accurate (Recommended)') || '高档 / 精准 (推荐)'}
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
                      const val = parseInt(e.target.value, 10)
                      field.onChange(isNaN(val) ? 2 : Math.max(1, Math.min(8, val)))
                    }}
                  />
                </FormControl>
                <FormDescription className='text-xs'>
                  并发探针请求数（默认 2，建议 1～4；并发过高易触碰上游 429 限流）。
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>

        {/* 模型选择器：仅从已收录并具备实测特征基线的模型中选择 */}
        <FormField
          control={form.control}
          name='model'
          render={({ field }) => (
            <FormItem>
              <FormLabel>选择检测目标模型</FormLabel>
              <FormControl>
                <NativeSelect className='w-full' {...field}>
                  {vendorGroups.map((group) => (
                    <optgroup key={group.vendor} label={group.vendor}>
                      {group.models.map((m) => (
                        <NativeSelectOption key={m.id} value={m.request}>
                          {m.name} ({m.request})
                        </NativeSelectOption>
                      ))}
                    </optgroup>
                  ))}
                </NativeSelect>
              </FormControl>
              <FormDescription className='text-xs'>
                仅展示具备实测特征基准的模型。系统将逐项比对目标地址的返回是否符合该型号特征。
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <Button type='submit' disabled={pending} className='w-full sm:w-auto'>
          {pending ? <Loader2 className='size-4 animate-spin' /> : <FlaskConical className='size-4' />}
          {pending
            ? (t('Checking, up to 10 minutes') || '正在进行全项检测，至多需要数分钟...')
            : (t('Run substitution check') || '开始掺水检测')}
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
        badgeClass: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30',
      }
    case 'mismatch':
      return {
        label: '不一致',
        variant: 'destructive' as const,
        icon: XCircle,
        badgeClass: 'bg-rose-500/15 text-rose-700 dark:text-rose-300 border-rose-500/30',
      }
    case 'suspect':
      return {
        label: '待复核',
        variant: 'outline' as const,
        icon: AlertTriangle,
        badgeClass: 'bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30',
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
  } else if (verdict === 'suspect' || verdict === 'inconclusive' || clamped < 80) {
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
        <span className={`text-xl sm:text-2xl font-bold tracking-tight ${textColor} tabular-nums`}>
          {clamped}%
        </span>
        <span className='text-[10px] sm:text-xs text-muted-foreground font-medium'>
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
    <div className='space-y-4 rounded-xl border bg-card p-4 sm:p-5 shadow-sm'>
      {/* 真实性百分比圆环 + 综合结论 */}
      <div className='flex flex-col sm:flex-row items-center gap-4 sm:gap-6 border-b pb-4'>
        <AuthenticityRing score={report.score} verdict={report.verdict} />

        <div className='flex flex-1 flex-col items-center sm:items-start text-center sm:text-left gap-1.5'>
          <div className='flex flex-wrap items-center justify-center sm:justify-start gap-2'>
            <Badge variant='outline' className='font-mono font-medium text-xs px-2.5 py-0.5'>
              {report.model}
            </Badge>
            <Badge variant={overall.variant} className={`gap-1 px-2.5 py-0.5 text-xs font-semibold ${overall.badgeClass}`}>
              <overall.icon className='size-3.5' />
              {overallTitle}
            </Badge>
          </div>

          <p className='text-xs text-muted-foreground mt-0.5'>
            {report.family
              ? `对照 ${report.family} 官方公开特征与基线比对`
              : '对照所声称型号特征与指纹基线比对'}
          </p>

          <div className='flex items-center gap-3 text-xs text-muted-foreground'>
            <span className='font-medium text-foreground'>
              综合一致性得分: <span className='tabular-nums font-semibold'>{report.score}</span> / 100
            </span>
          </div>
        </div>
      </div>

      {/* 8 个维度矩阵：紧凑栅格，只展示真实性与状态，无底下小字 */}
      <div className='space-y-2'>
        <div className='flex items-center justify-between text-xs text-muted-foreground px-0.5'>
          <span className='font-medium text-foreground'>多维特征核验 (8项)</span>
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
                title={finding.evidence ? `${finding.name}: ${finding.evidence}` : finding.name}
                className='flex flex-col justify-between rounded-lg border bg-muted/10 p-2.5 transition-colors hover:bg-muted/20 cursor-default'
              >
                <div className='flex items-center gap-1.5 text-xs'>
                  <Icon className='size-3.5 shrink-0 text-muted-foreground' />
                  <span className='truncate font-medium text-foreground' title={finding.name}>
                    {meta.title}
                  </span>
                </div>
                <div className='mt-2.5 flex items-center justify-end'>
                  <Badge variant={tone.variant} className={`text-[11px] px-1.5 py-0.5 gap-1 ${tone.badgeClass}`}>
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
