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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { getShareGroups } from '@/features/share/api'

import { GrantAccessCard } from './components/grant-card'
import { AccessInviteCard } from './components/invite-card'
import { RedeemAccessCard } from './components/redeem-card'

export function AccessAndSharing() {
  const { t } = useTranslation()
  const groupsQuery = useQuery({
    queryKey: ['apigx-share-groups'],
    queryFn: getShareGroups,
  })

  let content = <LoadingState />
  if (groupsQuery.isError) {
    content = (
      <ErrorState
        description={groupsQuery.error.message}
        onRetry={() => void groupsQuery.refetch()}
      />
    )
  } else if (!groupsQuery.isPending) {
    const groups = groupsQuery.data?.data?.items ?? []
    const shareableGroups = groups.filter(
      (group) => group.default_visibility === 1 && group.publish_status === 2
    )
    content = (
      <div className='flex flex-col gap-4'>
        <RedeemAccessCard />
        {shareableGroups.length > 0 ? (
          <div className='grid gap-4 xl:grid-cols-2'>
            <AccessInviteCard groups={shareableGroups} />
            <GrantAccessCard groups={shareableGroups} />
          </div>
        ) : (
          <EmptyState
            bordered
            className='min-h-52'
            title={t('No restricted shared resource')}
            description={t(
              'Create a shared resource with invite or specific-user access before issuing access here.'
            )}
          />
        )}
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Access and sharing')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Issue access, redeem invite codes, and review who can use your shared resources.'
            )}
          </p>
          {content}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
