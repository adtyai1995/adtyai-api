import { describe, expect, it } from 'vitest'

import { retailOverrideChanges } from '../lib/site-owner'

describe('editing retail overrides', () => {
  it('keeps a re-added key, removes a renamed key and omits untouched prices', () => {
    expect(
      retailOverrideChanges({ gpt: '2', old: '3', keep: '4' }, [
        ['gpt', '5'],
        ['renamed', '3'],
        ['keep', '4'],
      ])
    ).toEqual({ gpt: '5', old: '0', renamed: '3' })
  })
})
