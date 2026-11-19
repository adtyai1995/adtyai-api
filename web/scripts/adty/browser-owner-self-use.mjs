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
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'

import { chromium } from 'playwright'

import {
  configureBrowserEpayChannel,
  startBrowserEpayGateway,
} from './browser-epay-gateway.mjs'

const base = process.env.ADTY_BROWSER_BASE_URL
assert.ok(base && process.env.ADTY_BROWSER_SQL_DSN)
assert.ok(['localhost', '127.0.0.1', '[::1]'].includes(new URL(base).hostname))

function postgres(query) {
  return execFileSync(
    'psql',
    [
      process.env.ADTY_BROWSER_SQL_DSN,
      '-XAt',
      '-v',
      'ON_ERROR_STOP=1',
      '-c',
      query,
    ],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }
  ).trim()
}

const browser = await chromium.launch({
  headless: true,
  ...(process.env.ADTY_CHROMIUM_PATH
    ? { executablePath: process.env.ADTY_CHROMIUM_PATH }
    : {}),
})
const errors = []
const gateway = await startBrowserEpayGateway()
const business = 'owner_self_use_topup'
const sceneKey = 'browser_owner_checkout'

async function session(url, username, password, width = 1440) {
  const context = await browser.newContext({
    locale: 'en-US',
    viewport: { width, height: 1000 },
  })
  const page = await context.newPage()
  const state = { page, authorization: '' }
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('request', (request) => {
    if (request.headers().authorization) {
      state.authorization = request.headers().authorization
    }
  })
  await page.goto(`${url}/sign-in`)
  await page.getByLabel('Username or Email').fill(username)
  await page.getByLabel('Password', { exact: true }).fill(password)
  const loggedIn = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/user/login' &&
      response.request().method() === 'POST'
  )
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  const login = await (await loggedIn).json()
  assert.equal(login.success, true, login.message)
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
  assert.ok(state.authorization)
  return state
}

async function api(state, method, path, data, headers = {}) {
  const response = await state.page.request[method](base + path, {
    headers: { authorization: state.authorization, ...headers },
    ...(data === undefined ? {} : { data }),
  })
  assert.equal(response.status(), 200, path)
  const body = await response.json()
  assert.equal(body.success, true, body.message || path)
  return body.data
}

async function createOwnerScene(root) {
  const channel = await configureBrowserEpayChannel(
    (...args) => api(root, ...args),
    base,
    gateway
  )
  const page = root.page
  await page.goto(`${base}/system-settings/billing/payment?tab=general`)
  await page
    .getByRole('button', { name: 'New payment scene', exact: true })
    .click()
  const editor = page.getByRole('dialog', { name: 'New payment scene' })
  await editor.getByLabel('Scene identifier').fill(sceneKey)
  await editor.getByLabel('Name', { exact: true }).fill('Owner checkout')
  await editor.getByLabel('Business', { exact: true }).selectOption(business)
  assert.equal(await editor.getByLabel('Site scope').inputValue(), 'all_sites')
  assert.equal(await editor.getByLabel('User types').isDisabled(), true)
  await editor.getByRole('switch', { name: 'Enabled' }).click()
  await editor.getByLabel('Payment providers').click()
  await page.getByRole('option', { name: 'Epay channels', exact: true }).click()
  await page.keyboard.press('Escape')
  await editor.getByLabel('Allowed Epay channels').click()
  await page.getByRole('option', { name: /Scene browser channel/ }).click()
  await page.keyboard.press('Escape')
  await editor.getByRole('button', { name: 'Save payment scene' }).click()
  const saved = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/adty/admin/payment-scenes' &&
      response.request().method() === 'PUT'
  )
  await page
    .getByRole('alertdialog')
    .getByRole('button', { name: 'Continue' })
    .click()
  const result = await (await saved).json()
  assert.equal(result.success, true, result.message)
  const scene = result.data.scenes.find((item) => item.key === sceneKey)
  assert.equal(scene.business, business)
  assert.deepEqual(scene.user_types, ['site_owner'])
  assert.deepEqual(scene.channel_ids, [channel.id])
  return channel.id
}

