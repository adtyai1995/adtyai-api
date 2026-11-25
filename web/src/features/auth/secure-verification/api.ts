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
import { api, isAuthBundle } from '@/lib/api'
import {
  AuthOperationError,
  authRequestOptions,
  authResult,
} from '@/lib/secure-verification'
import type { AuthBundle } from '@/stores/auth-store'

import { encryptPassword } from '../lib/password-encryption'
import type {
  SecurityProof,
  LoginChallenge,
  SecurityProofScope,
  VerificationInput,
  VerificationOperation,
  VerificationRequirements,
} from './types'

export async function checkVerificationMethods(
  scope: SecurityProofScope,
  signal?: AbortSignal
): Promise<VerificationRequirements> {
  return authResult<VerificationRequirements>(
    api.get('/api/verify/methods', {
      ...authRequestOptions,
      params: { scope },
      signal,
      disableDuplicate: true,
    })
  )
}

export async function requestSecurityEmailCode(
  scope: string,
  signal?: AbortSignal
): Promise<void> {
  await authResult(
    api.post(
      '/api/verify/email-code',
      { scope },
      { ...authRequestOptions, signal }
    )
  )
}

export function isLoginChallenge(value: unknown): value is LoginChallenge {
  if (!value || typeof value !== 'object') return false
  const challenge = value as Partial<LoginChallenge>
  return (
    challenge.require_verification === true &&
    typeof challenge.flow_token === 'string' &&
    challenge.flow_token.length > 0 &&
    typeof challenge.expires_at === 'number' &&
    Number.isFinite(challenge.expires_at) &&
    Array.isArray(challenge.methods) &&
    challenge.methods.length > 0 &&
    challenge.methods.every(
      (option) =>
        option &&
        option.method === '2fa' &&
        typeof option.available === 'boolean'
    )
  )
}

export async function getLoginVerificationRequirements(
  challenge: LoginChallenge,
  signal: AbortSignal
): Promise<VerificationRequirements> {
  if (challenge.expires_at * 1000 <= Date.now()) {
    throw new AuthOperationError(
      'Login flow expired. Please sign in again.',
      'AUTH_FLOW_INVALID'
    )
  }
  signal.throwIfAborted()
  return {
    scope: 'auth.login',
    methods: challenge.methods,
    password_encryption_enabled: false,
  }
}

export async function verifyLogin(
  input: VerificationInput,
  challenge: LoginChallenge,
  signal: AbortSignal
): Promise<AuthBundle> {
  if (challenge.expires_at * 1000 <= Date.now()) {
    throw new AuthOperationError(
      'Login flow expired. Please sign in again.',
      'AUTH_FLOW_INVALID'
    )
  }
  const options = { ...authRequestOptions, skipAuthRefresh: true, signal }
  if (input.method !== '2fa') {
    throw new AuthOperationError(
      'This verification method is not allowed for this action.'
    )
  }
  const result: unknown = await authResult(
    api.post(
      '/api/user/login/verify',
      {
        flow_token: challenge.flow_token,
        method: '2fa',
        code: input.code.trim(),
      },
      options
    )
  )
  signal.throwIfAborted()
  if (!isAuthBundle(result)) throw new AuthOperationError('Login failed')
  return result
}

export async function verify(
  input: VerificationInput,
  operation: VerificationOperation,
  passwordEncryptionEnabled: boolean,
  signal: AbortSignal
): Promise<SecurityProof> {
  try {
    const operationFields = {
      scope: operation.scope,
      ...(operation.context ? { context: operation.context } : {}),
    }
    let proof: SecurityProof
    switch (input.method) {
      case 'session':
      case 'root_exempt':
        proof = await authResult<SecurityProof>(
          api.post(
            '/api/verify',
            { method: input.method, ...operationFields },
            {
              ...authRequestOptions,
              signal,
            }
          )
        )
        break
      case '2fa':
      case 'email_code':
        proof = await authResult<SecurityProof>(
          api.post(
            '/api/verify',
            {
              method: input.method,
              ...operationFields,
              code: input.code.trim(),
            },
            { ...authRequestOptions, signal }
          )
        )
        break
      case 'password': {
        const passwordFields = passwordEncryptionEnabled
          ? await encryptPassword(input.password)
          : { password: input.password }
        signal.throwIfAborted()
        proof = await authResult<SecurityProof>(
          api.post(
            '/api/verify',
            { method: input.method, ...operationFields, ...passwordFields },
            { ...authRequestOptions, signal }
          )
        )
        break
      }
    }
    signal.throwIfAborted()
    if (
      !proof.proof_token ||
      proof.scope !== operation.scope ||
      proof.method !== input.method ||
      proof.expires_at * 1000 <= Date.now()
    ) {
      throw new AuthOperationError('Verification proof was not returned')
    }
    return proof
  } catch (error) {
    throw AuthOperationError.from(error)
  }
}
