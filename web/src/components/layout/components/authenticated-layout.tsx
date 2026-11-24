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
import { useQueryClient } from '@tanstack/react-query'
import { useLocation } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CommandMenu } from '@/components/command-menu'
import { AnimatedOutlet } from '@/components/page-transition'
import { SkipToMain } from '@/components/skip-to-main'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { setApiKeySecurityProofRequester } from '@/features/tokens/key-reveal-proof'
import { clearSidebarBadgesForPath } from '@/hooks/use-sidebar-badges'
import { useStatus } from '@/hooks/use-status'
import { getCookie } from '@/lib/cookies'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { resolveSidebarView } from '../lib/sidebar-view-registry'
import { AppHeader } from './app-header'
import { AppSidebar } from './app-sidebar'

type AuthenticatedLayoutProps = {
  children?: React.ReactNode
}

export function AuthenticatedLayout(props: AuthenticatedLayoutProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const queryClient = useQueryClient()
  const hasSidebarCookieRef = useRef(getCookie('sidebar_state') !== undefined)
  const userToggledSidebarRef = useRef(false)
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    const cookieVal = getCookie('sidebar_state')
    if (cookieVal !== undefined) {
      return cookieVal !== 'false'
    }
    return !status?.default_collapse_sidebar
  })
  useEffect(() => {
    if (
      status &&
      !hasSidebarCookieRef.current &&
      !userToggledSidebarRef.current
    ) {
      setSidebarOpen(!status.default_collapse_sidebar)
    }
  }, [status])
  const handleSidebarOpenChange = (nextOpen: boolean) => {
    userToggledSidebarRef.current = true
    setSidebarOpen(nextOpen)
  }
  const pathname = useLocation({ select: (location) => location.pathname })
  // Nested workspaces (System settings) carry longer section names, so their
  // drill-in navigation gets a wider rail (design system §3: 240px).
  const wideSidebar = resolveSidebarView(pathname) !== null
  const userId = useAuthStore((s) => s.auth.user?.id ?? 0)
  const signedIn = userId > 0
  const { requestVerification, dialogProps } = useSecureVerification()

  // R-32 #6: Clear menu badges on landing so direct URL navigation also clears them immediately.
  useEffect(() => {
    if (userId <= 0) return
    clearSidebarBadgesForPath(queryClient, pathname)
  }, [pathname, userId, queryClient])

  // R2-011 / R3-050: Register global single-use api_key.read step-up requester bound to token_id.
  useEffect(() => {
    if (!signedIn) {
      setApiKeySecurityProofRequester(null)
      return
    }
    setApiKeySecurityProofRequester(async ({ scope, tokenId }) => {
      const proof = await requestVerification({
        scope,
        context: { token_id: tokenId },
        title: t('Verify your identity'),
        description: t(
          'Viewing or copying a full API key requires security verification.'
        ),
      })
      return proof?.proof_token ?? null
    })
    return () => {
      setApiKeySecurityProofRequester(null)
    }
  }, [signedIn, requestVerification, t])

  // Unmount pages on sign-out, otherwise their queries refetch and toast "Session expired!" / "invalid access token"
  if (!signedIn) return null

  return (
    <LayoutProvider>
      <SearchProvider>
        <SidebarProvider
          open={sidebarOpen}
          onOpenChange={handleSidebarOpenChange}
          className='flex-col'
          style={
            wideSidebar
              ? ({ '--sidebar-width': '15rem' } as React.CSSProperties)
              : undefined
          }
        >
          <SkipToMain />
          <AppHeader />
          <div className='flex min-h-0 w-full flex-1'>
            <AppSidebar />
            <SidebarInset
              className={cn(
                '@container/content',
                'bg-canvas',
                'h-[calc(100svh-var(--app-header-height,0px))]',
                'min-h-0 overflow-hidden',
                'peer-data-[variant=inset]:h-[calc(100svh-var(--app-header-height,0px)-(var(--spacing)*4))]'
              )}
            >
              {props.children ?? <AnimatedOutlet />}
            </SidebarInset>
          </div>
        </SidebarProvider>
        <CommandMenu />
        <SecureVerificationDialog {...dialogProps} />
      </SearchProvider>
    </LayoutProvider>
  )
}
