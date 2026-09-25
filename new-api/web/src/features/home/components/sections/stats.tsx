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
import {
  AudioLines,
  Database,
  Image,
  ListFilter,
  MessageSquareText,
  Video,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function Stats() {
  const { t } = useTranslation()
  const capabilities = [
    { label: t('Conversation API'), icon: MessageSquareText },
    { label: t('Image API'), icon: Image },
    { label: t('Audio API'), icon: AudioLines },
    { label: t('Embedding API'), icon: Database },
    { label: t('Rerank API'), icon: ListFilter },
    { label: t('Video API'), icon: Video },
  ]

  return (
    <section
      aria-label={t('Six AI API capabilities')}
      className='border-border/60 bg-muted/10 relative z-10 border-y'
    >
      <div className='bg-border/50 mx-auto grid max-w-6xl grid-cols-2 gap-px sm:grid-cols-3 lg:grid-cols-6'>
        {capabilities.map(({ label, icon: Icon }) => (
          <div
            className='bg-background flex min-w-0 items-center gap-2.5 px-4 py-4 sm:justify-center'
            key={label}
          >
            <Icon
              aria-hidden
              className='text-muted-foreground size-4 shrink-0'
              strokeWidth={1.7}
            />
            <span className='truncate text-xs font-medium'>{label}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
