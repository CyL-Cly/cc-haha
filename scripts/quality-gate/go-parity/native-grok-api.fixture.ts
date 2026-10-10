import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native Grok API')
if (process.env.CC_HAHA_OPENAI_API_FIXTURE !== '1') throw new Error('Unowned Grok API fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { handleHahaGrokOAuthApi } = await import('../../../src/server/api/haha-grok-oauth')
const { hahaGrokOAuthService } = await import('../../../src/server/services/hahaGrokOAuthService')
const originalNow = Date.now
Date.now = () => input.now
const rows = []
try {
  for (const row of input.rows) {
    if (row.tokens !== undefined) {
      await mkdir(join(home, '.claude', 'cc-haha'), { recursive: true })
      await writeFile(hahaGrokOAuthService.getOAuthFilePath(), typeof row.tokens === 'string' ? row.tokens : JSON.stringify(row.tokens))
    }
    const url = new URL(row.path, 'http://localhost:3456')
    const req = new Request(url, {
      method: row.method,
      ...(row.method === 'GET' ? {} : { body: row.body ?? '' }),
    })
    const response = await handleHahaGrokOAuthApi(req, url, url.pathname.split('/').filter(Boolean))
    const isHTML = response.headers.get('content-type')?.includes('text/html')
    let body: any = isHTML ? await response.text() : await response.json()
    if (row.path.endsWith('/start') && response.ok) {
      const auth = new URL(body.authorizeUrl)
      body = { stateLength: body.state.length, issuer: auth.origin, path: auth.pathname, keys: [...auth.searchParams.keys()], clientId: auth.searchParams.get('client_id'), redirectHost: new URL(auth.searchParams.get('redirect_uri')!).hostname, redirectPath: new URL(auth.searchParams.get('redirect_uri')!).pathname, method: auth.searchParams.get('code_challenge_method'), challengeLength: auth.searchParams.get('code_challenge')!.length, nonceLength: auth.searchParams.get('nonce')!.length }
    }
    rows.push({ status: response.status, body, ...(isHTML ? { cache: response.headers.get('cache-control') } : {}) })
  }
} finally {
  Date.now = originalNow
  hahaGrokOAuthService.dispose()
}
process.stdout.write(JSON.stringify(rows))
