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
import React, { useState, useCallback, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import useDialogState from '@/hooks/use-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { fetchTokenKey, fetchTokenKeysBatch } from '../api'
import { ERROR_MESSAGES } from '../constants'
import type { ApiKey, ApiKeysDialogType } from '../types'

type ApiKeysContextType = {
  open: ApiKeysDialogType | null
  setOpen: (str: ApiKeysDialogType | null) => void
  currentRow: ApiKey | null
  setCurrentRow: React.Dispatch<React.SetStateAction<ApiKey | null>>
  initialCreateGroup: string | null
  setInitialCreateGroup: React.Dispatch<React.SetStateAction<string | null>>
  refreshTrigger: number
  triggerRefresh: () => void
  resolvedKey: string
  setResolvedKey: React.Dispatch<React.SetStateAction<string>>
  resolveRealKey: (id: number) => Promise<string | null>
  resolveRealKeysBatch: (ids: number[]) => Promise<Record<number, string>>
  resolvedKeys: Record<number, string>
  loadingKeys: Record<number, boolean>
  copiedKeyId: number | null
  markKeyCopied: (id: number) => void
}

const ApiKeysContext = React.createContext<ApiKeysContextType | null>(null)

export function ApiKeysProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const { requestVerification, dialogProps } = useSecureVerification()
  const [open, setOpen] = useDialogState<ApiKeysDialogType>(null)
  const [currentRow, setCurrentRow] = useState<ApiKey | null>(null)
  const [initialCreateGroup, setInitialCreateGroup] = useState<string | null>(
    null
  )
  const [refreshTrigger, setRefreshTrigger] = useState(0)
  const [resolvedKey, setResolvedKey] = useState('')

  const [resolvedKeys, setResolvedKeys] = useState<Record<number, string>>({})
  const [loadingKeys, setLoadingKeys] = useState<Record<number, boolean>>({})
  const pendingRequests = useRef<Record<number, Promise<string | null>>>({})

  const [copiedKeyId, setCopiedKeyId] = useState<number | null>(null)
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)

  useEffect(() => {
    return () => clearTimeout(copiedTimerRef.current)
  }, [])

  const markKeyCopied = useCallback((id: number) => {
    setCopiedKeyId(id)
    clearTimeout(copiedTimerRef.current)
    copiedTimerRef.current = setTimeout(() => setCopiedKeyId(null), 2000)
  }, [])

  const triggerRefresh = useCallback(() => {
    setRefreshTrigger((prev) => prev + 1)
  }, [])

  const obtainApiKeyReadProof = useCallback(
    async (
      context: { token_id: number } | { token_ids: number[] }
    ): Promise<string | null> => {
      const proof = await requestVerification({
        scope: 'api_key.read',
        context,
        title: t('Verify your identity'),
        description: t(
          'Viewing or copying a full API key requires security verification.'
        ),
      })
      return proof?.proof_token ?? null
    },
    [requestVerification, t]
  )

  const resolveRealKey = useCallback(
    async (id: number): Promise<string | null> => {
      if (resolvedKeys[id]) return resolvedKeys[id]
      if (id in pendingRequests.current) return pendingRequests.current[id]

      const request = (async () => {
        const proofToken = await obtainApiKeyReadProof({ token_id: id })
        if (!proofToken) return null

        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
        try {
          const res = await fetchTokenKey(id, proofToken)
          if (res.success && res.data?.key) {
            const fullKey = `sk-${res.data.key}`
            setResolvedKeys((prev) => ({ ...prev, [id]: fullKey }))
            return fullKey
          }
          handleServerError(res, t(ERROR_MESSAGES.UNEXPECTED))
          return null
        } catch (error) {
          handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
          return null
        } finally {
          delete pendingRequests.current[id]
          setLoadingKeys((prev) => {
            const next = { ...prev }
            delete next[id]
            return next
          })
        }
      })()

      pendingRequests.current[id] = request
      return request
    },
    [resolvedKeys, obtainApiKeyReadProof, t]
  )

  const resolveRealKeysBatch = useCallback(
    async (ids: number[]): Promise<Record<number, string>> => {
      const uncachedIds = ids.filter((id) => !resolvedKeys[id])
      if (uncachedIds.length === 0) {
        const result: Record<number, string> = {}
        for (const id of ids) result[id] = resolvedKeys[id]
        return result
      }

      const proofToken = await obtainApiKeyReadProof({ token_ids: uncachedIds })
      if (!proofToken) return {}

      for (const id of uncachedIds) {
        setLoadingKeys((prev) => ({ ...prev, [id]: true }))
      }

      try {
        const res = await fetchTokenKeysBatch(uncachedIds, proofToken)
        if (res.success && res.data?.keys) {
          const newKeys: Record<number, string> = {}
          for (const [idStr, key] of Object.entries(res.data.keys)) {
            newKeys[Number(idStr)] = `sk-${key}`
          }
          setResolvedKeys((prev) => ({ ...prev, ...newKeys }))

          const result: Record<number, string> = { ...newKeys }
          for (const id of ids) {
            if (resolvedKeys[id]) result[id] = resolvedKeys[id]
          }
          return result
        }
        handleServerError(res, t(ERROR_MESSAGES.UNEXPECTED))
        return {}
      } catch (error) {
        handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
        return {}
      } finally {
        for (const id of uncachedIds) {
          setLoadingKeys((prev) => {
            const next = { ...prev }
            delete next[id]
            return next
          })
        }
      }
    },
    [resolvedKeys, obtainApiKeyReadProof, t]
  )

  return (
    <>
      <ApiKeysContext
        value={{
          open,
          setOpen,
          currentRow,
          setCurrentRow,
          initialCreateGroup,
          setInitialCreateGroup,
          refreshTrigger,
          triggerRefresh,
          resolvedKey,
          setResolvedKey,
          resolveRealKey,
          resolveRealKeysBatch,
          resolvedKeys,
          loadingKeys,
          copiedKeyId,
          markKeyCopied,
        }}
      >
        {children}
      </ApiKeysContext>
      <SecureVerificationDialog {...dialogProps} />
    </>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useApiKeys = () => {
  const apiKeysContext = React.useContext(ApiKeysContext)

  if (!apiKeysContext) {
    throw new Error('useApiKeys has to be used within <ApiKeysContext>')
  }

  return apiKeysContext
}
