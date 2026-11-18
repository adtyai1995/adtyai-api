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
import { fetchTokenKey } from '@/features/keys/api'

export const API_KEY_READ_SCOPE = 'api_key.read' as const
export const SECURITY_PROOF_HEADER = 'X-Security-Proof' as const
export const SECURITY_PROOF_REQUIRED_CODE = 'SECURITY_PROOF_REQUIRED' as const

export interface ApiKeySecurityProofContext {
  scope: typeof API_KEY_READ_SCOPE
  tokenId: number
}

export type ApiKeySecurityProofRequester = (
  context: ApiKeySecurityProofContext
) => Promise<string | null | undefined> | string | null | undefined

export interface FetchTokenKeyWithProofOptions {
  proof?: string | null
  requestProof?: ApiKeySecurityProofRequester
}

export interface TokenKeyRevealResult {
  success: boolean
  code?: string
  message?: string
  data?: { key: string }
}

let cachedApiKeyReadProof: string | null = null
let registeredProofRequester: ApiKeySecurityProofRequester | null = null

export function setApiKeyReadSecurityProof(proof: string | null): void {
  const trimmed = proof?.trim() ?? ''
  cachedApiKeyReadProof = trimmed.length > 0 ? trimmed : null
}

export function getApiKeyReadSecurityProof(): string | null {
  return cachedApiKeyReadProof
}

export function setApiKeySecurityProofRequester(
  requester: ApiKeySecurityProofRequester | null
): void {
  registeredProofRequester = requester
}

export function getApiKeySecurityProofRequester(): ApiKeySecurityProofRequester | null {
  return registeredProofRequester
}

export function isSecurityProofRequired(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false
  const record = value as Record<string, unknown>
  if (record.code === SECURITY_PROOF_REQUIRED_CODE) return true
  if (
    typeof record.message === 'string' &&
    record.message.includes(SECURITY_PROOF_REQUIRED_CODE)
  ) {
    return true
  }
  const response = record.response as Record<string, unknown> | undefined
  if (response && typeof response === 'object') {
    if (isSecurityProofRequired(response.data)) return true
    if (response.status === 403 && !record.code) return true
  }
  return false
}

function buildProofRequestConfig(proof: string | null) {
  if (!proof) return undefined
  return {
    headers: {
      [SECURITY_PROOF_HEADER]: proof,
    },
  }
}

/**
 * Fetches an unmasked API key with `api_key.read` step-up security proof
 * enforcement so cross-feature shortcuts (Quick Start CC Switch, Quick Start
 * curl copy, and Chat active key link) attach `X-Security-Proof` consistently
 * with `/keys`.
 */
export async function fetchTokenKeyWithSecurityProof(
  tokenId: number,
  options?: FetchTokenKeyWithProofOptions
): Promise<TokenKeyRevealResult> {
  const requester = options?.requestProof ?? registeredProofRequester
  let proof = options?.proof?.trim() || cachedApiKeyReadProof
  let requestedFreshProof = false

  if (!proof && requester) {
    requestedFreshProof = true
    const acquired = await requester({ scope: API_KEY_READ_SCOPE, tokenId })
    const trimmed = acquired?.trim() || null
    if (trimmed) {
      cachedApiKeyReadProof = trimmed
      proof = trimmed
    }
  }

  const executeWithProof = async (
    activeProof: string | null
  ): Promise<TokenKeyRevealResult> => {
    const config = buildProofRequestConfig(activeProof)
    return config
      ? await fetchTokenKey(tokenId, config)
      : await fetchTokenKey(tokenId)
  }

  try {
    const result = await executeWithProof(proof)
    if (
      !result.success &&
      isSecurityProofRequired(result) &&
      requester &&
      !requestedFreshProof
    ) {
      cachedApiKeyReadProof = null
      const retryProof = (
        await requester({ scope: API_KEY_READ_SCOPE, tokenId })
      )?.trim()
      if (retryProof) {
        cachedApiKeyReadProof = retryProof
        return await executeWithProof(retryProof)
      }
    }
    return result
  } catch (error) {
    if (isSecurityProofRequired(error) && requester && !requestedFreshProof) {
      cachedApiKeyReadProof = null
      const retryProof = (
        await requester({ scope: API_KEY_READ_SCOPE, tokenId })
      )?.trim()
      if (retryProof) {
        cachedApiKeyReadProof = retryProof
        return await executeWithProof(retryProof)
      }
    }
    throw error
  }
}
