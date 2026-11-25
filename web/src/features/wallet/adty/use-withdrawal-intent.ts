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
import i18next from 'i18next'
import { useSyncExternalStore } from 'react'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type Draft = { amount: string; method: string; quote?: string }
type Saved = Draft & { key: string; accepted?: boolean; receipt?: number }

function subscribe(listener: () => void) {
  window.addEventListener('storage', listener)
  window.addEventListener('withdrawal-intent', listener)
  return () => {
    window.removeEventListener('storage', listener)
    window.removeEventListener('withdrawal-intent', listener)
  }
}

export function useWithdrawalIntent(
  path: string,
  siteId: number,
  archived = false
) {
  const userId = useAuthStore((s) => s.auth.user?.id)
  const slot = `adty-withdrawal:${userId}:${siteId}:${path}`
  const read = () => localStorage.getItem(slot)
  const raw = useSyncExternalStore(subscribe, () => {
    try {
      return read()
    } catch {
      return null
    }
  })
  const saved: Saved | null = raw ? JSON.parse(raw) : null
  const current = (): Saved | null => JSON.parse(read() ?? 'null')
  const write = (value: Saved | null) => {
    if (value) localStorage.setItem(slot, JSON.stringify(value))
    else localStorage.removeItem(slot)
    window.dispatchEvent(new Event('withdrawal-intent'))
  }
  function requireScope() {
    if (
      !userId ||
      userId !== useAuthStore.getState().auth.user?.id ||
      siteId <= 0
    ) {
      throw new Error(i18next.t('Invalid parameters'))
    }
  }
  async function lookup(previous: Saved) {
    requireScope()
    const res = await api.get<{ success: boolean; data: { id: number } }>(
      `${path}/by-key`,
      {
        params: {
          site_id: siteId,
          ...(archived ? { archived_site_id: siteId } : {}),
        },
        headers: { 'Idempotency-Key': previous.key },
      }
    )
    const id = requireServerSuccess(res.data).data?.id
    if (!Number.isSafeInteger(id) || id < 0) {
      throw new Error(i18next.t('Invalid response'))
    }
    requireScope()
    if (current()?.key !== previous.key) {
      throw new Error(i18next.t('Check previous request'))
    }
    if (id) write({ ...previous, accepted: true, receipt: id })
    return id
  }

  async function prepare(draft: Draft) {
    requireScope()
    // Reserve before sending. Two tabs opening the same form share the reservation.
    const reserve = () => {
      const previous = current()
      if (previous) return { previous, key: previous.key }
      const key = crypto.randomUUID().replaceAll('-', '').slice(0, 28)
      write({ ...draft, key })
      return { previous: null, key }
    }
    const { previous, key } = navigator.locks
      ? await navigator.locks.request(slot, reserve)
      : reserve()
    if (previous) {
      if (previous.accepted || (await lookup(previous))) return null
      // A negative read can race an in-flight POST. Even an edited draft must
      // reuse this key, so at most one version can ever reach the ledger.
      write({ ...draft, key })
    }
    requireScope()
    return key
  }
  function clear(key: string) {
    if (current()?.key === key) write(null)
  }
  return {
    saved,
    prepare,
    clear,
    accept: (key: string, receipt?: number) => {
      const pending = current()
      if (pending?.key === key) write({ ...pending, accepted: true, receipt })
    },
    check: () => (saved ? lookup(saved) : Promise.resolve(0)),
  }
}
