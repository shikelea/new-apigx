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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { LoadingState } from '@/components/loading-state'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { handleServerError } from '@/lib/handle-server-error'

import { createShareChannel, getChannelRecipes } from '../api'
import type { ChannelRecipe, ShareGroup } from '../types'

const emptyRecipes: ChannelRecipe[] = []

function capabilityLabelKey(
  capability: ChannelRecipe['capability_hints'][number]
): string {
  if (capability === 'chat') return 'Chat'
  if (capability === 'image') return 'Image'
  if (capability === 'audio') return 'Audio'
  if (capability === 'embedding') return 'Embeddings'
  if (capability === 'rerank') return 'Rerank'
  return 'Video'
}

function recipeLabel(recipe: ChannelRecipe, t: (key: string) => string) {
  return recipe.id === 'new-api' ? t('New API instance') : recipe.label
}

/**
 * 新建分享渠道（§4「渠道 CRUD」）。
 *
 * 只需填配方：名称 / 服务端配方 / 上游地址 / **自己的上游 Key** —— 平台代建底座渠道并绑定，
 * 分享者不必（也不应该）知道管理员侧的渠道 ID。
 */
export function ChannelCreateDialog(props: {
  groups: ShareGroup[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: (createdId?: number) => void
}) {
  const { t } = useTranslation()
  const [groupId, setGroupId] = useState('')
  const [name, setName] = useState('')
  const [recipeId, setRecipeId] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [upstreamKey, setUpstreamKey] = useState('')

  const recipesQuery = useQuery({
    queryKey: ['apigx-channel-recipes'],
    queryFn: getChannelRecipes,
    enabled: props.open,
  })
  const recipes = recipesQuery.data?.data?.items ?? emptyRecipes
  const selectedRecipe = recipes.find((recipe) => recipe.id === recipeId)

  useEffect(() => {
    if (!props.open || recipes.length === 0 || selectedRecipe != null) return
    const firstRecipe = recipes[0]
    setRecipeId(firstRecipe.id)
    setBaseUrl((current) =>
      current.trim() === '' ? firstRecipe.default_base_url : current
    )
  }, [props.open, recipes, selectedRecipe])

  const selectRecipe = (nextRecipeId: string) => {
    const nextRecipe = recipes.find((recipe) => recipe.id === nextRecipeId)
    if (nextRecipe == null) return
    setRecipeId(nextRecipe.id)
    setBaseUrl((current) => {
      const currentWasDefault =
        selectedRecipe != null && current === selectedRecipe.default_base_url
      return current.trim() === '' || currentWasDefault
        ? nextRecipe.default_base_url
        : current
    })
  }

  const create = useMutation({
    mutationFn: () =>
      createShareChannel({
        share_group_id: Number(groupId),
        name: name.trim(),
        recipe_id: recipeId,
        base_url: baseUrl.trim(),
        upstream_key: upstreamKey.trim(),
      }),
    onSuccess: (res) => {
      setName('')
      setRecipeId('')
      setBaseUrl('')
      setUpstreamKey('')
      props.onOpenChange(false)
      props.onSaved(res.data?.id)
    },
    onError: (error) => handleServerError(error),
  })

  const missing =
    recipesQuery.isPending ||
    recipesQuery.isError ||
    selectedRecipe == null ||
    groupId === '' ||
    name.trim() === '' ||
    (selectedRecipe?.requires_base_url === true && baseUrl.trim() === '') ||
    upstreamKey.trim() === ''

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('New share channel')}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={missing || create.isPending}
            onClick={() => create.mutate()}
          >
            {t('Create')}
          </Button>
        </>
      }
    >
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor='share-channel-group'>
            {t('Share group')}
          </FieldLabel>
          <Select
            value={groupId}
            onValueChange={(next) => setGroupId(next ?? '')}
          >
            <SelectTrigger id='share-channel-group' className='w-full'>
              <SelectValue placeholder={t('Select a share group')}>
                {props.groups.find((group) => String(group.id) === groupId)
                  ?.name ?? ''}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {props.groups.map((group) => (
                  <SelectItem key={group.id} value={String(group.id)}>
                    {group.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel htmlFor='share-channel-name'>{t('Name')}</FieldLabel>
          <Input
            id='share-channel-name'
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={64}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor='share-channel-recipe'>
            {t('Upstream type')}
          </FieldLabel>
          <Select
            value={recipeId}
            disabled={
              recipesQuery.isPending ||
              recipesQuery.isError ||
              recipes.length === 0
            }
            onValueChange={(next) => selectRecipe(next ?? '')}
          >
            <SelectTrigger id='share-channel-recipe' className='w-full'>
              <SelectValue placeholder={t('Upstream type')}>
                {selectedRecipe == null ? '' : recipeLabel(selectedRecipe, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {recipes.map((recipe) => (
                  <SelectItem key={recipe.id} value={recipe.id}>
                    {recipeLabel(recipe, t)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          {recipesQuery.isPending ? (
            <LoadingState inline size='sm' message={t('Loading...')} />
          ) : null}
          {recipesQuery.isError ? (
            <Alert variant='destructive'>
              <AlertTitle>{t('Loading failed')}</AlertTitle>
              <AlertDescription>
                {t('Could not load channel recipes')}
              </AlertDescription>
              <AlertAction>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => void recipesQuery.refetch()}
                >
                  {t('Retry')}
                </Button>
              </AlertAction>
            </Alert>
          ) : null}
          {!recipesQuery.isPending &&
          !recipesQuery.isError &&
          recipes.length === 0 ? (
            <Alert>
              <AlertTitle>{t('No channel recipes are available')}</AlertTitle>
            </Alert>
          ) : null}
          {selectedRecipe == null ? null : (
            <>
              <FieldDescription>
                {t('Capabilities')}:{' '}
                {selectedRecipe.capability_hints
                  .map((capability) => t(capabilityLabelKey(capability)))
                  .join(', ')}
              </FieldDescription>
              {selectedRecipe.id === 'new-api' ? (
                <FieldDescription>
                  {t(
                    'Use this only when the upstream address is another New API deployment.'
                  )}
                </FieldDescription>
              ) : null}
              {selectedRecipe.async_task ? (
                <FieldDescription>
                  {t(
                    'This channel uses asynchronous tasks. After creation, fetch models, confirm prices, and submit them for review.'
                  )}
                </FieldDescription>
              ) : null}
            </>
          )}
        </Field>
        <Field>
          <FieldLabel htmlFor='share-channel-base-url'>
            {t('Upstream address')}
          </FieldLabel>
          <Input
            id='share-channel-base-url'
            placeholder='https://api.example.com'
            value={baseUrl}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor='share-channel-upstream-key'>
            {t('Your upstream key')}
          </FieldLabel>
          <Input
            id='share-channel-upstream-key'
            type='password'
            autoComplete='off'
            value={upstreamKey}
            onChange={(event) => setUpstreamKey(event.target.value)}
          />
          <FieldDescription>
            {t('It is stored encrypted and never shown again.')}
          </FieldDescription>
          {selectedRecipe?.credential_format ===
          'access_secret_pair_or_relay_key' ? (
            <FieldDescription>
              {t(
                'For a direct provider account, enter accessKey|secretKey. Relay keys beginning with sk- are also accepted.'
              )}
            </FieldDescription>
          ) : null}
        </Field>
      </FieldGroup>
    </Dialog>
  )
}
