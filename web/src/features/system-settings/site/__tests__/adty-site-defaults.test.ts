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
import { describe, expect, it } from 'vitest'

import {
  ADTY_SITE_OPTION_DEFAULTS,
  toAdtySiteDefaults,
} from '../adty-site-defaults'

describe('adty site option defaults', () => {
  it('includes wallet_notice and voucher_purchase_url defaults and mapping', () => {
    expect(ADTY_SITE_OPTION_DEFAULTS['adty_site_setting.wallet_notice']).toBe(
      ''
    )
    expect(
      ADTY_SITE_OPTION_DEFAULTS['adty_site_setting.voucher_purchase_url']
    ).toBe('')

    const mapped = toAdtySiteDefaults({
      ...ADTY_SITE_OPTION_DEFAULTS,
      'adty_site_setting.wallet_notice': 'Recharge notice',
      'adty_site_setting.voucher_purchase_url': 'https://shop.example.com',
    })
    expect(mapped.wallet_notice).toBe('Recharge notice')
    expect(mapped.voucher_purchase_url).toBe('https://shop.example.com')
  })
})
