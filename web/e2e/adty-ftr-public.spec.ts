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
import { execFileSync } from 'node:child_process'
import { setTimeout as delay } from 'node:timers/promises'

import { expect, test as base, type Page, type Response } from '@playwright/test'

const MONEY_API =
  /\/api\/(?:user\/(?:topup|self|quota)|topup|token|log\/self|adty\/(?:wallet|payment|topup|billing|ledger|fund|order)|billing)/i

type SessionWatch = {
  writes: string[]
  money: string[]
  real?: {
    responses: Response[]
    retried: Set<Response>
    apiBaseURL?: string
  }
}

const isolatedBaseURL = process.env.ADTY_BROWSER_BASE_URL
const seoScenario = isolatedBaseURL && process.env.ADTY_BROWSER_SCENARIO === 'ftr-seo'
const MONEY_TABLES = [
  'adty_teams',
  'adty_site_wallets',
  'adty_fund_batches',
  'adty_fund_allocations',
  'adty_balance_ledgers',
  'adty_team_ledgers',
  'adty_site_wallet_ledgers',
  'top_ups',
  'adty_topup_orders',
  'adty_manual_refunds',
  'adty_order_payment_events',
]

function readBrowserDatabase(expression: string) {
  const database = process.env.ADTY_BROWSER_DATABASE
  const dsn = process.env.ADTY_BROWSER_SQL_DSN
  if (!dsn || !database || !/^adty_browser_\d+_\d+_\d+$/.test(database)) {
    throw new Error('Run the FTR with the isolated browser-business database')
  }
  const sql = `
    BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
    SELECT ${expression}
    WHERE current_database() = '${database}';
    COMMIT;
  `
  let output: string
  try {
    output = execFileSync(
      'psql',
      ['-XqAtw', '-v', 'ON_ERROR_STOP=1', '-d', dsn, '-c', sql],
      { encoding: 'utf8', timeout: 10_000, stdio: ['ignore', 'pipe', 'pipe'] },
    ).trim()
  } catch {
    // The child process arguments may contain PostgreSQL credentials.
    throw new Error('Could not read the isolated PostgreSQL database')
  }
  expect(output, 'Read from the database created by browser-business').not.toBe('')
  return JSON.parse(output)
}

function moneySnapshot() {
  // Hash the ordered PostgreSQL rows before parsing JSON to preserve exact money values.
  const tables = [
    ['users', '(SELECT id, site_id, quota, quota_pending, used_quota, checkin_quota FROM users)'],
    ...MONEY_TABLES.map((table) => [table, table]),
  ].map(
    ([name, source]) => `'${name}', (
      SELECT jsonb_build_object(
        'rows', count(*),
        'sha256', encode(sha256(convert_to(coalesce(jsonb_agg(r ORDER BY r.id), '[]')::text, 'UTF8')), 'hex')
      )
      FROM ${source} AS r
    )`,
  )
  const snapshot = readBrowserDatabase(`
    jsonb_build_object(
      'database', current_database(),
      'serverVersion', current_setting('server_version_num')::integer,
      ${tables.join(',\n')}
    )
  `)
  expect(snapshot.serverVersion).toBeGreaterThanOrEqual(180_000)
  expect(snapshot.serverVersion).toBeLessThan(190_000)
  expect(snapshot.users.rows, 'The setup account supplies a balance to compare').toBeGreaterThan(0)
  return snapshot
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
  function observe(method: string, url: string) {
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
      watch.writes.push(`${method} ${url}`)
    }
    if (MONEY_API.test(url)) {
      watch.money.push(`${method} ${url}`)
    }
  }
  if (isolatedBaseURL) {
    const origin = new URL(isolatedBaseURL).origin
    const real = { responses: [] as Response[], retried: new Set<Response>() }
    watch.real = real
    page.on('request', (request) => {
      const url = new URL(request.url())
      if (url.origin === origin && url.pathname.startsWith('/api/')) {
        observe(request.method(), url.pathname)
      }
    })
    page.on('response', (response) => {
      if (new URL(response.url()).origin === origin) {
        real.responses.push(response)
      }
    })
    return watch
  }
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const url = request.url()
    observe(request.method(), url)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(publicApiBody(url)),
    })
  })
  return watch
}

