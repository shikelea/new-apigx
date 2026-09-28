import type { Control, FieldValues, Path } from 'react-hook-form'
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
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import type { RelayOption } from '../api'
import {
  PROBE_SOURCE_MANUAL,
  PROBE_SOURCE_RELAY,
  type ProbeSource,
  type ProbeTargetValues,
} from '../lib/probe-target'

// Shared target picker for the speed, dilution and crowdtest tools. The key
// input is deliberately type="password" with autoComplete off so a one-off
// upstream key does not end up in the browser's saved-form data.
export function ProbeTargetFields<T extends FieldValues>({
  control,
  source,
  relays,
  allowManual = true,
}: {
  control: Control<T>
  source: ProbeSource
  relays: RelayOption[]
  allowManual?: boolean
}) {
  const { t } = useTranslation()
  // Relay-sourced runs decrypt the stored detection key, so relays without one
  // would fail server-side; hide them rather than let the request 400.
  const usable = relays.filter((item) => item.credential_configured)
  const name = (field: keyof ProbeTargetValues) => field as Path<T>

  return (
    <>
      {allowManual ? (
        <FormField
          control={control}
          name={name('source')}
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Target')}</FormLabel>
              <FormControl>
                <NativeSelect className='w-full' {...field}>
                  <NativeSelectOption value={PROBE_SOURCE_RELAY}>
                    {t('One of my shared relays')}
                  </NativeSelectOption>
                  <NativeSelectOption value={PROBE_SOURCE_MANUAL}>
                    {t('Enter a base URL and key once')}
                  </NativeSelectOption>
                </NativeSelect>
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      ) : null}

      {source === PROBE_SOURCE_RELAY ? (
        <FormField
          control={control}
          name={name('relay_share_public_id')}
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Relay')}</FormLabel>
              <FormControl>
                <NativeSelect className='w-full' {...field}>
                  <NativeSelectOption value='' disabled>
                    {usable.length > 0
                      ? t('Select a relay with a detection key')
                      : t('No relay with a detection key yet')}
                  </NativeSelectOption>
                  {usable.map((item) => (
                    <NativeSelectOption
                      key={item.public_id}
                      value={item.public_id}
                    >
                      {item.title} ·{' '}
                      {item.kind === 'charity'
                        ? t('Charity relay')
                        : t('Commercial relay')}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      ) : (
        <>
          <FormField
            control={control}
            name={name('base_url')}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream base URL')}</FormLabel>
                <FormControl>
                  <Input
                    autoComplete='off'
                    placeholder='https://api.example.com/v1'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={control}
            name={name('key')}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API key')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='off'
                    placeholder='sk-...'
                    {...field}
                  />
                </FormControl>
                <FormDescription className='text-xs'>
                  {t(
                    'The key is used for this single run and is not stored. It is sent to the server over HTTPS, never to the relay operator.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </>
      )}

      <FormField
        control={control}
        name={name('model')}
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Model ID')}</FormLabel>
            <FormControl>
              <Input
                autoComplete='off'
                placeholder={t('e.g. gpt-4o')}
                {...field}
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
    </>
  )
}