async function payOwnerPrincipal(page, siteBase, channelId) {
  const scene = page.getByLabel('Payment scene', { exact: true })
  await scene.waitFor()
  assert.deepEqual(await scene.locator('option').allTextContents(), [
    'Owner checkout',
  ])
  await page.getByLabel('Custom amount (USD)').fill('100')
  const quote = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/adty/topup/quote'
  )
  await page.getByRole('button', { name: 'Alipay', exact: true }).click()
  const quoted = await quote
  const common = {
    amount: 100,
    method: 'alipay',
    business,
    payment_scene: sceneKey,
    channel_id: channelId,
  }
  assert.deepEqual(quoted.request().postDataJSON(), common)
  assert.equal((await quoted.json()).data.quote.credit_quota, 50000000)
  const before = gateway.calls
  const placed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/adty/epay/pay'
  )
  await page.getByRole('button', { name: 'Pay now', exact: true }).click()
  const checkout = await placed
  assert.deepEqual(checkout.request().postDataJSON(), {
    ...common,
    device: 'pc',
  })
  const result = await checkout.json()
  assert.equal(result.success, true, result.message)
  const { trade_no, ext } = result.data
  assert.equal(ext.business, business)
  assert.equal(ext.bonus_usd, '0')
  const record = gateway.orders.get(trade_no)
  assert.ok(record)
  const params = {
    pid: gateway.merchant,
    type: 'alipay',
    out_trade_no: trade_no,
    trade_no: record.gateway_id,
    trade_status: 'TRADE_SUCCESS',
    money: record.money,
    name: record.name,
  }
  const callback = await page.request.post(
    new URL(`/api/user/epay/notify/${channelId}`, base).href,
    { form: { ...params, sign: gateway.sign(params), sign_type: 'MD5' } }
  )
  assert.equal(await callback.text(), 'success')
  assert.equal(new URL(record.return_url).origin, new URL(base).origin)
  const redirect = await page.request.get(record.return_url, {
    maxRedirects: 0,
  })
  assert.equal(redirect.status(), 302)
  const target = new URL(redirect.headers().location)
  assert.equal(target.hostname, siteBase.hostname)
  assert.equal(target.pathname, '/site-owner/earnings')
  assert.equal(target.searchParams.get('trade_no'), trade_no)
  // Production hostnames use the default HTTP(S) port. The harness uses an ephemeral port.
  target.port = siteBase.port
  await page.goto(target.href)
  const dialog = page.getByRole('dialog', { name: 'Pay with Alipay' })
  await dialog.getByText('Credited $100.00', { exact: true }).waitFor()
  await page.keyboard.press('Escape')
  await page.waitForURL((url) => !url.searchParams.has('trade_no'))
  assert.equal(gateway.calls, before + 1)
  await page
    .getByRole('table', { name: 'Top-up history' })
    .getByText(trade_no, { exact: true })
    .waitFor()
  assert.match(
    await page
      .getByLabel('Available site self-use principal', { exact: true })
      .innerText(),
    /120.00/
  )
}

