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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { getAllPlazaOfferings } from '@/features/plaza/api'

import { getPlatformKeyPreferences, setPlatformKeyPreferences } from '../../api'
import {
  buildScopePayload,
  ROUTE_MODE_OPTIONS,
  type ResourceScope,
  type ScopeFormState,
} from '../../lib/apigx-scope'

const EMPTY_FORM: ScopeFormState = {
  resourceScope: 'all',
  pinnedOfferingPublicId: '',
  allowedModelIds: [],
  allowFallback: true,
  defaultRouteMode: 'auto',
}

const RESOURCE_SCOPE_OPTIONS: Array<{
  value: ResourceScope
  labelKey: string
  descriptionKey: string
}> = [
  {
    value: 'all',
    labelKey: 'apigxKey.resourceScope.all',
    descriptionKey: 'apigxKey.resourceScope.all.description',
  },
  {
    value: 'public',
    labelKey: 'apigxKey.resourceScope.public',
    descriptionKey: 'apigxKey.resourceScope.public.description',
  },
  {
    value: 'private',
    labelKey: 'apigxKey.resourceScope.private',
    descriptionKey: 'apigxKey.resourceScope.private.description',
  },
  {
    value: 'shared',
    labelKey: 'apigxKey.resourceScope.shared',
    descriptionKey: 'apigxKey.resourceScope.shared.description',
  },
  {
    value: 'pinned',
    labelKey: 'apigxKey.resourceScope.pinned',
    descriptionKey: 'apigxKey.resourceScope.pinned.description',
  },
]

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
  const queryClient = useQueryClient()
  const [form, setForm] = useState<ScopeFormState>(EMPTY_FORM)

  const prefsQuery = useQuery({
    queryKey: ['apigx-key-preferences', props.tokenId],
    queryFn: () => getPlatformKeyPreferences(props.tokenId),
    enabled: props.open && props.tokenId > 0,
  })
  const plazaQuery = useQuery({
    queryKey: ['apigx-plaza', 'key-scope-picker'],
    queryFn: getAllPlazaOfferings,
    enabled: props.open,
    retry: false,
  })

  useEffect(() => {
    const prefs = prefsQuery.data?.data
    if (!prefs) {
      return
    }
    const allowedAccessKinds = prefs.allowed_access_kinds ?? []
    let resourceScope: ResourceScope = 'all'
    if (prefs.scope_mode === 2 && prefs.pinned_offering_public_id) {
      resourceScope = 'pinned'
    } else if (prefs.scope_mode === 3 && allowedAccessKinds.length === 1) {
      resourceScope = allowedAccessKinds[0]
    }
    setForm({
      resourceScope,
      pinnedOfferingPublicId: prefs.pinned_offering_public_id ?? '',
      allowedModelIds: prefs.allowed_model_ids,
      allowFallback: prefs.allow_fallback,
      defaultRouteMode:
        prefs.default_route_mode === '' ? 'auto' : prefs.default_route_mode,
    })
  }, [prefsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () =>
      setPlatformKeyPreferences(props.tokenId, buildScopePayload(form)),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['apigx-key-preferences', props.tokenId],
      })
      props.onOpenChange(false)
    },
  })

  const selectedScope =
    RESOURCE_SCOPE_OPTIONS.find(
      (option) => option.value === form.resourceScope
    ) ?? RESOURCE_SCOPE_OPTIONS[0]
  const offerings = plazaQuery.data ?? []
  const selectedOffering = offerings.find(
    (item) => item.offering_public_id === form.pinnedOfferingPublicId
  )

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
            disabled={
              saveMutation.isPending ||
              prefsQuery.isPending ||
              (form.resourceScope === 'pinned' &&
                form.pinnedOfferingPublicId === '')
            }
            onClick={() => saveMutation.mutate()}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <div className='flex flex-col gap-4'>
        <Alert>
          <AlertTitle>{t('apigxKey.scope.hint')}</AlertTitle>
          <AlertDescription>
            {t('apigxKey.scope.resourceHint')}
          </AlertDescription>
        </Alert>

        <FieldGroup>
          <Field>
            <FieldLabel htmlFor='apigx-resource-scope'>
              {t('apigxKey.scope.resourceScope')}
            </FieldLabel>
            <Select
              value={form.resourceScope}
              onValueChange={(next) =>
                setForm({
                  ...form,
                  resourceScope: (next ?? 'all') as ResourceScope,
                })
              }
            >
              <SelectTrigger id='apigx-resource-scope'>
                <SelectValue>{t(selectedScope.labelKey)}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {RESOURCE_SCOPE_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.labelKey)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <p className='text-muted-foreground text-xs'>
              {t(selectedScope.descriptionKey)}
            </p>
          </Field>

          {form.resourceScope === 'pinned' ? (
            <Field>
              <FieldLabel htmlFor='apigx-pinned-offering'>
                {t('apigxKey.scope.pinnedOffering')}
              </FieldLabel>
              <Select
                value={form.pinnedOfferingPublicId}
                onValueChange={(next) =>
                  setForm({ ...form, pinnedOfferingPublicId: next ?? '' })
                }
                disabled={plazaQuery.isPending || plazaQuery.isError}
              >
                <SelectTrigger id='apigx-pinned-offering'>
                  <SelectValue
                    placeholder={
                      plazaQuery.isPending
                        ? t('Loading...')
                        : t('apigxKey.scope.selectOffering')
                    }
                  >
                    {selectedOffering
                      ? `${selectedOffering.model} · ${selectedOffering.owner_display_name || selectedOffering.group_name || t('Share source')}`
                      : ''}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {offerings.map((offering) => (
                      <SelectItem
                        key={offering.offering_public_id}
                        value={offering.offering_public_id}
                      >
                        {offering.model} ·{' '}
                        {offering.owner_display_name ||
                          offering.group_name ||
                          t('Share source')}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              {plazaQuery.isError ? (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => void plazaQuery.refetch()}
                >
                  {t('Retry')}
                </Button>
              ) : null}
            </Field>
          ) : null}

          <Field>
            <FieldLabel htmlFor='apigx-route-mode'>
              {t('apigxKey.scope.routeMode')}
            </FieldLabel>
            <Select
              value={form.defaultRouteMode}
              onValueChange={(next) =>
                setForm({ ...form, defaultRouteMode: next ?? 'auto' })
              }
            >
              <SelectTrigger id='apigx-route-mode'>
                <SelectValue>
                  {t(
                    ROUTE_MODE_OPTIONS.find(
                      (option) => option.value === form.defaultRouteMode
                    )?.labelKey ?? 'apigxKey.scope.routeMode.auto'
                  )}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {ROUTE_MODE_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.labelKey)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>

          {form.resourceScope === 'pinned' ? (
            <div className='bg-muted/20 flex items-center justify-between gap-4 rounded-lg border p-3'>
              <div>
                <Label htmlFor='apigx-scope-fallback'>
                  {t('apigxKey.scope.allowFallback')}
                </Label>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('apigxKey.scope.allowFallback.description')}
                </p>
              </div>
              <Switch
                id='apigx-scope-fallback'
                checked={form.allowFallback}
                onCheckedChange={(next) =>
                  setForm({ ...form, allowFallback: next })
                }
              />
            </div>
          ) : null}
        </FieldGroup>
      </div>
    </Dialog>
  )
}
