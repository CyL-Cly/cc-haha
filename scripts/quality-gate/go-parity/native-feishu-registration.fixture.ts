import { mkdir, readFile, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { gunzipSync } from 'node:zlib'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Feishu registration')
if (process.env.CC_HAHA_FEISHU_REGISTRATION_FIXTURE !== '1') throw new Error('Unowned Feishu fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const registration = await import('../../../adapters/feishu/registration')
const originalFetch = globalThis.fetch
let row: any, index = 0, now = 1000, requests: any[] = []
let pendingResolve: (() => void) | undefined
let releaseResolve: (() => void) | undefined
let release: Promise<void> | undefined
const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(req) {
  const url = new URL(req.url)
  requests.push({ url: req.headers.get('x-fixture-logical-url'), method: req.method, contentType: req.headers.get('content-type'), body: await req.text() })
  const response = row.replies[index++] ?? { status: 500, raw: 'unexpected request' }
  if (response.hold) { pendingResolve?.(); await release }
  return new Response(response.raw ?? JSON.stringify(response.body), { status: response.status ?? 200, headers: { 'content-type': 'application/json' } })
} })
const fakeFetch = async (url: any, init: any) => {
  const logical = String(url)
  if (!/^https:\/\/accounts\.(feishu\.cn|larksuite\.com)\/oauth\/v1\/app\/registration$/.test(logical)) throw new Error('Unexpected logical platform URL')
  const headers = new Headers(init.headers)
  headers.set('x-fixture-logical-url', logical)
  return originalFetch(`http://127.0.0.1:${peer.port}/registration`, { ...init, headers })
}
globalThis.fetch = fakeFetch as typeof fetch
Date.now = () => now
const { handleAdaptersApi } = await import('../../../src/server/api/adapters')
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
function normalize(result: any) {
  if (typeof result?.verificationUri === 'string') {
    const u = new URL(result.verificationUri)
    const compressed = u.searchParams.get('addons')!
    const addons = JSON.parse(gunzipSync(Buffer.from(compressed, 'base64url')).toString('utf8'))
    result = { ...result, addons }
  }
  if (result?.qrDataUrl) { result = { ...result, qr: result.qrDataUrl.startsWith('data:image/png;base64,') }; delete result.qrDataUrl }
  return result
}
const output = []
for (row of input.rows) {
  registration.resetFeishuRegistrationsForTest()
  index = 0; requests = []; now = 1000
  await writeFile(file, JSON.stringify(row.seed ?? {}), { mode: 0o600 })
  let key: any = row.options?.sessionKey ?? 'private-fixed-key'
  const values = []
  for (const action of row.actions) {
    if (action.now !== undefined) now = action.now
    if (row.route) {
      const tail = action.tail ?? ['registration', action.kind]
      const u = new URL(`http://private.test/api/adapters/feishu/${tail.join('/')}`)
      const req = new Request(u, { method: action.method ?? 'POST', ...(action.method === 'GET' ? {} : { body: action.raw ?? JSON.stringify(action.input ?? { sessionKey: key }) }) })
      const response = await handleAdaptersApi(req, u, ['api', 'adapters', 'feishu', ...tail])
      const body = await response.json()
      if (body.sessionKey) key = body.sessionKey
      // UUID is the only nondeterministic field of the actual route.
      if (body.sessionKey) body.sessionKey = '<uuid>'
      values.push({ status: response.status, body: normalize(body) })
    } else {
      try {
        let value
        if (action.kind === 'begin') value = await registration.beginFeishuRegistration({ ...row.options, sessionKey: key, fetchImpl: fakeFetch as any })
        else if (action.kind === 'cancel') { registration.cancelFeishuRegistration(key); value = { cancelled: true } }
        else if (action.kind === 'cancel_poll') {
          const pending = new Promise<void>(resolve => { pendingResolve = resolve })
          release = new Promise<void>(resolve => { releaseResolve = resolve })
          const poll = registration.pollFeishuRegistration({ sessionKey: key, fetchImpl: fakeFetch as any })
          await pending
          registration.cancelFeishuRegistration(key)
          releaseResolve!()
          value = await poll
        } else value = await registration.pollFeishuRegistration({ sessionKey: action.key ?? key, fetchImpl: fakeFetch as any })
        values.push({ value: normalize(value) })
      } catch (error) { values.push({ error: (error as Error).message }) }
    }
  }
  output.push({ values, requests, raw: JSON.parse(await readFile(file, 'utf8')), mode: (await stat(file)).mode & 0o777 })
}
await peer.stop(true)
process.stdout.write(JSON.stringify(output))
