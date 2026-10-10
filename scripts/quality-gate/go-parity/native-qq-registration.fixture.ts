import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import https from 'node:https'
import crypto from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'QQ actual vendor HTTPS registration')
if (process.env.CC_HAHA_QQ_REGISTRATION_FIXTURE !== '1') throw new Error('Unowned QQ registration fixture')
const input = await Bun.stdin.json()
const realTimeout = globalThis.setTimeout
const realClear = globalThis.clearTimeout
let now = 1000, row: any, at = 0, rowIndex = -1, actionLabel = ''
let requests: any[] = [], events: string[] = [], watchers: (() => void)[] = []
let held = new Map<number, () => void>()
let timers = new Map<number, { duration: number; callback: () => void }>()
let timerId = 0
const changed = () => { for (const f of watchers.splice(0)) f() }
async function until(predicate: () => boolean) {
  let deadline: ReturnType<typeof realTimeout>
  await Promise.race([
    (async () => { while (!predicate()) await new Promise<void>(resolve => watchers.push(resolve)) })(),
    new Promise<never>((_, reject) => { deadline = realTimeout(() => reject(new Error(`QQ physical barrier timed out row ${rowIndex} action ${actionLabel} events ${JSON.stringify(events)} HTTP ${at} timers ${JSON.stringify([...timers.values()].map(t => t.duration))}`)), 4000) }),
  ]).finally(() => realClear(deadline))
}
globalThis.setTimeout = ((callback: () => void, duration: number, ...args: any[]) => {
  if (duration !== 2000 && duration !== 20000) return realTimeout(callback, duration, ...args)
  const id = ++timerId
  timers.set(id, { duration, callback })
  changed()
  return id as any
}) as typeof setTimeout
globalThis.clearTimeout = ((id: any) => { if (timers.delete(id)) changed(); else realClear(id) }) as typeof clearTimeout
Date.now = () => now
crypto.randomBytes = ((size: number) => { if (size !== 32) throw new Error('Unowned vendor crypto request'); return Buffer.alloc(32) }) as any
const peer = https.createServer({ key: input.key, cert: input.cert }, async (req, res) => {
  let body = ''
  for await (const chunk of req) body += chunk
  const index = at++
  requests.push({ url: req.headers['x-fixture-logical-url'], method: req.method, contentType: req.headers['content-type'], accept: req.headers.accept, contentLength: Number(req.headers['content-length']), userAgent: req.headers['user-agent'] ?? '', acceptEncoding: req.headers['accept-encoding'] ?? '', connection: req.headers.connection ?? '', body: JSON.parse(body) })
  changed()
  const reply = row.replies[index]
  if (!reply) { res.writeHead(500); res.end('unexpected physical request'); return }
  if (reply.hold) await new Promise<void>(resolve => { held.set(index, resolve); changed() })
  let payload = reply.body
  if (reply.secret !== undefined) {
    const cipher = crypto.createCipheriv('aes-256-gcm', Buffer.alloc(32), Buffer.alloc(12))
    const encrypted = Buffer.concat([Buffer.alloc(12), cipher.update(reply.secret, 'utf8'), cipher.final(), cipher.getAuthTag()]).toString('base64')
    payload = { retcode: 0, data: { status: 2, bot_appid: reply.appId ?? 'private-app', bot_encrypt_secret: encrypted, user_openid: reply.userOpenid } }
  }
  res.writeHead(reply.status ?? 200, { 'content-type': 'application/json', ...(reply.redirect ? { location: `https://127.0.0.1:${(peer.address() as any).port}/escaped` } : {}) })
  res.end(reply.raw ?? JSON.stringify(payload))
})
await new Promise<void>(resolve => peer.listen(0, '127.0.0.1', resolve))
const originalRequest = https.request
https.request = ((options: any, callback: any) => {
  if (options.hostname !== 'q.qq.com' || !['/lite/create_bind_task', '/lite/poll_bind_result'].includes(options.path) || options.method !== 'POST' || options.timeout !== 10000) throw new Error('Unowned QQ vendor HTTPS primitive')
  return originalRequest({ ...options, hostname: '127.0.0.1', port: (peer.address() as any).port, rejectUnauthorized: false, headers: { ...options.headers, 'X-Fixture-Logical-URL': `https://${options.hostname}${options.path}` } }, callback)
}) as typeof https.request
const source = await import('../../../adapters/qq/qr-auth')
const vendor = await import(join(dirname(createRequire(new URL('../../../adapters/package.json', import.meta.url)).resolve('@tencent-connect/qqbot-connector')), '../esm/qr-connect.js'))
// This seam only observes the original vendor's callbacks. The real vendor
// creates sockets, parses responses, decrypts secrets, sleeps and disposes.
source.setQqQrConnector((callbacks, options) => vendor.startQrConnect({
  ...callbacks,
  onQrDisplayed(url: string) { callbacks.onQrDisplayed?.(url); events.push('qr'); changed() },
  onSuccess(credentials: any) { callbacks.onSuccess(credentials); events.push('success'); changed() },
  onFailure(error: Error) { callbacks.onFailure(error); events.push('failed'); changed() },
}, options))
const output = []
for (row of input.rows) {
  rowIndex++
  source.resetQqQrLoginsForTest()
  at = 0; requests = []; events = []; held = new Map(); timers = new Map(); now = 1000
  let started: Promise<any> | undefined
  const values = []
  const key = row.options?.sessionKey ?? 'private-fixed-key'
  const normalize = (value: any) => ({ value })
  for (const action of row.actions) {
    actionLabel = JSON.stringify(action)
    if (action.now !== undefined) now = action.now
    if (action.kind === 'wait') {
      await until(() => action.target === 'sleep' ? [...timers.values()].some(t => t.duration === 2000)
        : action.target === 'request' ? held.has(action.index ?? 0)
        : action.target === 'terminal' ? events.some(e => e === 'success' || e === 'failed')
        : events.filter(e => e === action.target).length >= (action.count ?? 1))
      continue
    }
    if (action.kind === 'tick') {
      const entry = [...timers.entries()].find(([, t]) => t.duration === (action.duration ?? 2000))
      if (!entry) throw new Error('Missing actual vendor timer')
      timers.delete(entry[0]); entry[1].callback(); changed(); continue
    }
    if (action.kind === 'release') { held.get(action.index ?? 0)!(); held.delete(action.index ?? 0); continue }
    try {
      if (action.kind === 'start_async') { started = source.startQqQrLogin({ ...row.options, ...action.options, sessionKey: key }); started.catch(() => {}); continue }
      if (action.kind === 'start') values.push(normalize(await source.startQqQrLogin({ ...row.options, ...action.options, sessionKey: key })))
      else if (action.kind === 'await_start') values.push(normalize(await started))
      else if (action.kind === 'cancel') { source.cancelQqQrLogin(action.key ?? key); values.push(normalize({ cancelled: true })) }
      else values.push(normalize(source.pollQqQrLogin({ sessionKey: action.key ?? key })))
    } catch (error: any) { values.push({ error: error.message }) }
  }
  output.push({ values, requests })
  source.resetQqQrLoginsForTest()
  // Every successful fixture ends on a real sleep, terminal callback or failed
  // ready result. Reset aborts sleeps; observe its callback before next row.
  if (timers.size) await until(() => events.includes('failed') || events.includes('success'))
}
for (const release of held.values()) release()
peer.closeAllConnections()
await new Promise<void>(resolve => peer.close(() => resolve()))
source.setQqQrConnector(null)
process.stdout.write(JSON.stringify(output)+'\n')
