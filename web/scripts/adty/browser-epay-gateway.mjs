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
import assert from 'node:assert/strict'
import { createHash, randomBytes } from 'node:crypto'
import { createServer } from 'node:http'

export async function startBrowserEpayGateway() {
  const merchant = 'SCENE_BROWSER'
  const merchantKey = randomBytes(24).toString('hex')
  const orders = new Map()
  let gatewayCalls = 0
  function sign(params) {
    return createHash('md5')
      .update(
        Object.keys(params)
          .filter(
            (key) => !['sign', 'sign_type'].includes(key) && params[key] !== ''
          )
          .sort()
          .map((key) => `${key}=${params[key]}`)
          .join('&') + merchantKey
      )
      .digest('hex')
  }
  const gatewayServer = createServer(async (request, response) => {
    try {
      assert.equal(new URL(request.url, gateway).pathname, '/mapi.php')
      let raw = ''
      for await (const chunk of request) raw += chunk
      const params = Object.fromEntries(new URLSearchParams(raw))
      assert.equal(params.pid, merchant)
      assert.equal(params.sign, sign(params))
      const order = { ...params, gateway_id: `SCENE_${++gatewayCalls}` }
      orders.set(params.out_trade_no, order)
      response.setHeader('Content-Type', 'application/json')
      response.end(
        JSON.stringify({
          code: 1,
          trade_no: order.gateway_id,
          payurl: `${gateway}/checkout`,
        })
      )
    } catch {
      response.statusCode = 400
      response.end('Synthetic gateway rejected request')
    }
  })
  await new Promise((resolve, reject) => {
    gatewayServer.once('error', reject)
    gatewayServer.listen(0, '127.0.0.1', resolve)
  })
  const gateway = `http://127.0.0.1:${gatewayServer.address().port}`
  return {
    url: gateway,
    merchant,
    merchantKey,
    orders,
    sign,
    get calls() {
      return gatewayCalls
    },
    close: () => new Promise((resolve) => gatewayServer.close(resolve)),
  }
}

export async function configureBrowserEpayChannel(api, base, gateway) {
  for (const key of ['ServerAddress', 'CustomCallbackAddress']) {
    await api('put', '/api/option/', { key, value: base })
  }
  const channel = await api('post', '/api/adty/admin/epay/channels', {
    name: 'Scene browser channel',
    gateway_url: gateway.url,
    merchant_id: gateway.merchant,
    merchant_key: gateway.merchantKey,
    priority: 999,
    weight: 1,
    order_mode: 'api',
    probe_mode: 'off',
    unpaid_threshold: 100,
    methods: [
      {
        method: 'alipay',
        type_code: 'alipay',
        min_cny: '0',
        max_cny: '10000',
        daily_limit_cny: '0',
        enabled: true,
        return_type: 'payurl',
      },
    ],
  })
  await api('post', `/api/adty/admin/epay/channels/${channel.id}/status`, {
    status: 'enabled',
  })
  return channel
}
