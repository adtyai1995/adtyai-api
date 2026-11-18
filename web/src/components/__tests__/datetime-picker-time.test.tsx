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
import { act, fireEvent, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { DateTimePicker } from '../datetime-picker'
import { StatusBadge } from '../status-badge'

test('shows the seconds of the selected time', () => {
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 30, 45)}
      onChange={() => undefined}
    />
  )
  expect(screen.getByLabelText('Time')).toHaveValue('08:30:45')
})

test('keeps the seconds typed into the time field', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 0, 0)}
      onChange={onChange}
    />
  )
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '08:30:45' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 11, 8, 30, 45))
})

test('reads a time without seconds as zero seconds', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 0, 30)}
      onChange={onChange}
    />
  )
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '09:15' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 11, 9, 15, 0))
})

test('keeps the current time when the time field is emptied', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 20, 30, 45)}
      onChange={onChange}
    />
  )
  const field = screen.getByLabelText('Time')
  fireEvent.change(field, { target: { value: '' } })
  expect(onChange).not.toHaveBeenCalled()
  expect(field).toHaveValue('20:30:45')
})

test('translates DateTimePicker Clear aria-label and StatusBadge Click to copy title across language switches', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'zh',
    fallbackLng: false,
    nsSeparator: false,
    resources: { en, zh },
  })

  render(
    <I18nextProvider i18n={i18n}>
      <DateTimePicker
        value={new Date(2026, 9, 11, 8, 30, 0)}
        onChange={() => undefined}
      />
      <StatusBadge label='草稿' />
    </I18nextProvider>
  )

  expect(screen.getByRole('button', { name: '清空' })).toBeVisible()
  expect(screen.getByText('草稿').closest('[data-slot="status-badge"]')).toHaveAttribute(
    'title',
    '点击复制: 草稿'
  )

  await act(() => i18n.changeLanguage('en'))
  expect(screen.getByRole('button', { name: 'Clear' })).toBeVisible()
  expect(screen.getByText('草稿').closest('[data-slot="status-badge"]')).toHaveAttribute(
    'title',
    'Click to copy: 草稿'
  )
})
