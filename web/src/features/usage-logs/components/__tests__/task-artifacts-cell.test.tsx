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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { TaskLog } from '../../types'
import { TaskArtifactsCell } from '../task-artifacts'

if (typeof Element !== 'undefined' && !Element.prototype.getAnimations) {
  Element.prototype.getAnimations = () => []
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function taskLog(overrides: Partial<TaskLog> = {}): TaskLog {
  return {
    id: 1,
    user_id: 7,
    platform: 'alibaba',
    task_id: 'task-public',
    action: 'text_to_image',
    channel_id: 357,
    group: 'default',
    quota: 25000,
    submit_time: 1,
    status: 'SUCCESS',
    admin_info: {
      task_plugin: {
        key: 'alibaba',
        name: 'Alibaba Bailian',
        version: '1.4.0',
      },
    },
    ...overrides,
  }
}

function renderCell(log: TaskLog) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <TaskArtifactsCell log={log} />
    </QueryClientProvider>
  )
}

describe('task artifacts cell', () => {
  test('given a synchronous result the gateway did not retain, a hint replaces the artifacts button', () => {
    renderCell(taskLog({ result_discarded: true }))

    expect(screen.getByText('Result not retained')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /Artifacts/ })
    ).not.toBeInTheDocument()
  })

  test('given a retained successful plugin task, the artifacts button is offered', () => {
    renderCell(taskLog())

    expect(screen.getByRole('button', { name: /Artifacts/ })).toBeVisible()
    expect(screen.queryByText('Result not retained')).not.toBeInTheDocument()
  })

  test('given an unfinished task, neither the button nor the hint is shown', () => {
    renderCell(taskLog({ status: 'IN_PROGRESS', result_discarded: true }))

    expect(screen.getByText('-')).toBeVisible()
    expect(screen.queryByText('Result not retained')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /Artifacts/ })
    ).not.toBeInTheDocument()
  })

  test('renders retryable ErrorState instead of None when legacy Suno audio preview query fails', async () => {
    let shouldFail = true
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url.includes('/artifacts')) {
        if (shouldFail) {
          throw new Error('artifact projection failed')
        }
        return {
          data: {
            success: true,
            data: {
              artifacts: [],
              legacy_audio_clips: [
                {
                  id: 'clip-1',
                  title: 'Suno Song',
                  audio_url: 'https://example.com/song.mp3',
                },
              ],
            },
          },
        }
      }
      throw new Error(`Unexpected GET ${url}`)
    })

    renderCell(
      taskLog({
        platform: 'suno',
        action: 'MUSIC',
        admin_info: undefined,
      })
    )

    fireEvent.click(
      screen.getByRole('button', { name: 'Click to preview audio' })
    )

    const retryButton = await screen.findByRole('button', { name: 'Retry' })
    expect(screen.getByText('Failed to load artifacts')).toBeVisible()
    expect(screen.queryByText('None')).not.toBeInTheDocument()

    shouldFail = false
    fireEvent.click(retryButton)
    await waitFor(() => expect(screen.getByText('Suno Song')).toBeVisible())
  })
})
