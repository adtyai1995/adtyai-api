import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as keysApi from '@/features/keys/api'
import { fetchActiveChatKey } from '@/features/chat/hooks/use-active-chat-key'

import {
  API_KEY_READ_SCOPE,
  SECURITY_PROOF_HEADER,
  SECURITY_PROOF_REQUIRED_CODE,
  fetchTokenKeyWithSecurityProof,
  setApiKeyReadSecurityProof,
  setApiKeySecurityProofRequester,
} from './key-reveal-proof'

describe('key-reveal-proof (ISS-R1-0178)', () => {
  beforeEach(() => {
    setApiKeyReadSecurityProof(null)
    setApiKeySecurityProofRequester(null)
    vi.restoreAllMocks()
  })

  it('acquires api_key.read security proof and attaches X-Security-Proof header', async () => {
    const fetchSpy = vi
      .spyOn(keysApi, 'fetchTokenKey')
      .mockResolvedValue({ success: true, data: { key: 'real-secret-key' } })
    const requester = vi.fn().mockResolvedValue('proof-token-123')

    const result = await fetchTokenKeyWithSecurityProof(42, {
      requestProof: requester,
    })

    expect(requester).toHaveBeenCalledWith({
      scope: API_KEY_READ_SCOPE,
      tokenId: 42,
    })
    expect(fetchSpy).toHaveBeenCalledWith(42, {
      headers: {
        [SECURITY_PROOF_HEADER]: 'proof-token-123',
      },
    })
    expect(result.success).toBe(true)
    expect(result.data?.key).toBe('real-secret-key')
  })

  it('re-prompts for api_key.read proof when cached proof fails with SECURITY_PROOF_REQUIRED', async () => {
    setApiKeyReadSecurityProof('expired-proof')
    const fetchSpy = vi
      .spyOn(keysApi, 'fetchTokenKey')
      .mockResolvedValueOnce({
        success: false,
        code: SECURITY_PROOF_REQUIRED_CODE,
        message: 'Security verification required',
      })
      .mockResolvedValueOnce({
        success: true,
        data: { key: 'fresh-unmasked-key' },
      })
    const requester = vi.fn().mockResolvedValue('fresh-proof-456')

    const result = await fetchTokenKeyWithSecurityProof(77, {
      requestProof: requester,
    })

    expect(requester).toHaveBeenCalledTimes(1)
    expect(requester).toHaveBeenCalledWith({
      scope: API_KEY_READ_SCOPE,
      tokenId: 77,
    })
    expect(fetchSpy).toHaveBeenNthCalledWith(1, 77, {
      headers: { [SECURITY_PROOF_HEADER]: 'expired-proof' },
    })
    expect(fetchSpy).toHaveBeenNthCalledWith(2, 77, {
      headers: { [SECURITY_PROOF_HEADER]: 'fresh-proof-456' },
    })
    expect(result.data?.key).toBe('fresh-unmasked-key')
  })

  it('routes fetchActiveChatKey through api_key.read security proof verification', async () => {
    vi.spyOn(keysApi, 'getApiKeys').mockResolvedValue({
      success: true,
      data: {
        items: [
          {
            id: 99,
            name: 'chat-key',
            key: 'abcd****efgh',
            status: 1,
            remain_quota: 100,
            used_quota: 0,
            unlimited_quota: true,
            expired_time: -1,
            created_time: 1,
            accessed_time: 1,
            group: 'default',
            auto_groups: null,
            cross_group_retry: false,
            model_limits_enabled: false,
            model_limits: '',
            allow_ips: '',
            allow_user_quota_query: true,
            rate_limit_per_minute: 0,
          },
        ],
        total: 1,
        page: 1,
        page_size: 50,
      },
    })
    const fetchSpy = vi
      .spyOn(keysApi, 'fetchTokenKey')
      .mockResolvedValue({ success: true, data: { key: 'chat-raw-secret' } })
    setApiKeySecurityProofRequester(async ({ scope }) =>
      scope === API_KEY_READ_SCOPE ? 'chat-stepup-proof' : null
    )

    const chatKey = await fetchActiveChatKey()

    expect(chatKey).toBe('sk-chat-raw-secret')
    expect(fetchSpy).toHaveBeenCalledWith(99, {
      headers: {
        [SECURITY_PROOF_HEADER]: 'chat-stepup-proof',
      },
    })
  })
})
