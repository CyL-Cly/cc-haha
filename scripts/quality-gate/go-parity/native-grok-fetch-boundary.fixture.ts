import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Grok fetch boundary')
if (process.env.CC_HAHA_GROK_FETCH_BOUNDARY_FIXTURE !== '1') throw new Error('Unowned Grok fetch boundary fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { buildGrokFetch } = await import('../../../src/services/grokAuth/fetch')
const { clearFreshGrokTokenCache } = await import('../../../src/services/grokAuth/refresh')

const tokenPath = join(process.env.CLAUDE_CONFIG_DIR!, 'private-grok-tokens.json')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
process.env.GROK_OAUTH_FILE = tokenPath

Date.now = () => 1700000000000
const output = []
for (const row of input.rows) {
  clearFreshGrokTokenCache()
  writeFileSync(tokenPath, JSON.stringify(row.tokens), { mode: 0o600 })
  const controller = new AbortController()
  let request: unknown = null
  const requests: unknown[] = []
  const fetcher = buildGrokFetch(async (url, init) => {
    if (String(url).endsWith('/oauth2/token')) return Response.json(row.forceResponse ?? { access_token: 'private-forced', expires_in: 3600 })
    request = { endpoint: String(url), body: JSON.parse(init!.body as string), headers: Object.fromEntries(new Headers(init!.headers)) }
    requests.push(request)
    return new Response(row.upstreamBody, { status: row.upstreamStatus, headers: row.upstreamHeaders })
  }, 'fixture')
  let result: unknown
  try {
    const response = await fetcher!('https://anthropic.invalid/v1/messages', { method: 'POST', headers: { 'x-claude-code-session-id': row.session }, body: JSON.stringify(row.body), signal: controller.signal })
    const status = response.status
    const headers = Object.fromEntries(response.headers)
    try { result = { status, headers, body: await response.text(), request, requests } } catch (error) {
      result = { status, headers, readError: (error as Error).message, request, requests }
    }
  } catch (error) {
    result = { error: (error as Error).message, request, requests }
  }

  output.push(result)
}
process.stdout.write(JSON.stringify(output))
