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
*/ import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import { getPlatformKeyPreferences, setPlatformKeyPreferences } from '../../api'
import {
  buildScopePayload,
  ROUTE_MODE_OPTIONS,
  type ScopeFormState,
} from '../../lib/apigx-scope'

const EMPTY_FORM: ScopeFormState = {
  scopeMode: '0',
  pinnedOfferingId: '',
  allowedModelIds: '',
  allowedOwnerUserIds: '',
  allowFallback: true,
  defaultRouteMode: 'auto',
}

/**
 * 平台 Key 的**来源限制与路由策略**（计划书 §9.1：偏好保存在平台 Key 上）。
 *
 * 表单用 `GET` 的当前值**预填**后再整体提交 ⇒ 幂等往返，不会误清空用户没动过的限制。
 */
export function ApigxScopeDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  tokenId: number
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<ScopeFormState>(EMPTY_FORM)

  const prefsQuery = useQuery({
    queryKey: ['apigx-key-preferences', props.tokenId],
    queryFn: () => getPlatformKeyPreferences(props.tokenId),
    enabled: props.open && props.tokenId > 0,
  })

  useEffect(() => {
    const prefs = prefsQuery.data?.data
    if (!prefs) {
      return
    }
    setForm({
      scopeMode: String(prefs.scope_mode),
      pinnedOfferingId:
        prefs.pinned_offering_id > 0 ? String(prefs.pinned_offering_id) : '',
      allowedModelIds: prefs.allowed_model_ids.join(', '),
      allowedOwnerUserIds: prefs.allowed_owner_user_ids.join(', '),
      allowFallback: prefs.allow_fallback,
      defaultRouteMode:
        prefs.default_route_mode === '' ? 'auto' : prefs.default_route_mode,
    })
  }, [prefsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () =>
      setPlatformKeyPreferences(props.tokenId, buildScopePayload(form)),
    onSuccess: () => {
      props.onOpenChange(false)
    },
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('apigxKey.scope.title')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <p className='text-muted-foreground text-xs'>
          {t('apigxKey.scope.hint')}
        </p>

        <div className='space-y-2'>
          <Label>{t('apigxKey.scope.routeMode')}</Label>
          <Select
            value={form.defaultRouteMode}
            onValueChange={(next) =>
              setForm({ ...form, defaultRouteMode: next ?? 'auto' })
            }
          >
            <SelectTrigger>
              <SelectValue>
                {t(
                  ROUTE_MODE_OPTIONS.find(
                    (option) => option.value === form.defaultRouteMode
                  )?.labelKey ?? 'apigxKey.routeMode.auto'
                )}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {ROUTE_MODE_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.labelKey)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className='space-y-2'>
          <Label>{t('apigxKey.scope.pinnedOffering')}</Label>
          <Input
            value={form.pinnedOfferingId}
            onChange={(event) =>
              setForm({ ...form, pinnedOfferingId: event.target.value })
            }
            placeholder={t('apigxKey.scope.pinnedOfferingPlaceholder')}
          />
        </div>

        <div className='space-y-2'>
          <Label>{t('apigxKey.scope.allowedModels')}</Label>
          <Input
            value={form.allowedModelIds}
            onChange={(event) =>
              setForm({ ...form, allowedModelIds: event.target.value })
            }
            placeholder={t('apigxKey.scope.listPlaceholder')}
          />
        </div>

        <div className='space-y-2'>
          <Label>{t('apigxKey.scope.allowedOwners')}</Label>
          <Input
            value={form.allowedOwnerUserIds}
            onChange={(event) =>
              setForm({ ...form, allowedOwnerUserIds: event.target.value })
            }
            placeholder={t('apigxKey.scope.listPlaceholder')}
          />
        </div>

        <div className='flex items-center gap-3'>
          <Switch
            checked={form.allowFallback}
            onCheckedChange={(next) =>
              setForm({ ...form, allowFallback: next })
            }
            id='apigx-scope-fallback'
          />
          <Label htmlFor='apigx-scope-fallback'>
            {t('apigxKey.scope.allowFallback')}
          </Label>
        </div>
      </div>
    </Dialog>
  )
}
