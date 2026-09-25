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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { redeemAccessInvite } from '@/features/access-invite/api'

export function RedeemAccessCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [token, setToken] = useState('')
  const [redeemed, setRedeemed] = useState(false)
  const redeem = useMutation({
    mutationFn: () => redeemAccessInvite(token),
    onSuccess: async (response) => {
      const success = response.success !== false
      setRedeemed(success)
      if (!success) return
      setToken('')
      await queryClient.invalidateQueries({ queryKey: ['apigx-plaza'] })
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Redeem shared resources')}</CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Paste an invite code to add the resource to Shared with me in the model plaza.'
          )}
        </p>
        <div className='flex flex-col gap-3 sm:flex-row sm:items-end'>
          <Field className='flex-1'>
            <FieldLabel htmlFor='access-invite-redeem'>
              {t('Invite code')}
            </FieldLabel>
            <Input
              id='access-invite-redeem'
              value={token}
              autoComplete='off'
              onChange={(event) => {
                setToken(event.target.value)
                setRedeemed(false)
              }}
            />
          </Field>
          <Button
            disabled={token.trim() === '' || redeem.isPending}
            onClick={() => redeem.mutate()}
          >
            {t('Redeem invite')}
          </Button>
        </div>
        {redeemed ? (
          <p className='text-sm text-emerald-600'>
            {t('Invite redeemed. The shared resources are now visible.')}{' '}
            <Link to='/pricing' className='font-medium underline'>
              {t('Open Shared with me')}
            </Link>
          </p>
        ) : null}
        {redeem.isError || redeem.data?.success === false ? (
          <p className='text-destructive text-sm'>
            {t('Invalid or already used invite code')}
          </p>
        ) : null}
      </CardContent>
    </Card>
  )
}
