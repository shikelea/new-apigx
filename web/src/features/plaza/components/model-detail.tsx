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
*/import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

import { getPlaza, getRouteExplain } from '../api'
import { describeCandidateQuality, formatMicroUSD } from '../lib/format'
import { InvocationCard } from './invocation-card'
import type { RouteExplainCandidate } from '../types'

/** 模型详情：来源表 + 价格与质量对比（数据来自广场与可解释路由结果）。 */
export function ModelDetail(props: { model: string }) {
  const { t } = useTranslation()

  const plazaQuery = useQuery({ queryKey: ['apigx-plaza'], queryFn: () => getPlaza({ page: 1, page_size: 100 }) })
  const explainQuery = useQuery({
    queryKey: ['apigx-route-explain', props.model],
    queryFn: () => getRouteExplain(props.model),
  })

  if (plazaQuery.isPending || explainQuery.isPending) {
    return <LoadingState />
  }
  if (plazaQuery.isError || explainQuery.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void plazaQuery.refetch()
          void explainQuery.refetch()
        }}
      />
    )
  }

  const offerings = (plazaQuery.data?.data?.items ?? []).filter((item) => item.model === props.model)
  const candidates = explainQuery.data?.data?.candidates ?? []

  if (offerings.length === 0) {
    return <EmptyState title={t('plaza.detail.noSource')} />
  }

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle className='break-all'>{props.model}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className='flex flex-wrap gap-2'>
            {[...new Set(offerings.flatMap((item) => item.capabilities ?? []))].map((capability) => (
              <Badge key={capability} variant='secondary'>
                {capability}
              </Badge>
            ))}
          </div>
        </CardContent>
      </Card>

      <InvocationCard model={props.model} />

      <Card>
        <CardHeader>
          <CardTitle>{t('plaza.detail.sources')}</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('plaza.detail.channel')}</TableHead>
                <TableHead>{t('plaza.detail.owner')}</TableHead>
                <TableHead>{t('plaza.capabilities')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {offerings.map((item) => (
                <TableRow key={item.offering_public_id}>
                  <TableCell className='break-all font-mono text-xs'>{item.channel_public_id}</TableCell>
                  <TableCell>{item.owner_user_id}</TableCell>
                  <TableCell>{(item.capabilities ?? []).join(', ')}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('plaza.detail.compare')}</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('plaza.detail.rank')}</TableHead>
                <TableHead>{t('plaza.detail.quality')}</TableHead>
                <TableHead>{t('plaza.detail.latency')}</TableHead>
                <TableHead>{t('plaza.detail.price')}</TableHead>
                <TableHead>{t('plaza.detail.status')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {candidates.map((candidate) => (
                <TableRow key={candidate.offering_public_id}>
                  <TableCell>{candidate.rank}</TableCell>
                  <TableCell>{describeCandidateQuality(candidate)}</TableCell>
                  <TableCell>{candidate.latency_ms_p50 > 0 ? `${candidate.latency_ms_p50} ms` : ''}</TableCell>
                  <TableCell>
                    {candidate.price_known
                      ? `${formatMicroUSD(candidate.input_price_micro)} / ${formatMicroUSD(candidate.output_price_micro)}`
                      : t('plaza.detail.unknownPrice')}
                  </TableCell>
                  <TableCell>
                    <CandidateStatus candidate={candidate} selectedLabel={t('plaza.detail.selected')} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}
/**
 * 候选状态徽标。
 *
 * 抽成组件而不是在 JSX 里写嵌套三元 —— 项目 lint 明确禁止两层以上嵌套三元
 * （web/AGENTS.md 3.2），而"被排除 / 已选中 / 其它"本身就是三个分支。
 */
function CandidateStatus(props: { candidate: RouteExplainCandidate; selectedLabel: string }) {
  if (props.candidate.excluded_reason) {
    return <Badge variant='outline'>{props.candidate.excluded_reason}</Badge>
  }
  if (props.candidate.selected) {
    return <Badge>{props.selectedLabel}</Badge>
  }
  return null
}