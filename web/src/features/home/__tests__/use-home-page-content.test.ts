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
import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useHomePageContent } from '../hooks/use-home-page-content'

const getHomePageContent = vi.hoisted(() => vi.fn())

vi.mock('../api', () => ({
  getHomePageContent,
}))

describe('useHomePageContent', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    delete document.documentElement.dataset.adtySiteId
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('does not show a previous custom homepage if a subsite content request fails', async () => {
    document.documentElement.dataset.adtySiteId = '42'
    localStorage.setItem('home_page_content', 'Previous tenant')
    getHomePageContent.mockRejectedValueOnce(new Error('offline'))
    const { result } = renderHook(() => useHomePageContent())
    await waitFor(() => expect(result.current.isLoaded).toBe(true))
    expect(result.current.content).toBe('')
  })

  it('fetches home page content and marks isLoaded=true even when localStorage throws SecurityError', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('Access is denied', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('QuotaExceededError', 'QuotaExceededError')
    })
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new DOMException('Access is denied', 'SecurityError')
    })

    getHomePageContent.mockResolvedValueOnce({
      success: true,
      data: '# Welcome to AdtyAI',
    })

    const { result } = renderHook(() => useHomePageContent())

    await waitFor(() => expect(result.current.isLoaded).toBe(true))
    expect(getHomePageContent).toHaveBeenCalledTimes(1)
    expect(result.current.content).toBe('# Welcome to AdtyAI')
    expect(result.current.isUrl).toBe(false)
  })
})
