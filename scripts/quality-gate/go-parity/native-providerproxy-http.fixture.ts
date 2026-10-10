import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'

assertDisposableFixtureHome(process.env.HOME, 'Provider proxy physical HTTP')
globalThis.MACRO = { VERSION: 'proxy-fixture', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
Date.now = () => 1777000000123
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
await writeFile(`${process.env.CLAUDE_CONFIG_DIR}/settings.json`, JSON.stringify({ network: { proxy: { mode: 'direct' } }, traceCapture: { enabled: false } }))
const { ProviderService } = await import('../../../src/server/services/providerService')
const { handleProxyRequest } = await import('../../../src/server/proxy/handler')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let selected: any
let captured: unknown
const upstream = Bun.serve({ port: 0, hostname: '127.0.0.1', async fetch(req) {
  captured = { path: new URL(req.url).pathname, headers: Object.fromEntries(req.headers), body: await req.json() }
  const bytes = Buffer.from(selected.wire.bytes, 'base64')
  return new Response([204, 205].includes(selected.wire.status) ? null : bytes, { status: selected.wire.status, headers: selected.wire.headers })
} })
const results = []
try {
  for (const row of rows) {
    selected = row
    captured = null
    ProviderService.prototype.getProviderForProxy = async () => row.noProvider ? null : { ...row.config, baseUrl: `http://127.0.0.1:${upstream.port}` }
    const path = row.path ?? '/proxy/v1/messages'
    const request = new Request(`http://127.0.0.1${path}`, { method: row.method ?? 'POST', body: row.method === 'GET' ? undefined : row.rawBody ?? JSON.stringify(row.body), headers: row.headers })
    try {
      const response = await handleProxyRequest(request, new URL(request.url))
      const bytes = await response.arrayBuffer()
      results.push({ captured, status: response.status, headers: Object.fromEntries(response.headers), bytes: Buffer.from(bytes).toString('base64') })
    } catch (error) {
      results.push({ captured, thrown: { name: (error as Error).name, message: (error as Error).message } })
    }
  }
} finally { upstream.stop(true) }
process.stdout.write(JSON.stringify(results))
