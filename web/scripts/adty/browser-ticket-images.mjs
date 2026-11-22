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
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

import { chromium } from 'playwright'

const base = process.env.ADTY_BROWSER_BASE_URL
assert.ok(base && process.env.ADTY_BROWSER_SQL_DSN)
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname))
const browser = await chromium.launch({
  headless: true,
  ...(process.env.ADTY_CHROMIUM_PATH
    ? { executablePath: process.env.ADTY_CHROMIUM_PATH }
    : {}),
})
const context = await browser.newContext({ locale: 'en-US', viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
const errors = []
let authorization = ''
const imageRequests = []
page.on('pageerror', (error) => errors.push(error.message))
page.on('request', (request) => {
  const headers = request.headers()
  if (headers.authorization) {
    authorization = headers.authorization
  }
  if (new RegExp('/tickets/[0-9]+/images/[0-9]+$').test(new URL(request.url()).pathname)) {
    imageRequests.push({ url: new URL(request.url()), headers })
  }
})
async function api(method, path, data) {
  const response = await page.request[method](base + path, {
    headers: { authorization, 'Idempotency-Key': `ticket-images-${path}-${imageRequests.length}` },
    ...(data === undefined ? {} : { data }),
  })
  assert.equal(response.status(), 200)
  const body = await response.json()
  assert.equal(body.success, true, body.message)
  return body.data
}
try {
  await page.goto(`${base}/sign-in`)
  await page.getByLabel('Username or Email').fill(process.env.ADTY_BROWSER_USERNAME)
  await page.getByLabel('Password', { exact: true }).fill(process.env.ADTY_BROWSER_PASSWORD)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
  assert.ok(authorization)
  const ticket = await api('post', '/api/adty/tickets', {
    type: 'other', title: 'Image authorization receipt', body: 'Receipt preview verification',
  })
  const receipt = await page.evaluate(() => {
    const canvas = document.createElement('canvas')
    canvas.width = 320
    canvas.height = 180
    const painter = canvas.getContext('2d')
    painter.fillStyle = '#ffffff'
    painter.fillRect(0, 0, 320, 180)
    painter.fillStyle = '#202020'
    painter.font = '20px sans-serif'
    painter.fillText('Receipt #42', 24, 48)
    painter.fillText('Local image fixture', 24, 92)
    return canvas.toDataURL('image/png').split(',')[1]
  })
  const image = await api('post', `/api/adty/tickets/${ticket.id}/images`, {
    visibility: 'public',
    data: receipt,
  })
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 })
    imageRequests.length = 0
    await page.goto(`${base}/profile`)
    await page.getByRole('button', { name: /Image authorization receipt/ }).click()
    const preview = page.getByRole('img', { name: new RegExp(image.object_id) })
    await preview.waitFor()
    await preview.evaluate((element) => element.decode())
    assert.equal(await preview.evaluate((element) => element.naturalWidth), 320)
    assert.ok((await preview.getAttribute('src')).startsWith('blob:'))
    assert.equal(imageRequests.filter((request) => request.url.searchParams.get('action') === 'grant').length, 0)
    assert.ok(imageRequests.some((request) => request.headers['x-ticket-image-grant']))
    assert.ok(imageRequests.every((request) => request.headers.authorization && !request.url.searchParams.has('grant')))
    await preview.scrollIntoViewIfNeeded()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    if (process.env.ADTY_BROWSER_ARTIFACT_DIR) {
      await mkdir(process.env.ADTY_BROWSER_ARTIFACT_DIR, { recursive: true })
      await page.screenshot({ path: join(process.env.ADTY_BROWSER_ARTIFACT_DIR, `ticket-image-${width}.png`), fullPage: true })
    }
    console.log(`PASS ticket image ${width}: real identity, scoped grant, decoded private preview, no URL credential or horizontal overflow`)
  }
  const audit = execFileSync('psql', [process.env.ADTY_BROWSER_SQL_DSN, '-XAt', '-v', 'ON_ERROR_STOP=1',
    '-c', `SELECT count(*) FROM adty_ticket_events WHERE ticket_id=${Number(ticket.id)} AND action='image_view'`],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  assert.ok(Number(audit) >= 2)
  assert.deepEqual(errors, [])
} finally {
  await context.close()
  await browser.close()
}
