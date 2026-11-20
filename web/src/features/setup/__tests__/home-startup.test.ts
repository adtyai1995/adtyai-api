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
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

beforeEach(() => {
  vi.resetModules()
  document.head
    .querySelectorAll('meta[name="adty-setup-complete"]')
    .forEach((node) => node.remove())
  document.cookie = 'adtyai_has_session=; Max-Age=0; Path=/'
})
afterEach(() => {
  vi.restoreAllMocks()
  document.head
    .querySelectorAll('meta[name="adty-setup-complete"]')
    .forEach((node) => node.remove())
})

async function visit(pathname: string, marker: string | null, complete = true) {
  if (marker !== null) {
    const meta = document.createElement('meta')
    meta.name = 'adty-setup-complete'
    meta.content = marker
    document.head.append(meta)
  }
  const { api } = await import('@/lib/api')
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: { status: complete } } })
  const { Route } = await import('@/routes/__root')
  const beforeLoad = Route.options.beforeLoad as (context: {
    location: { href: string; pathname: string }
  }) => Promise<unknown>
  return { get, result: beforeLoad({ location: { href: pathname, pathname } }) }
}

it.each(['/', '/zh-cn'])(
  'a server-confirmed installed home %s proceeds without a setup request',
  async (path) => {
    const { get, result } = await visit(path, 'true')
    await result
    expect(get).not.toHaveBeenCalled()
  }
)
it.each([null, 'false'])(
  'a home with marker %s still redirects an uninstalled instance to setup',
  async (marker) => {
    const { get, result } = await visit('/', marker, false)
    await expect(result).rejects.toMatchObject({ options: { to: '/setup' } })
    expect(get).toHaveBeenCalledWith('/api/setup', expect.anything())
  }
)
it('a non-home entry checks setup even if a home marker is present', async () => {
  const { get, result } = await visit('/sign-in', 'true')
  await result
  expect(get).toHaveBeenCalledWith('/api/setup', expect.anything())
})
