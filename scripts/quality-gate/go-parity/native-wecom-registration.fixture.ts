import { mkdir, readFile, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'WeCom registration')
if (process.env.CC_HAHA_WECOM_REGISTRATION_FIXTURE !== '1') throw new Error('Unowned WeCom fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const registration = await import('../../../adapters/wecom/qr-auth')
const originalFetch = globalThis.fetch
const originalPlatform = process.platform
let row: any, index = 0, now = 1000, requests: any[] = []
let pendingResolve: (() => void) | undefined
let releaseResolve: (() => void) | undefined
let release: Promise<void> | undefined
const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(req) {
  requests.push({ url: req.headers.get('x-fixture-logical-url'), method: req.method, accept: req.headers.get('accept') })
  const reply = row.replies[index++] ?? { status: 500, raw: 'unexpected request' }
  if (reply.hold) { pendingResolve?.(); await release }
  return new Response(reply.raw ?? JSON.stringify(reply.body), { status: reply.status ?? 200, headers: { 'content-type': 'application/json', ...(reply.redirect ? { location: `http://127.0.0.1:${peer.port}/redirect-target` } : {}) } })
} })
const fakeFetch = async (url: any, init: any) => {
  const logical = String(url)
  const u = new URL(logical)
  if (u.origin !== 'https://work.weixin.qq.com' || !['/ai/qc/generate', '/ai/qc/query_result'].includes(u.pathname)) throw new Error('Unexpected logical platform URL')
  if (init.redirect !== 'error') throw new Error('Source redirect guard missing')
  const headers = new Headers(init.headers)
  headers.set('x-fixture-logical-url', logical)
  return originalFetch(`http://127.0.0.1:${peer.port}/registration`, { ...init, headers })
}
globalThis.fetch = fakeFetch as typeof fetch
Date.now = () => now
const { handleAdaptersApi } = await import('../../../src/server/api/adapters')
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
function resultError(error: Error) { return /redirect/i.test(error.message) ? '<redirect rejected>' : error.message }
function normalize(result: any) {
  if (result?.qrDataUrl) { result = { ...result, qr: result.qrDataUrl.startsWith('data:image/png;base64,') }; delete result.qrDataUrl }
  return result
}
const output = []
for (row of input.rows) {
  registration.resetWecomQrLoginsForTest()
  Object.defineProperty(process, 'platform', { value: row.platform ?? originalPlatform, configurable: true })
  index = 0; requests = []; now = 1000
  await writeFile(file, JSON.stringify(row.seed ?? {}), { mode: 0o600 })
  let key: any = row.options?.sessionKey ?? 'private-fixed-key'
  const values = []
  for (const action of row.actions) {
    if (action.now !== undefined) now = action.now
    if (row.route) {
      const tail = action.tail ?? ['login', action.kind]
      const u = new URL(`http://private.test/api/adapters/wecom/${tail.join('/')}`)
      const req = new Request(u, { method: action.method ?? 'POST', ...(action.method === 'GET' ? {} : { body: action.raw ?? JSON.stringify(action.input ?? { sessionKey: key }) }) })
      const response = await handleAdaptersApi(req, u, ['api', 'adapters', 'wecom', ...tail])
      const body = await response.json()
      if (body.sessionKey) key = body.sessionKey
      if (body.sessionKey) body.sessionKey = '<uuid>'
      if (body.expiresAt !== undefined) { if (body.expiresAt !== now + 300000) throw new Error('Source expiry contract'); body.expiresAt = '<five-minute-expiry>' }
      values.push({ status: response.status, body: normalize(body) })
    } else {
      try {
        let value
        if (action.kind === 'start') value = await registration.startWecomQrLogin({ ...row.options, ...action.options, sessionKey: key, fetchImpl: fakeFetch as any })
        else if (action.kind === 'cancel') { registration.cancelWecomQrLogin(key); value = { cancelled: true } }
        else if (action.kind === 'cancel_poll') {
          const pending = new Promise<void>(resolve => { pendingResolve = resolve })
          release = new Promise<void>(resolve => { releaseResolve = resolve })
          const poll = registration.pollWecomQrLogin({ sessionKey: key, fetchImpl: fakeFetch as any })
          await pending
          registration.cancelWecomQrLogin(key)
          releaseResolve!()
          value = await poll
        } else value = await registration.pollWecomQrLogin({ sessionKey: action.key ?? key, fetchImpl: fakeFetch as any })
        values.push({ value: normalize(value) })
      } catch (error) { values.push({ error: resultError(error as Error) }) }
    }
  }
  output.push({ values, requests, raw: JSON.parse(await readFile(file, 'utf8')), ...(row.route ? { storedJson: JSON.stringify(JSON.parse(await readFile(file, 'utf8'))) } : {}), mode: (await stat(file)).mode & 0o777 })
}
Object.defineProperty(process, 'platform', { value: originalPlatform, configurable: true })
await peer.stop(true)
process.stdout.write(JSON.stringify(output))
