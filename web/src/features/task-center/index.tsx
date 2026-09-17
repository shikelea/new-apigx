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
import dayjs from 'dayjs'
import { useTranslation } from 'react-i18next'

import { StaticDataTable, type StaticDataTableColumn } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { TaskArtifactsCell } from '@/features/usage-logs/components/task-artifacts'

import { getMyTasks } from './api'
import { isTaskTerminal, taskStatusLabelKey } from './lib/status'
import type { UserTask } from './types'

const PAGE_SIZE = 50

/** 任务中心：查看自己的异步任务状态与结果。 */
export function TaskCenter() {
  const { t } = useTranslation()

  const query = useQuery({
    queryKey: ['apigx-my-tasks', PAGE_SIZE],
    queryFn: () => getMyTasks(1, PAGE_SIZE),
  })

  const tasks = query.data?.data?.items ?? []

  if (query.isPending) {
    return <LoadingState />
  }
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }

  const columns: StaticDataTableColumn<UserTask>[] = [
    {
      id: 'submit_time',
      header: t('taskCenter.submitTime'),
      cell: (row) => dayjs.unix(row.submit_time).format('YYYY-MM-DD HH:mm:ss'),
    },
    { id: 'task_id', header: t('taskCenter.taskId'), cell: (row) => row.task_id },
    { id: 'action', header: t('taskCenter.action'), cell: (row) => row.action },
    { id: 'status', header: t('taskCenter.status'), cell: (row) => <TaskStatus status={row.status} /> },
    { id: 'progress', header: t('taskCenter.progress'), cell: (row) => row.progress ?? '' },
    { id: 'quota', header: t('taskCenter.quota'), cell: (row) => String(row.quota) },
    {
      id: 'fail_reason',
      header: t('taskCenter.failReason'),
      cell: (row) => (isTaskTerminal(row.status) ? (row.fail_reason ?? '') : ''),
    },
    {
      id: 'artifacts',
      header: t('taskCenter.artifacts'),
      cell: (row) => <TaskArtifactsCell key={row.task_id} log={row} />,
    },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('taskCenter.title')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Card>
          <CardHeader>
            <CardTitle>{t('taskCenter.myTasks')}</CardTitle>
          </CardHeader>
          <CardContent>
            {tasks.length === 0 ? (
              <EmptyState title={t('taskCenter.empty')} />
            ) : (
              <StaticDataTable columns={columns} data={tasks} getRowKey={(row) => row.id} />
            )}
          </CardContent>
        </Card>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

/** 状态徽标：已知状态用文案，**未知状态显示原值**（不静默变空白）。 */
function TaskStatus(props: { status: string }) {
  const { t } = useTranslation()
  const labelKey = taskStatusLabelKey(props.status)
  const label = labelKey === '' ? props.status : t(labelKey)
  return (
    <span className={isTaskTerminal(props.status) ? 'font-medium' : 'text-muted-foreground'}>{label}</span>
  )
}