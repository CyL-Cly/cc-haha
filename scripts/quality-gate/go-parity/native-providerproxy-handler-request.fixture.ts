import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'

assertDisposableFixtureHome(process.env.HOME, 'Provider proxy handler')
globalThis.MACRO = { VERSION: 'proxy-fixture', BUILD_TIME: '', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
Date.now = () => 1777000000123
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
await writeFile(`${process.env.CLAUDE_CONFIG_DIR}/settings.json`, JSON.stringify({ network: { proxy: { mode: 'direct' } }, traceCapture: { enabled: false } }))
const { ProviderService } = await import('../../../src/server/services/providerService')
const { handleProxyRequest } = await import('../../../src/server/proxy/handler')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let captured: unknown = null
const upstream = Bun.serve({ port: 0, hostname: '127.0.0.1', async fetch(req) {
  captured = { path: new URL(req.url).pathname, headers: Object.fromEntries(req.headers), body: await req.json() }
  const body = { id: 'fixture', model: 'wire', choices: [{ message: { content: 'fixture' }, finish_reason: 'stop' }], output: [], status: 'completed' }
  return Response.json(body)
} })
const results = []
try {
  for (const row of rows) {
    captured = null
    const config = { ...row.config, baseUrl: `http://127.0.0.1:${upstream.port}${row.basePath}` }
    ProviderService.prototype.getProviderForProxy = async () => config
    const path = row.scoped ? `/proxy/providers/${config.id}/v1/messages` : '/proxy/v1/messages'
    const request = new Request(`http://127.0.0.1${path}`, { method: 'POST', body: JSON.stringify(row.body), headers: row.headers })
    try {
      const response = await handleProxyRequest(request, new URL(request.url))
      const text = await response.text()
      results.push({ captured, status: response.status, response: text.startsWith('{') ? JSON.parse(text) : text })
    } catch (error) {
      results.push({ captured, thrown: { name: (error as Error).name, message: (error as Error).message } })
    }
  }
} finally { upstream.stop(true) }
process.stdout.write(JSON.stringify(results))
