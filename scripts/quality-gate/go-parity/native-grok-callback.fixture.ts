import { assertDisposableFixtureHome } from './fixture-guard'
import { readFile, stat } from 'node:fs/promises'
const home = assertDisposableFixtureHome(process.env.HOME, 'Grok actual callback')
if (process.env.CC_HAHA_GROK_CALLBACK_FIXTURE !== '1') throw new Error('Unowned Grok callback fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { HahaGrokOAuthService } = await import('../../../src/server/services/hahaGrokOAuthService')
const originalNow = Date.now
const originalFetch = globalThis.fetch
Date.now = () => input.now
const service = new HahaGrokOAuthService()
let request: any
let verifier = ''
globalThis.fetch = (async (url: any, init: any) => {
  if (String(url) !== 'https://auth.x.ai/oauth2/token') throw new Error('Unexpected official request in disposable callback fixture')
  const form = new URLSearchParams(init.body)
  verifier = form.get('code_verifier')!
  request = { url: String(url), method: init.method, headers: Object.fromEntries(new Headers(init.headers)), grant: form.get('grant_type'), client: form.get('client_id'), code: form.get('code'), redirectPath: new URL(form.get('redirect_uri')!).pathname, verifierLength: verifier.length }
  return Response.json(input.response)
}) as typeof fetch
try {
  const session = await service.startSession()
  const auth = new URL(session.authorizeUrl)
  const response = await originalFetch(`${auth.searchParams.get('redirect_uri')}?code=fake-code&state=${session.state}`)
  const body = await response.text()
  const file = service.getOAuthFilePath()
  const path = file.slice(home.length)
  const mode = (await stat(file)).mode & 0o777
  const stored = await readFile(file, 'utf8')
  const challenge = Buffer.from(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))).toString('base64url')
  process.stdout.write(JSON.stringify({ status: response.status, contentType: response.headers.get('content-type'), body, request, stored, mode, path, pkceMatches: auth.searchParams.get('code_challenge') === challenge, consumed: (service as any).sessions.has(session.state) === false }))
} finally { globalThis.fetch = originalFetch; Date.now = originalNow; service.dispose() }