const test = base.extend<{ watch: SessionWatch }>({
  watch: async ({ page }, use, testInfo) => {
    const watch = await watchPublicSession(page)
    if (!watch.real) {
      await use(watch)
      return
    }
    const before = moneySnapshot()
    try {
      await use(watch)
    } finally {
      const after = moneySnapshot()
      const { responses, retried } = watch.real
      const responseFacts = responses.map((response) => ({
        method: response.request().method(),
        path: new URL(response.url()).pathname,
        status: response.status(),
        retried: retried.has(response),
      }))
      await testInfo.attach('public-session', {
        contentType: 'application/json',
        body: JSON.stringify({
          before,
          after,
          writes: watch.writes,
          money: watch.money,
          responses: responseFacts.filter(({ path, status }) => path.startsWith('/api/') || status >= 400),
        }),
      })
      expect
        .soft(after, 'Balances, ledgers and orders before and after the guest scenario')
        .toEqual(before)
      expect.soft(
        responseFacts.filter(({ path, status, retried }) =>
          status >= 400 && !retried && (path.startsWith('/api/') || status === 429),
        ),
        'Public API errors and remaining rate limits',
      ).toEqual([])
      await assertNoMoneyChange(watch)
    }
  },
})

if (seoScenario) {
  base('ADTY-01-09 Root saves bilingual SEO for the public home pages', async ({
    page,
    browser,
  }, testInfo) => {
    const username = process.env.ADTY_BROWSER_USERNAME
    const password = process.env.ADTY_BROWSER_PASSWORD
    if (!username || !password) throw new Error('The isolated Root account is required')
    const before = moneySnapshot()
    const settingsPath = '/system-settings/site/adty-seo'
    const titles = { en: 'Gateway for teams', 'zh-CN': '团队网关' }
    const descriptions = { en: 'Manage model access', 'zh-CN': '管理模型接入' }
    const shareImage = '/adty-seo-browser-share.png'
    const options = {
      'adty_seo_setting.home_title': JSON.stringify(titles),
      'adty_seo_setting.home_description': JSON.stringify(descriptions),
      'adty_seo_setting.og_image': shareImage,
    }

    await page.goto('/sign-in')
    await page.getByLabel('Username or Email').fill(username)
    await page.getByLabel('Password', { exact: true }).fill(password)
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()

    await page.goto(settingsPath)
    for (const [lang, name] of [['en', 'English'], ['zh-CN', '简体中文']] as const) {
      await page.getByRole('tab', { name, exact: true }).click()
      await page.locator(`#adty-seo-title-${lang}`).fill(titles[lang])
      await page.locator(`#adty-seo-description-${lang}`).fill(descriptions[lang])
    }
    await page.getByLabel('Share image').fill(shareImage)
    const writes = Object.entries(options).map(async ([key, value]) => {
      const response = await page.waitForResponse((candidate) =>
        new URL(candidate.url()).pathname === '/api/option/' &&
        candidate.request().method() === 'PUT' &&
        candidate.request().postDataJSON().key === key,
      )
      expect(response.request().postDataJSON()).toEqual({ key, value })
      expect(response.status()).toBe(200)
      expect(await response.json()).toMatchObject({ success: true })
    })
    await Promise.all([
      ...writes,
      page.getByRole('button', { name: 'Save Changes', exact: true }).click(),
    ])

    await page.reload()
    for (const [lang, name] of [['en', 'English'], ['zh-CN', '简体中文']] as const) {
      await page.getByRole('tab', { name, exact: true }).click()
      await expect(page.locator(`#adty-seo-title-${lang}`)).toHaveValue(titles[lang])
      await expect(page.locator(`#adty-seo-description-${lang}`)).toHaveValue(descriptions[lang])
    }
    await expect(page.getByLabel('Share image')).toHaveValue(shareImage)

    const persisted = readBrowserDatabase(`
      jsonb_build_object(
        'options', (
          SELECT jsonb_object_agg(key, value)
          FROM options WHERE key LIKE 'adty_seo_setting.%'
        ),
        'audits', (
          SELECT jsonb_agg(
            jsonb_build_object('key', a.object_id, 'actor', u.username,
              'role', a.actor_role, 'site', u.site_id, 'success', a.success)
            ORDER BY a.object_id, a.id
          )
          FROM audit_logs a JOIN users u ON u.id = a.user_id
          WHERE a.action = 'option.update' AND a.object_id LIKE 'adty_seo_setting.%'
        )
      )
    `)
    expect(persisted.options).toMatchObject(options)
    expect(persisted.audits).toEqual(
      Object.keys(options).sort().map((key) => ({
        key, actor: username, role: 100, site: 0, success: true,
      })),
    )

    const guest = await browser.newContext({ baseURL: isolatedBaseURL, javaScriptEnabled: false })
    try {
      const home = await guest.newPage()
      for (const [lang, path] of [['en', '/'], ['zh-CN', '/zh-cn']] as const) {
        const response = await home.goto(path)
        expect(response?.status()).toBe(200)
        await expect(home).toHaveTitle(titles[lang])
        await expect(home.locator('meta[name="description"]')).toHaveAttribute('content', descriptions[lang])
        await expect(home.locator('meta[property="og:image"]')).toHaveAttribute('content', isolatedBaseURL + shareImage)
        await expect(home.locator('link[rel="canonical"]')).toHaveAttribute('href', isolatedBaseURL + path)
        await expect(home.locator('link[rel="alternate"][hreflang="en"]')).toHaveAttribute('href', isolatedBaseURL + '/')
        await expect(home.locator('link[rel="alternate"][hreflang="zh-Hans"]')).toHaveAttribute('href', isolatedBaseURL + '/zh-cn')
        await expect(home.locator('meta[name="robots"][content*="noindex"]')).toHaveCount(0)
        await expect(home.locator('script[type="application/ld+json"]')).toHaveCount(lang === 'en' ? 1 : 0)
      }
    } finally {
      await guest.close()
    }
    const after = moneySnapshot()
    await testInfo.attach('seo-save-and-readback', {
      contentType: 'application/json',
      body: JSON.stringify({ before, after, ...persisted }),
    })
    expect(after).toEqual(before)
  })
}

