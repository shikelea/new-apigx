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
*/import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { buildInvocationSnippets, resolveServerAddress } from '../lib/invocation'

/** 模型详情的「调用配置」：地址、模型名与可直接复制的最小示例。 */
export function InvocationCard(props: { model: string }) {
  const { t } = useTranslation()
  const snippets = buildInvocationSnippets(resolveServerAddress(), props.model)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Invocation')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='grid gap-2 text-sm sm:grid-cols-2'>
          <div className='break-all'>
            {t('Base URL')}: <span className='font-mono text-xs'>{snippets.baseURL}</span>
          </div>
          <div className='break-all'>
            {t('Model')}: <span className='font-mono text-xs'>{snippets.model}</span>
          </div>
        </div>

        <div className='space-y-1'>
          <div className='flex items-center justify-between'>
            <span className='text-sm font-medium'>curl</span>
            <CopyButton value={snippets.curl} />
          </div>
          <pre className='bg-muted overflow-x-auto rounded-md p-3 text-xs'>{snippets.curl}</pre>
        </div>

        <div className='space-y-1'>
          <div className='flex items-center justify-between'>
            <span className='text-sm font-medium'>Python (OpenAI SDK)</span>
            <CopyButton value={snippets.python} />
          </div>
          <pre className='bg-muted overflow-x-auto rounded-md p-3 text-xs'>{snippets.python}</pre>
        </div>
      </CardContent>
    </Card>
  )
}