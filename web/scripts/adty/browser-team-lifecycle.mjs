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
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

import { chromium } from 'playwright'

const base = process.env.ADTY_BROWSER_BASE_URL
assert.ok(base && process.env.ADTY_BROWSER_SQL_DSN)
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(base).hostname))
const width = Number(process.env.ADTY_BROWSER_TEAM_WIDTH)
assert.ok([1440, 390].includes(width))
const browser = await chromium.launch({
  headless: true,
  ...(process.env.ADTY_CHROMIUM_PATH
    ? { executablePath: process.env.ADTY_CHROMIUM_PATH }
    : {}),
})
const errors = []
const sessions = []
async function login(username, password, width = 1440) {
  const context = await browser.newContext({
    locale: 'en-US', viewport: { width, height: 1000 },
  })
  sessions.push(context)
  const page = await context.newPage()
  const state = { context, page, authorization: '' }
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('request', (request) => {
    if (request.headers().authorization) {
      state.authorization = request.headers().authorization
    }
  })
  await page.goto(`${base}/sign-in`)
  await page.getByLabel('Username or Email').fill(username)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
  assert.ok(state.authorization)
  return state
}
async function api(state, method, path, data) {
  const response = await state.page.request[method](base + path, {
    headers: { authorization: state.authorization },
    ...(data === undefined ? {} : { data }),
  })
  assert.equal(response.status(), 200, path)
  const body = await response.json()
  assert.equal(body.success, true, body.message || path)
  return body.data
}
function postgres(query) {
  return execFileSync('psql', [process.env.ADTY_BROWSER_SQL_DSN, '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', query],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
}
async function capture(page, name, width) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'horizontal overflow')
  const output = process.env.ADTY_BROWSER_ARTIFACT_DIR
  if (output) {
    await mkdir(output, { recursive: true })
    await page.screenshot({ path: join(output, `team-${name}-${width}.png`), fullPage: true })
  }
}
try {
  const root = await login(process.env.ADTY_BROWSER_USERNAME, process.env.ADTY_BROWSER_PASSWORD)
  {
    const leaderName = `teamlead${width}`
    const leaderPassword = `LocalGate!${randomBytes(12).toString('hex')}`
    await api(root, 'post', '/api/user/', { username: leaderName, password: leaderPassword, role: 1 })
    const leaderID = Number(postgres(`SELECT id FROM users WHERE username='${leaderName}'`))
    assert.ok(leaderID > 0)
    const created = await api(root, 'post', '/api/adty/admin/teams', { name: `Lifecycle ${width}`, leader_id: leaderID })
    const teamID = created.Team.id
    assert.ok(teamID > 0)
    const leader = await login(leaderName, leaderPassword, width)
    await leader.page.goto(`${base}/wallet`)
    await leader.page.getByLabel('Employee username').waitFor()
    const employeeName = `employee${width}`
    const employeePassword = `Employee!${randomBytes(12).toString('hex')}`
    await leader.page.getByLabel('Employee username').fill(employeeName)
    await leader.page.getByLabel('Employee password').fill(employeePassword)
    const [creation] = await Promise.all([
      leader.page.waitForResponse((r) => new URL(r.url()).pathname === `/api/adty/teams/${teamID}/employees` && r.request().method() === 'POST'),
      leader.page.getByRole('button', { name: 'Create employee', exact: true }).click(),
    ])
    assert.equal((await creation.json()).success, true)
    await leader.page.getByRole('button', { name: 'Disable member', exact: true }).waitFor()
    assert.equal(await leader.page.getByLabel('Employee password').inputValue(), '')
    await capture(leader.page, 'employees', width)
    await leader.page.getByRole('button', { name: 'Disable member', exact: true }).click()
    await leader.page.getByRole('button', { name: 'Enable employee', exact: true }).waitFor()
    await leader.page.getByRole('button', { name: 'Enable employee', exact: true }).click()
    await leader.page.getByRole('button', { name: 'Disable member', exact: true }).waitFor()
    await leader.page.getByRole('button', { name: 'Delete employee', exact: true }).click()
    const confirm = leader.page.getByRole('alertdialog', { name: 'Delete employee', exact: true })
    await confirm.getByRole('button', { name: 'Confirm delete', exact: true }).click()
    await leader.page.getByRole('button', { name: 'Delete employee', exact: true }).waitFor({ state: 'hidden' })
    await leader.page.getByRole('button', { name: 'Create invite', exact: true }).click()
    const registrationLink = leader.page.getByLabel('Employee registration link', { exact: true })
    await registrationLink.waitFor()
    const registrationURL = await registrationLink.inputValue()
    assert.equal(new URL(registrationURL).searchParams.get('kind'), 'employee')
    const guestContext = await browser.newContext({ locale: 'en-US', viewport: { width, height: 1000 } })
    sessions.push(guestContext)
    const guest = await guestContext.newPage()
    guest.on('pageerror', (error) => errors.push(error.message))
    await guest.goto(registrationURL)
    await guest.getByRole('heading', { name: 'Register employee', exact: true }).waitFor()
    assert.equal(await guest.getByLabel('Team name', { exact: true }).count(), 0)
    await capture(guest, 'registration', width)
    await guest.getByLabel('Username', { exact: true }).fill(`invited${width}`)
    await guest.getByLabel('Password', { exact: true }).fill(employeePassword)
    await guest.getByLabel('Confirm password', { exact: true }).fill(employeePassword)
    await guest.getByRole('button', { name: 'Register employee', exact: true }).click()
    await guest.getByRole('link', { name: 'Sign in', exact: true }).waitFor()
    const registered = postgres(`SELECT count(*) FROM adty_team_members m JOIN users u ON u.id=m.user_id WHERE m.team_id=${teamID} AND m.independent_employee AND u.username='invited${width}' AND u.quota=0 AND u.staff_site_id=0`)
    assert.equal(registered, '1')
    await api(leader, 'post', `/api/adty/teams/${teamID}/leave`)
    assert.equal(postgres(`SELECT status || ':' || shared_quota FROM adty_teams WHERE id=${teamID}`), 'closed:0')
    console.log(`PASS team lifecycle ${width}: leader creation, employee create/disable/enable/delete, anonymous invitation registration, identity and closure retained`)
  }
  assert.deepEqual(errors, [])
} catch (error) {
  if (process.env.ADTY_BROWSER_ARTIFACT_DIR) {
    await mkdir(process.env.ADTY_BROWSER_ARTIFACT_DIR, { recursive: true })
    for (const [i, context] of sessions.entries()) {
      for (const [j, page] of context.pages().entries()) {
        await page.screenshot({ path: join(process.env.ADTY_BROWSER_ARTIFACT_DIR, `team-failure-${width}-${i}-${j}.png`), fullPage: true }).catch(() => {})
      }
    }
  }
  throw error
} finally {
  for (const context of sessions) await context.close()
  await browser.close()
}
