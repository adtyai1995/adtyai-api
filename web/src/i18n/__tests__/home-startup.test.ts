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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import i18n, { whenInterfaceLanguageReady } from '@/i18n/config'

beforeEach(() => {
  window.localStorage.clear()
  document.cookie = 'adtyai_has_session=; Max-Age=0; Path=/'
  vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('zh-CN')
  i18n.removeResourceBundle('en', 'translation')
  i18n.removeResourceBundle('zhCN', 'translation')
})

afterEach(() => {
  window.localStorage.clear()
  window.history.replaceState(null, '', '/')
  document.cookie = 'adtyai_has_session=; Max-Age=0; Path=/'
})

test('an anonymous root visit loads only English despite a Chinese browser', async () => {
  window.history.replaceState(null, '', '/')
  await whenInterfaceLanguageReady()
  expect(i18n.language).toBe('en')
  expect(i18n.hasResourceBundle('zhCN', 'translation')).toBe(false)
})

test('an explicit Chinese home loads only Chinese despite a saved English interface', async () => {
  window.history.replaceState(null, '', '/zh-cn/')
  window.localStorage.setItem('i18nextLng', 'en')
  await whenInterfaceLanguageReady()
  expect(i18n.language).toBe('zhCN')
  expect(i18n.hasResourceBundle('en', 'translation')).toBe(false)
})

test('a remembered Chinese home overrides the saved English interface on root', async () => {
  window.history.replaceState(null, '', '/')
  window.localStorage.setItem('adtyai:lang', 'zhCN')
  window.localStorage.setItem('i18nextLng', 'en')
  await whenInterfaceLanguageReady()
  expect(i18n.language).toBe('zhCN')
  expect(i18n.hasResourceBundle('en', 'translation')).toBe(false)
})

test('a returning session keeps its saved interface while authentication resolves', async () => {
  window.history.replaceState(null, '', '/')
  document.cookie = 'adtyai_has_session=1; Path=/'
  window.localStorage.setItem('i18nextLng', 'zhCN')
  await whenInterfaceLanguageReady()
  expect(i18n.language).toBe('zhCN')
})

test('an anonymous root still selects English when browser storage is blocked', async () => {
  window.history.replaceState(null, '', '/')
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new DOMException('Blocked', 'SecurityError')
  })
  await whenInterfaceLanguageReady()
  expect(i18n.language).toBe('en')
})

test('a non-home route still uses the browser language', async () => {
  window.history.replaceState(null, '', '/sign-in')
  await whenInterfaceLanguageReady()
  expect(i18n.language).toBe('zhCN')
})