async function gotoPublicPage(page: Page, watch: SessionWatch, path: string) {
  const real = watch.real
  if (!real) {
    await page.goto(path)
    return
  }

  for (const attempt of [1, 2]) {
    const start = real.responses.length
    const document = await page.goto(path)
    if (document?.status() !== 429) expect(document?.status()).toBe(200)
    let throttled: Response | undefined
    let status: Response | undefined
    let site: Response | undefined
    await expect
      .poll(
        () => {
          const navigation = real.responses.slice(start)
          throttled = navigation.find((response) => response.status() === 429)
          status = navigation.find(
            (response) => new URL(response.url()).pathname === '/api/status',
          )
          site = navigation.find(
            (response) => new URL(response.url()).pathname === '/api/adty/site',
          )
          return Boolean(throttled || (status && site))
        },
        { message: 'The page loads public status and site settings from the Go app' },
      )
      .toBe(true)

    if (throttled) {
      expect(attempt, 'Public navigation is still rate limited after retry').toBe(1)
      // By default the Redis web/API windows expire in no more than 180 seconds.
      const seconds = Number(throttled.headers()['retry-after'] ?? 1)
      expect(seconds, 'Redis Retry-After in seconds').toBeGreaterThan(0)
      expect(seconds, 'Redis Retry-After within the test budget').toBeLessThanOrEqual(180)
      for (const response of real.responses.slice(start)) {
        if (response.status() === 429) real.retried.add(response)
      }
      await delay(seconds * 1000)
      continue
    }

    expect(document?.status()).toBe(200)
    expect(status?.status()).toBe(200)
    expect(site?.status()).toBe(200)
    const statusBody = await status?.json()
    const siteBody = await site?.json()
    expect(statusBody).toMatchObject({
      success: true,
      data: { system_name: 'ADTY AI', setup: true },
    })
    expect(statusBody.data.adty_site).toBeUndefined()
    expect(siteBody).toMatchObject({
      success: true,
      data: { api_base_url: expect.any(String) },
    })
    const setup = real.responses.slice(start).find(
      (response) => new URL(response.url()).pathname === '/api/setup',
    )
    if (setup) {
      expect(setup.status()).toBe(200)
      expect(await setup.json()).toMatchObject({
        success: true,
        data: { status: true },
      })
    }
    real.apiBaseURL = (
      siteBody.data.api_base_url.trim() ||
      statusBody.data.server_address?.trim() ||
      new URL(page.url()).origin
    ).replace(/\/+$/, '')
    return
  }
}

