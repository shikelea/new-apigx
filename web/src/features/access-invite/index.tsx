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

import { useMutation } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { redeemAccessInvite } from './api'

/**
 * 资源邀请兑换入口（§4 第 3 条：资源邀请通过**独立兑换入口**处理）。
 *
 * 安全与体验上的两点约定：
 *  1. 兑换是**一次性**的（后端原子核销）：同一个码不能被两个人同时用掉；
 *  2. 失败时统一提示"邀请码无效或已被使用" —— 不区分"不存在/已撤销/已过期/已用尽"，
 *     避免用这个页面**枚举**有效邀请码（与后端同一口径）。
 */
export function AccessInviteRedeem() {
  const { t } = useTranslation()
  const [token, setToken] = useState('')

  const redeem = useMutation({
    mutationFn: () => redeemAccessInvite(token),
    onSuccess: () => {
      setToken('')
    },
  })

  const invalid = token.trim() === ''
  const succeeded = redeem.isSuccess && redeem.data?.success !== false
  const failed = redeem.isError || redeem.data?.success === false

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Redeem an invite')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Card className='max-w-xl'>
          <CardHeader>
            <CardTitle>{t('Redeem an invite')}</CardTitle>
          </CardHeader>
          <CardContent className='space-y-4'>
            <p className='text-muted-foreground text-sm'>
              {t('Paste the invite code you received to unlock the shared resources.')}
            </p>

            <div className='space-y-2'>
              <Label htmlFor='access-invite-token'>{t('Invite code')}</Label>
              <Input
                id='access-invite-token'
                value={token}
                autoComplete='off'
                onChange={(event) => setToken(event.target.value)}
              />
            </div>

            <Button
              disabled={invalid || redeem.isPending}
              onClick={() => redeem.mutate()}
            >
              {t('Redeem')}
            </Button>

            {succeeded ? (
              <p className='text-sm text-emerald-600'>
                {t('Invite redeemed. The shared resources are now visible.')}{' '}
                <Link to='/pricing' className='underline'>
                  {t('Open the model plaza')}
                </Link>
              </p>
            ) : null}

            {failed ? (
              <p className='text-destructive text-sm'>
                {t('Invalid or already used invite code')}
              </p>
            ) : null}
          </CardContent>
        </Card>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
