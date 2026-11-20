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
import { expect, test, type Page } from '@playwright/test'

const MONEY_API =
  /\/api\/(?:user\/(?:topup|self|quota)|topup|token|log\/self|adty\/(?:wallet|payment|topup|billing|ledger|fund|order)|billing)/i

type SessionWatch = {
  writes: string[]
  money: string[]
}

const LEGAL_TEXT: Record<string, Record<string, string>> = {
  'user-agreement': {
    en: 'Main site terms apply to this guest session.',
    'zh-CN': '主站用户协议适用于本次访客会话。',
  },
  'privacy-policy': {
    en: 'Main site privacy policy applies to this guest session.',
    'zh-CN': '主站隐私政策适用于本次访客会话。',
  },
}

function publicApiBody(url: string): unknown {
  const parsed = new URL(url)
  const path = parsed.pathname.replace(/\/+$/, '') || '/'
  const legal = path.match(/^\/api\/adty\/legal\/(user-agreement|privacy-policy)$/)
  if (legal) {
    const lang = parsed.searchParams.get('lang') === 'zh-CN' ? 'zh-CN' : 'en'
    return { success: true, data: LEGAL_TEXT[legal[1]][lang] }
  }
  // Root beforeLoad redirects to the setup wizard unless data.status is true.
  if (path === '/api/setup') {
    return { success: true, data: { status: true } }
  }
  if (path === '/api/status') {
    return {
      success: true,
      data: {
        system_name: 'ADTY AI',
        server_address: 'http://127.0.0.1:4173',
        HeaderNavModules: { docs: true, home: true },
      },
    }
  }
  if (path === '/api/adty/site') {
    return {
      success: true,
      data: {
        api_base_url: '',
        support_email: '',
        telegram_url: '',
        support_hours: '',
        tawk_property_id: '',
        tawk_widget_id: '',
        refund_policy_enabled: false,
      },
    }
  }
  if (path === '/api/pricing') {
    return {
      success: true,
      data: [],
      vendors: [],
      group_ratio: {},
      usable_group: {},
      supported_endpoint: {},
      auto_groups: [],
    }
  }
  if (path === '/api/notice' || path === '/api/home_page_content') {
    return { success: true, data: '' }
  }
  return { success: true, data: {} }
}

async function watchPublicSession(page: Page): Promise<SessionWatch> {
  const watch: SessionWatch = { writes: [], money: [] }
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const method = request.method()
    const url = request.url()
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
      watch.writes.push(`${method} ${url}`)
    }
    if (MONEY_API.test(url)) {
      watch.money.push(`${method} ${url}`)
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(publicApiBody(url)),
    })
  })
  return watch
}

async function openAsMainSiteGuest(page: Page, language: 'en' | 'zhCN') {
  await page.addInitScript((stored) => {
    const key = 'i18nextLng'
    // Later navigations must keep the language the switcher wrote.
    if (!window.localStorage.getItem(key)) {
      window.localStorage.setItem(key, stored)
    }
  }, language)
  await page.goto('/')
}

async function assertGuestOnMainSite(page: Page, signIn: string) {
  const siteId = await page.evaluate(
    () => document.documentElement.dataset.adtySiteId ?? '',
  )
  expect(siteId).toBe('')
  // Desktop header exposes Sign in as a button; the mobile drawer exposes a link.
  const entry = page
    .getByRole('link', { name: signIn })
    .or(page.getByRole('button', { name: signIn }))
  await expect(entry.first()).toBeVisible()
  await expect(entry.first()).toHaveAttribute('href', /\/sign-in\/?$/)
}

async function assertNoMoneyChange(watch: SessionWatch) {
  expect(watch.writes).toEqual([])
  expect(watch.money).toEqual([])
}

async function assertMainFits(page: Page, maxWidth: number) {
  const viewport = page.viewportSize()
  expect(viewport).not.toBeNull()
  const box = await page.locator('main').boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeLessThanOrEqual(maxWidth)
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(viewport!.width + 1)
  const fits = await page.evaluate(
    () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
  )
  expect(fits).toBe(true)
}

