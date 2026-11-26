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
import { Lock } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { RichContent } from '@/components/rich-content'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { toContentLang } from '@/lib/adty-lang'

import manualEn from '../content/manual.en.md?raw'
import manualZh from '../content/manual.zh-CN.md?raw'

/** Authenticated internal operations manual visible only to site owners and Root. */
export function OwnerManualPanel() {
  const { t, i18n } = useTranslation()
  const lang = toContentLang(i18n.resolvedLanguage || i18n.language)
  const markdown = lang === 'zh-CN' ? manualZh : manualEn

  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <Lock className='size-4' />
        <AlertDescription>
          {t(
            'Internal operations manual visible only to authenticated site owners and platform administrators.'
          )}
        </AlertDescription>
      </Alert>

      <Card>
        <CardContent className='pt-6'>
          <article className='mx-auto max-w-4xl'>
            <RichContent
              mode='markdown'
              content={markdown}
              className='prose-neutral dark:prose-invert max-w-none [&_li]:leading-[1.8] [&_p]:leading-[1.8]'
            />
          </article>
        </CardContent>
      </Card>
    </div>
  )
}