async function openAsMainSiteGuest(page: Page, watch: SessionWatch, language: 'en' | 'zhCN') {
  await page.addInitScript((stored) => {
    const key = 'i18nextLng'
    // Later navigations must keep the language the switcher wrote.
    if (!window.localStorage.getItem(key)) {
      window.localStorage.setItem(key, stored)
    }
  }, language)
  await gotoPublicPage(page, watch, '/')
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
  const box = await page.locator('main').boundingBox()
  expect(viewport).not.toBeNull()
  expect(box).not.toBeNull()
  if (viewport == null || box == null) {
    return
  }
  expect(box.width).toBeLessThanOrEqual(maxWidth)
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(viewport.width + 1)
  const fits = await page.evaluate(
    () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
  )
  expect(fits).toBe(true)
}

test('ADTY-01-04 help center entry opens the quick start', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
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

test('ADTY-01-10 public pages stay inside the viewport', async ({ page, watch }) => {
  await openAsMainSiteGuest(page, watch, 'en')

  await page.setViewportSize({ width: 1280, height: 800 })
  await gotoPublicPage(page, watch, '/docs/')
  await expect(
    page.getByRole('heading', { level: 1, name: 'Quick start' }),
  ).toBeVisible()
  await assertMainFits(page, 1180)
  await expect(page.locator('main')).toHaveCSS('padding-left', '40px')
  await expect(page.getByRole('link', { name: 'Docs', exact: true }).first()).toBeVisible()

  await page.setViewportSize({ width: 390, height: 844 })
  await gotoPublicPage(page, watch, '/docs/')
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
  watch,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
  await page.getByRole('link', { name: 'Docs', exact: true }).first().click()
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

  await gotoPublicPage(page, watch, '/zh-cn')
  await page.getByRole('link', { name: '文档', exact: true }).first().click()
  await expect(
    page.getByRole('heading', { level: 1, name: '快速开始' }),
  ).toBeVisible()
  await gotoPublicPage(page, watch, '/docs/faq')
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('lang', /zh/i)
  await expect(page.getByRole('heading', { level: 1, name: '常见问题' })).toBeVisible()
  await page.getByRole('button', { name: '更改语言' }).first().click()
  await page.getByRole('menuitem', { name: 'English' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'FAQ' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Docs', exact: true }).first()).toBeVisible()
  await expect(page).toHaveURL(/\/docs\/faq$/)
  await assertGuestOnMainSite(page, 'Sign in')
  await assertNoMoneyChange(watch)
})

test('ADTY-01-01 home shows the main-site brand to a guest', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
  await expect(page).toHaveURL(/\/$/)
  await expect(
    page.getByRole('heading', { level: 1, name: 'One endpoint. Every model.' }),
  ).toBeVisible()
  await expect(page.getByRole('link', { name: 'ADTY AI' }).first()).toBeVisible()
  await assertGuestOnMainSite(page, 'Sign in')
  await assertNoMoneyChange(watch)
})

test('ADTY-01-05 terms page shows the configured agreement', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
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

test('ADTY-01-06 privacy page shows the configured policy', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
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

async function openRoleGuide(page: Page, watch: SessionWatch) {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
  await gotoPublicPage(page, watch, '/docs/roles')
  await expect(page).toHaveURL(/\/docs\/roles$/)
}

test('ADTY-22-01 user role guide shows the wallet and key steps', async ({ page, watch }) => {
  await openRoleGuide(page, watch)
  await expect(page.getByRole('heading', { level: 2, name: 'User' })).toBeVisible()
  await expect(
    page.getByText('Create an API key and put the site URL and key in the client.'),
  ).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.locator('html')).toHaveAttribute('lang', /zh/i)
  await expect(page.getByRole('heading', { level: 2, name: '普通用户' })).toBeVisible()
  await expect(page.getByText('创建 API Key 后，把站点地址和 Key 配到客户端即可调用。')).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-02 team lead guide shows invite and shared balance', async ({ page, watch }) => {
  await openRoleGuide(page, watch)
  await expect(page.getByRole('heading', { level: 2, name: 'Team lead' })).toBeVisible()
  await expect(page.getByText('Only Root can create a team and appoint its lead.')).toBeVisible()
  await expect(
    page.getByText('The lead can invite members and authorize them to use team API keys.'),
  ).toBeVisible()
  await expect(
    page.getByText("Usage of those keys is charged to the team's shared balance."),
  ).toBeVisible()
  await expect(
    page.getByText('If the lead leaves the team or closes their account, the team enters the closure process.'),
  ).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 2, name: '团队负责人' })).toBeVisible()
  await expect(page.getByText('只有 Root 可以创建团队并指定负责人。')).toBeVisible()
  await expect(page.getByText('负责人可以邀请成员并授权成员使用团队 API Key。')).toBeVisible()
  await expect(page.getByText('这些 Key 产生的费用从团队共享余额扣除。')).toBeVisible()
  await expect(page.getByText('负责人退出团队或注销账号时，团队进入关闭流程。')).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-03 team member guide shows one-team membership', async ({ page, watch }) => {
  await openRoleGuide(page, watch)
  await expect(page.getByRole('heading', { level: 2, name: 'Team member' })).toBeVisible()
  await expect(page.getByText('A person can belong to only one team.')).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 2, name: '团队成员' })).toBeVisible()
  if (watch.real) {
    await expect(
      page.getByText('退出后团队 Key 不再扣你的个人余额'),
      'A.9.3 charges the shared balance throughout team membership',
    ).toHaveCount(0)
  }
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-04 site owner guide shows earnings and closing', async ({ page, watch }) => {
  await openRoleGuide(page, watch)
  await expect(page.getByRole('heading', { level: 2, name: 'Site owner' })).toBeVisible()
  await expect(page.getByText('Earnings go to the earnings wallet.')).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 2, name: '分站站长' })).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-05 site staff guide shows approval before a balance change', async ({ page, watch }) => {
  await openRoleGuide(page, watch)
  await expect(page.getByRole('heading', { level: 2, name: 'Site staff' })).toBeVisible()
  await expect(
    page.getByText('A balance change is a request until the owner or a main-site admin approves it.'),
  ).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 2, name: '分站员工' })).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-06 main-site admin guide shows ledgered adjustments', async ({ page, watch }) => {
  await openRoleGuide(page, watch)
  await expect(page.getByRole('heading', { level: 2, name: 'Main-site admin' })).toBeVisible()
  await expect(page.getByText('Do not edit a balance by hand.')).toBeVisible()
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 2, name: '主站管理员' })).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-07 API guide shows the OpenAI-compatible base URL', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
  await gotoPublicPage(page, watch, '/docs/openai-sdk')
  await expect(page).toHaveURL(/\/docs\/openai-sdk$/)
  await expect(page.getByRole('heading', { level: 1, name: 'OpenAI SDK' })).toBeVisible()
  await expect(page.getByText('Any OpenAI SDK works')).toBeVisible()
  const configuredApiBase = process.env.ADTY_BROWSER_DOCS_API_BASE_URL
  if (watch.real) {
    expect(configuredApiBase, 'Seed the isolated application with a custom API base URL').toBeTruthy()
    expect(watch.real.apiBaseURL).toBe(configuredApiBase)
    await expect(page.getByText(`${watch.real.apiBaseURL}/v1`, { exact: true })).toBeVisible()
  }
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'OpenAI SDK' })).toBeVisible()
  await expect(page.getByText('任何 OpenAI SDK 都能用')).toBeVisible()
  if (watch.real) {
    expect(watch.real.apiBaseURL).toBe(configuredApiBase)
    await expect(page.getByText(`${watch.real.apiBaseURL}/v1`, { exact: true })).toBeVisible()
  }
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-08 model guide shows alias routing without the channel name', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
  await gotoPublicPage(page, watch, '/docs/models')
  await expect(page).toHaveURL(/\/docs\/models$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Models and groups' })).toBeVisible()
  await expect(page.getByText('Customer logs do not show the upstream channel name.')).toBeVisible()
  if (watch.real) {
    await expect(page.getByText('Send the alias or upstream name')).toBeVisible()
    await expect(page.getByText('the routing group comes from the authenticated API key')).toBeVisible()
  }
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 1, name: '模型与分组' })).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})

test('ADTY-22-09 agent guide shows the tool-call loop', async ({ page, watch }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openAsMainSiteGuest(page, watch, 'en')
  await gotoPublicPage(page, watch, '/docs/agents')
  await expect(page).toHaveURL(/\/docs\/agents$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Agents' })).toBeVisible()
  await expect(page.getByText('Your program owns the agent loop')).toBeVisible()
  if (watch.real) {
    await expect(page.getByText('you send the tool result back on the next request')).toBeVisible()
    await expect(page.locator('article pre code').filter({ hasText: 'tools=[' })).toBeVisible()
  }
  await page.getByRole('button', { name: 'Change language' }).first().click()
  await page.getByRole('menuitem', { name: '简体中文' }).click()
  await expect(page.getByRole('heading', { level: 1, name: '智能体' })).toBeVisible()
  await assertGuestOnMainSite(page, '登录')
  await assertNoMoneyChange(watch)
})
