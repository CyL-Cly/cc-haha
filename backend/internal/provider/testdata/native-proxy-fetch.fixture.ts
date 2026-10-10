import { getProxyFetchOptions } from '../../../../src/utils/proxy.ts'

const row = JSON.parse(await Bun.stdin.text())
const response = await fetch(row.url, {
  ...getProxyFetchOptions({ forAnthropicAPI: true, targetUrl: row.url }),
  method: 'POST',
  headers: { 'content-type': 'application/json', authorization: 'Bearer fake-owned-loopback-only' },
  body: '{"original":true}',
})
console.log(JSON.stringify({ status: response.status, body: await response.text() }))
