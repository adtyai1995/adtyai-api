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
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout/components/public-layout'
import { RichContent } from '@/components/rich-content'
import { useTheme } from '@/context/theme-provider'
import { AdtyLanding } from '@/features/adty-home'
import { useRemovePrerenderOnMount } from '@/features/adty-seo/prerender-cleanup'
import { useStatus } from '@/hooks/use-status'
import { isHttpUrl, isLikelyHtml } from '@/lib/content-format'
import { subsiteDocument } from '@/lib/subsite-document'

import { useHomePageContent } from './hooks'

export function Home() {
  // The content can switch from the default landing to a custom homepage
  // while its stylesheet loads. Keep the handover active across both views.
  useRemovePrerenderOnMount()
  const { i18n, t } = useTranslation()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const { resolvedTheme } = useTheme()
  const { status } = useStatus()
  const { content, isLoaded, isUrl } = useHomePageContent()
  const isSubsite = Boolean(
    subsiteDocument() || status?.adty_site || status?.data?.adty_site
  )

  const syncIframePreferences = useCallback(() => {
    try {
      iframeRef.current?.contentWindow?.postMessage(
        { themeMode: resolvedTheme },
        '*'
      )
      iframeRef.current?.contentWindow?.postMessage(
        { lang: i18n.language },
        '*'
      )
    } catch {
      // Cross-origin frames may reject access while navigating.
    }
  }, [i18n.language, resolvedTheme])

  useEffect(() => {
    if (isUrl) {
      syncIframePreferences()
    }
  }, [isUrl, syncIframePreferences])

  // ADTY task 8.3: while the custom home content loads, show the landing page
  // instead of a loading text; without custom content it simply stays.
  if (isLoaded && content) {
    if (isUrl && isHttpUrl(content)) {
      const iframeSandbox = isSubsite
        ? 'allow-forms allow-popups allow-scripts'
        : 'allow-forms allow-popups allow-scripts allow-top-navigation-by-user-activation'
      return (
        <PublicLayout showMainContainer={false}>
          {/*
            Subsite custom homepage URLs stay strictly sandboxed without top
            navigation or popup sandbox escape; main-site admin URLs keep
            user-activated top navigation only, without popup sandbox escape.
          */}
          <iframe
            ref={iframeRef}
            src={content}
            className='h-screen w-full border-none'
            title={t('Custom Home Page')}
            sandbox={iframeSandbox}
            onLoad={syncIframePreferences}
          />
        </PublicLayout>
      )
    }

    const contentIsHtml = isLikelyHtml(content)

    if (contentIsHtml) {
      return (
        <PublicLayout showMainContainer={false}>
          <RichContent
            mode='html'
            htmlVariant={isSubsite ? 'subsite-isolated' : 'isolated'}
            content={content}
            className='custom-home-content'
          />
        </PublicLayout>
      )
    }

    return (
      <PublicLayout>
        <div className='mx-auto max-w-6xl px-4 py-8'>
          <RichContent
            mode='markdown'
            content={content}
            className='custom-home-content'
          />
        </div>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout showMainContainer={false}>
      <AdtyLanding />
    </PublicLayout>
  )
}