test('ADTY-01-04 help center entry opens the quick start', async ({ page }) => {
  const watch = await watchPublicSession(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, 'en')
  await assertGuestOnMainSite(page, 'Sign in')

  await page.getByRole('link', { name: 'Docs', exact: true }).first().click()
  await expect(page).toHaveURL(/\/docs\/?$/)
  await expect(
    page.getByRole('heading', { level: 1, name: 'Quick start' }),
  ).toBeVisible()
  await expect(
    page.getByRole('navigation', { name: 'Documentation' }),
  ).toBeVisible()
  await expect(
    page.getByRole('link', { name: 'Quick start' }).first(),
  ).toHaveAttribute('aria-current', 'page')
  await assertNoMoneyChange(watch)
})

test('ADTY-01-10 public pages stay inside the viewport', async ({ page }) => {
  const watch = await watchPublicSession(page)
  await openAsMainSiteGuest(page, 'en')

  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/docs/')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Quick start' }),
  ).toBeVisible()
  await assertMainFits(page, 1180)
  await expect(page.locator('main')).toHaveCSS('padding-left', '40px')
  await expect(page.getByRole('link', { name: 'Docs', exact: true }).first()).toBeVisible()

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/docs/')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Quick start' }),
  ).toBeVisible()
  await assertMainFits(page, 390)
  await expect(page.locator('main')).toHaveCSS('padding-left', '20px')
  await page.getByRole('button', { name: 'Toggle navigation menu' }).click()
  await expect(page.getByRole('link', { name: 'Docs', exact: true }).first()).toBeVisible()
  await assertGuestOnMainSite(page, 'Sign in')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-10 docs and the help entry switch between Chinese and English', async ({
  page,
}) => {
  const watch = await watchPublicSession(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, 'en')
  await page.goto('/docs/')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Quick start' }),
  ).toBeVisible()
  await expect(page.getByRole('link', { name: 'Docs', exact: true }).first()).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('lang', /en/i)

  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(
    page.getByRole('heading', { level: 1, name: '快速开始' }),
  ).toBeVisible()
  await expect(page.getByRole('link', { name: '文档', exact: true }).first()).toBeVisible()
  await expect(page).toHaveURL(/\/docs\/?$/)
  await expect(page.locator('html')).toHaveAttribute('lang', /zh/i)

  await page.goto('/docs/faq')
  await expect(page.getByRole('heading', { level: 1, name: '常见问题' })).toBeVisible()
  await page.getByRole('button', { name: '更改语言' }).first().click()
  await page.getByRole('menuitem', { name: 'English' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'FAQ' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Docs', exact: true }).first()).toBeVisible()
  await expect(page).toHaveURL(/\/docs\/faq$/)
  await assertGuestOnMainSite(page, 'Sign in')
  await assertNoMoneyChange(watch)
})

test('ADTY-01-01 home shows the main-site brand to a guest', async ({ page }) => {
  const watch = await watchPublicSession(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, 'en')
  await expect(page).toHaveURL(/\/$/)
  await expect(
    page.getByRole('heading', { level: 1, name: 'One endpoint. Every model.' }),
  ).toBeVisible()
  await expect(page.getByRole('link', { name: 'ADTY AI' }).first()).toBeVisible()
  await assertGuestOnMainSite(page, 'Sign in')
  await assertNoMoneyChange(watch)
})

test('ADTY-01-05 terms page shows the configured agreement', async ({ page }) => {
  const watch = await watchPublicSession(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, 'en')
  await page.goto('/user-agreement')
  await expect(page).toHaveURL(/\/user-agreement$/)
  await expect(
    page.getByRole('heading', { level: 1, name: 'User Agreement' }),
  ).toBeVisible()
  await expect(
    page.getByText('Main site terms apply to this guest session.'),
  ).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(
    page.getByRole('heading', { level: 1, name: '用户协议' }),
  ).toBeVisible()
  await expect(page.getByText('主站用户协议适用于本次访客会话。')).toBeVisible()
  await expect(page).toHaveURL(/\/user-agreement$/)
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-01-06 privacy page shows the configured policy', async ({ page }) => {
  const watch = await watchPublicSession(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, 'en')
  await page.goto('/privacy-policy')
  await expect(page).toHaveURL(/\/privacy-policy$/)
  await expect(
    page.getByRole('heading', { level: 1, name: 'Privacy Policy' }),
  ).toBeVisible()
  await expect(
    page.getByText('Main site privacy policy applies to this guest session.'),
  ).toBeVisible()
  await assertGuestOnMainSite(page, 'Sign in')
  await assertNoMoneyChange(watch)
})
