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
// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest'
import { cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it } from 'vitest'

import { useRemovePrerenderOnMount } from '../prerender-cleanup'

beforeEach(() => {
  document.body.innerHTML =
    '<div id="adty-prerender"><a href="/sign-up">Register</a></div><div id="root"></div>'
})

afterEach(() => {
  cleanup()
  document.head.querySelectorAll('template').forEach((tag) => tag.remove())
  document.body.replaceChildren()
})

it('keeps the signup link until the final stylesheet is ready', () => {
  const pending = document.createElement('template')
  pending.dataset.adtyHomeStyle = ''
  document.head.append(pending)
  renderHook(useRemovePrerenderOnMount)
  expect(document.querySelector('a[href="/sign-up"]')).toBeInTheDocument()

  pending.remove()
  document.dispatchEvent(new Event('adty-home-styles-ready'))
  expect(document.querySelector('#adty-prerender')).not.toBeInTheDocument()
})

it('replaces the prerender immediately when styles are already ready', () => {
  renderHook(useRemovePrerenderOnMount)
  expect(document.querySelector('#adty-prerender')).not.toBeInTheDocument()
})
