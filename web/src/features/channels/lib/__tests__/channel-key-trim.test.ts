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
import { describe, expect, test } from 'vitest'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../channel-form'
import { supportsResponsesWebSocket } from '../responses-websocket'

function keyForm(
  overrides: Partial<ChannelFormValues> = {}
): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Test channel',
    type: 1,
    key: 'test-key',
    models: 'gpt-5',
    ...overrides,
  }
}

function createKey(key: string): string | null {
  const form = channelFormSchema.parse(keyForm({ key }))
  return transformFormDataToCreatePayload(form).channel.key ?? null
}

function updateKey(key: string): string | undefined {
  const form = channelFormSchema.parse(keyForm({ key }))
  return transformFormDataToUpdatePayload(form, 1).key
}

describe('channel key trimming', () => {
  test('trims surrounding whitespace from a single key on create', () => {
    expect(createKey('  sk-123  ')).toBe('sk-123')
  })

  test('trims each newline-separated key on create and drops blank lines', () => {
    expect(createKey('  key-a \n key-b  \n\n  key-c ')).toBe(
      'key-a\nkey-b\nkey-c'
    )
  })

  test('normalizes CRLF-separated keys on create', () => {
    expect(createKey(' key-a \r\n key-b ')).toBe('key-a\nkey-b')
  })

  test('clears a blank key on create', () => {
    expect(createKey('   ')).toBeNull()
  })

  test('keeps JSON credentials intact apart from surrounding whitespace', () => {
    const jsonKey = '  {\n  "access_token": "abc",\n  "account_id": "123"\n}  '
    expect(createKey(jsonKey)).toBe(
      '{\n  "access_token": "abc",\n  "account_id": "123"\n}'
    )
  })

  test('keeps a JSON array of keys intact apart from surrounding whitespace', () => {
    expect(createKey('  [{"a":1},\n{"b":2}]  ')).toBe('[{"a":1},\n{"b":2}]')
  })

  test('trims each newline-separated key on update', () => {
    expect(updateKey('  key-a \n key-b  ')).toBe('key-a\nkey-b')
  })

  test('omits a blank key on update', () => {
    expect(updateKey('   ')).toBeUndefined()
  })
})

describe('channel setting preservation and vertex api version', () => {
  test('renaming keeps fields in setting that the form does not edit', () => {
    const form = keyForm({
      name: 'Renamed channel',
      setting: JSON.stringify({
        extension_option: { enabled: true },
      }),
    })

    const payload = transformFormDataToUpdatePayload(form, 7)

    expect(payload.name).toBe('Renamed channel')
    expect(JSON.parse(payload.setting ?? '{}')).toMatchObject({
      extension_option: { enabled: true },
    })
  })

  test('saving default transport options clears saved overrides', () => {
    const form = keyForm({
      setting: JSON.stringify({
        http_protocol: 'http1',
        http2_connection_shards: 8,
        tls_insecure_skip_verify: true,
      }),
      http_protocol: 'auto',
      http2_connection_shards: 1,
      tls_insecure_skip_verify: false,
    })

    const setting = JSON.parse(
      transformFormDataToUpdatePayload(form, 7).setting ?? '{}'
    )

    expect(setting).not.toHaveProperty('http_protocol')
    expect(setting).not.toHaveProperty('http2_connection_shards')
    expect(setting).not.toHaveProperty('tls_insecure_skip_verify')
  })

  test('round-trips vertex_api_version in settings JSON', () => {
    const defaults = transformChannelToFormDefaults({
      id: 1,
      type: 41,
      key: '',
      status: 1,
      name: 'Vertex',
      created_time: 0,
      test_time: 0,
      response_time: 0,
      other: '{"default":"us-central1"}',
      balance: 0,
      balance_updated_time: 0,
      models: 'gemini-2.5-pro',
      group: 'default',
      used_quota: 0,
      other_info: '',
      remark: '',
      max_input_tokens: 0,
      channel_info: {
        is_multi_key: false,
        multi_key_size: 0,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
      settings: JSON.stringify({
        vertex_key_type: 'json',
        vertex_api_version: 'v1beta1',
      }),
    })

    expect(defaults.vertex_api_version).toBe('v1beta1')
    const payload = transformFormDataToUpdatePayload(defaults, 1)
    expect(JSON.parse(payload.settings ?? '{}').vertex_api_version).toBe(
      'v1beta1'
    )
  })

  test('allows Azure channels to opt in to Responses WebSocket', () => {
    expect(supportsResponsesWebSocket(3)).toBe(true)
  })
})