try {
  const root = await session(
    base,
    process.env.ADTY_BROWSER_USERNAME,
    process.env.ADTY_BROWSER_PASSWORD
  )
  await api(root, 'put', '/api/adty/admin/reseller/settings', {
    enabled: true,
    root_domains: ['sites.localhost'],
    cost_factor: 0.8,
    min_cost_factor: 0.5,
    max_markup: 3,
    trial_grant_max_usd: 0,
    trial_grant_daily_usd: 0,
  })
  let channelId
  for (const width of [1440, 390]) {
    const username = `selfowner${width}`
    const password = `LocalOwner!${randomBytes(12).toString('hex')}`
    await api(root, 'post', '/api/user/', { username, password, role: 1 })
    const ownerID = Number(
      postgres(`SELECT id FROM users WHERE username = '${username}'`)
    )
    const site = await api(root, 'post', '/api/adty/admin/sites', {
      user_id: ownerID,
      subdomain: username,
      root_domain: 'sites.localhost',
    })
    assert.ok(Number.isSafeInteger(site.id) && site.id > 0)
    // Opening balances are fixtures; transfers and key creation use the production UI.
    postgres(
      `UPDATE adty_sites SET agreement_ver = '0' WHERE id = ${site.id}; UPDATE users SET quota = 0, quota_pending = 0 WHERE id = ${ownerID}; UPDATE adty_site_wallets SET balance = 100, frozen_usd = 10 WHERE site_id = ${site.id} AND kind = 'earnings'`
    )
    const host = `${username}.sites.localhost`
    const siteBase = new URL(base)
    siteBase.hostname = host
    const owner = await session(siteBase.origin, username, password, width)
    const page = owner.page
    await page.goto(`${siteBase.origin}/site-owner/earnings`)
    const amount = page.getByLabel('Transfer to personal balance (USD)', {
      exact: true,
    })
    await amount.waitFor()
    const transferButton = page.getByRole('button', {
      name: 'Transfer to personal balance',
      exact: true,
    })
    await amount.fill('91')
    assert.equal(await transferButton.isDisabled(), true)
    await amount.fill('20')
    await transferButton.click()
    const dialog = page.getByRole('alertdialog', {
      name: 'Transfer to personal balance',
      exact: true,
    })
    assert.match(await dialog.innerText(), /70.00/)
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    assert.equal(
      Number(
        postgres(
          `SELECT balance FROM adty_site_wallets WHERE site_id = ${site.id} AND kind = 'earnings'`
        )
      ),
      100
    )
    await transferButton.click()
    const transferred = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname ===
          '/api/adty/site/wallets/earning-to-personal' &&
        response.request().method() === 'POST'
    )
    await dialog.getByRole('button', { name: 'Continue', exact: true }).click()
    const response = await transferred
    const receipt = await response.json()
    assert.equal(receipt.success, true, receipt.message)
    await page
      .getByRole('table', { name: 'Owner principal transfers', exact: true })
      .getByRole('row')
      .filter({ hasText: receipt.data.transfer_id })
      .waitFor()
    assert.match(
      await page
        .getByLabel('Available site self-use principal', { exact: true })
        .innerText(),
      /20.00/
    )
    assert.equal(
      Number(
        postgres(
          `SELECT balance FROM adty_site_wallets WHERE site_id = ${site.id} AND kind = 'earnings'`
        )
      ),
      80
    )
    assert.equal(
      Number(postgres(`SELECT quota FROM users WHERE id = ${ownerID}`)),
      10000000
    )
    const intent = response.request().headers()['idempotency-key']
    assert.ok(intent)
    const replay = await api(
      owner,
      'post',
      '/api/adty/site/wallets/earning-to-personal',
      { amount_usd: '20' },
      { host, 'Idempotency-Key': intent }
    )
    assert.equal(replay.transfer_id, receipt.data.transfer_id)
    assert.equal(
      Number(postgres(`SELECT quota FROM users WHERE id = ${ownerID}`)),
      10000000
    )
    const created = page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === '/api/adty/site/self-use/keys' &&
        candidate.request().method() === 'POST'
    )
    await page
      .getByRole('button', { name: 'Create site self-use key', exact: true })
      .click()
    const keyResponse = await created
    const key = await keyResponse.json()
    assert.equal(key.success, true, key.message)
    assert.equal(keyResponse.headers()['cache-control'], 'no-store')
    await page.getByText(key.data.key, { exact: true }).waitFor()
    assert.equal(
      Number(
        postgres(
          `SELECT owner_self_use_site_id FROM tokens WHERE id = ${Number(key.data.id)}`
        )
      ),
      site.id
    )
    await page.getByRole('button', { name: 'Close', exact: true }).click()
    assert.equal(await page.getByText(key.data.key, { exact: true }).count(), 0)
    if (!channelId) {
      await page
        .getByText('Online top-up is unavailable for this site.')
        .waitFor()
      channelId = await createOwnerScene(root)
      await page.reload()
    }
    await payOwnerPrincipal(page, siteBase, channelId)
    const geometry = await page.evaluate(() => ({
      width: document.documentElement.clientWidth,
      scroll: document.documentElement.scrollWidth,
    }))
    assert.ok(
      geometry.scroll <= geometry.width + 1,
      `Owner page overflows at ${width}px`
    )
    console.log(
      `owner self-use: ${width}px transfer, replay, key, online top-up, signed return, principal, history and layout passed`
    )
    await page.context().close()
  }
  assert.deepEqual(errors, [])
} finally {
  await browser.close()
  await gateway.close()
}
