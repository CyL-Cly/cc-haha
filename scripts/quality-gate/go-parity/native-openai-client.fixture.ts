import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI client')
if (process.env.CC_HAHA_OPENAI_CLIENT_FIXTURE !== '1') throw new Error('Unowned OpenAI client fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const client = await import('../../../src/services/openaiAuth/client')
const originalNow = Date.now
Date.now = () => input.now
try {
  const rows = input.rows.map((row: any) => {
    try {
      switch (row.kind) {
        case 'authorize': return { value: client.buildOpenAIAuthorizeUrl(row.input) }
        case 'claims': return { value: client.parseOpenAIJwtClaims(row.token) }
        case 'normalize': return { value: client.normalizeOpenAITokens(row.response) }
        case 'refresh': return { value: client.withRefreshedAccessToken(row.existing, row.response) }
        case 'expired': return { value: client.isOpenAITokenExpired(row.expiresAt) }
        default: throw new Error('Unknown owned profile')
      }
    } catch (error) { return { error: (error as Error).message } }
  })
  const requests: any[] = []
  const transportResults: any[] = []
  let current: any
  const tokenServer = Bun.serve({
    hostname: '127.0.0.1',
    port: 0,
    async fetch(req) {
      requests.push({
        method: req.method,
        path: new URL(req.url).pathname,
        body: await req.text(),
        headers: Object.fromEntries(['accept', 'content-type', 'user-agent', 'authorization'].map(key => [key, req.headers.get(key)])),
      })
      return new Response(current.body, { status: current.status, headers: { 'Content-Type': 'application/json' } })
    },
  })
  const originalFetch = globalThis.fetch
  globalThis.fetch = ((address: any, init: any) => {
    if (String(address) !== 'https://auth.openai.com/oauth/token') throw new Error('Unowned token endpoint')
    return originalFetch(`http://127.0.0.1:${tokenServer.port}/oauth/token`, init)
  }) as typeof fetch
  try {
    for (const row of input.transportRows ?? []) {
      current = row
      try {
        const value = row.operation === 'exchange' ? await client.exchangeOpenAICodeForTokens(row.input) : await client.refreshOpenAITokens(row.refreshToken)
        transportResults.push({ value })
      } catch (error) { transportResults.push({ error: (error as Error).message }) }
    }
  } finally {
    globalThis.fetch = originalFetch
    tokenServer.stop(true)
  }
  process.stdout.write(JSON.stringify({ rows, requests, transportResults }))
} finally { Date.now = originalNow }
