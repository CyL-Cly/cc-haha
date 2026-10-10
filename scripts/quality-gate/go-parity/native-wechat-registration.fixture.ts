import { readFile, mkdir, writeFile, stat, chmod } from 'node:fs/promises'
import { createServer } from 'node:http'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'WeChat registration')
if (process.env.CC_HAHA_WECHAT_REGISTRATION_FIXTURE !== '1') throw new Error('Unowned WeChat fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
// Export only reset for test isolation; all original protocol functions stay real.
Bun.plugin({ name: 'wechat-original-fixture-reset', setup(build) {
  build.onLoad({ filter: /adapters\/wechat\/protocol\.ts$/ }, async args => ({ contents: await Bun.file(args.path).text() + '\nexport function clearWechatLoginFixture() { activeLogins.clear() }\n', loader: 'ts' }))
} })
const registration = await import('../../../adapters/wechat/protocol') as typeof import('../../../adapters/wechat/protocol') & { clearWechatLoginFixture(): void }
const originalFetch = globalThis.fetch
let row: any, index = 0, now = 1000, requests: any[] = []
let pendingResolve: (() => void) | undefined, releaseResolve: (() => void) | undefined
let release: Promise<void> | undefined
const peer = createServer(async (req, res) => {
  if (req.headers['ilink-app-id'] !== 'bot' || req.headers['ilink-app-clientversion'] !== '131335') throw new Error('Original physical redirect/common headers lost')
  const reply = row.replies[index++] ?? { status: 500, raw: 'unexpected physical request' }
  if (reply.disconnect) { req.socket.destroy(); return }
  if (reply.hold) { pendingResolve?.(); await release }
  res.writeHead(reply.status ?? (reply.redirect ? 302 : 200), { 'content-type': 'application/json', ...(reply.redirect ? { location: '/private-follow' } : {}) })
  res.end(reply.raw ?? JSON.stringify(reply.body))
})
await new Promise<void>(resolve => peer.listen(0, '127.0.0.1', resolve))
const port = (peer.address() as any).port
Date.now = () => now
globalThis.fetch = (async (url: any, init: any) => {
  const logical = String(url)
  const u = new URL(logical)
  if (!['/ilink/bot/get_bot_qrcode', '/ilink/bot/get_qrcode_status', '/gateway/ilink/bot/get_qrcode_status'].includes(u.pathname)) throw new Error('Unexpected logical WeChat endpoint')
  const headers = new Headers(init.headers)
  requests.push({ url: logical, method: init.method, appId: headers.get('ilink-app-id'), clientVersion: headers.get('ilink-app-clientversion'), timed: Boolean(init.signal) })
  return originalFetch(`http://127.0.0.1:${port}/private`, init)
}) as typeof fetch
const { handleAdaptersApi } = await import('../../../src/server/api/adapters')
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
const output = []
for (row of input.rows) {
  registration.clearWechatLoginFixture(); index = 0; requests = []; now = 1000
  await writeFile(file, JSON.stringify(row.seed ?? {}), { mode: 0o600 })
  let key: any = row.options?.sessionKey ?? 'private-fixed-key'
  const values = []
  for (const action of row.actions) {
    if (action.now !== undefined) now = action.now
    if (action.failWrite) await chmod(process.env.CLAUDE_CONFIG_DIR!, 0o500)
    if (row.route) {
      const tail = action.tail ?? ['login', action.kind === 'cancel_renderer_poll' ? 'poll' : action.kind]
      const url = new URL(`http://private.test/api/adapters/wechat/${tail.join('/')}`)
      const method = action.method ?? 'POST'
      let response: Response
      if (action.kind === 'cancel_renderer_poll') {
        const ready = new Promise<void>(resolve => { pendingResolve = resolve })
        release = new Promise<void>(resolve => { releaseResolve = resolve })
        const controller = new AbortController()
        const pending = handleAdaptersApi(new Request(url, { method, signal: controller.signal, body: JSON.stringify({ sessionKey: key }) }), url, ['api','adapters','wechat',...tail])
        await ready
        controller.abort()
        releaseResolve!()
        response = await pending
      } else response = await handleAdaptersApi(new Request(url, { method, ...(method === 'GET' ? {} : { body: action.raw ?? JSON.stringify(action.input ?? { sessionKey: key }) }) }), url, ['api','adapters','wechat',...tail])
      const body = await response.json()
      if (body.sessionKey) { key = body.sessionKey; body.sessionKey = '<uuid>' }
      values.push({ status: response.status, body })
    } else {
      try {
        const value = action.kind === 'start' ? await registration.startWechatLoginWithQr({ ...row.options, ...action.options, sessionKey: key }) : await registration.pollWechatLoginWithQr({ sessionKey: action.key ?? key })
        values.push(value === undefined ? {} : { value })
      } catch (error) {
        values.push({ error: error instanceof SyntaxError ? '<invalid JSON>' : (error as Error).message })
      }
    }
  }
  const storedJson = await readFile(file, 'utf8')
  output.push({ values, requests, wireCalls: index, raw: JSON.parse(storedJson), ...(row.route ? { storedJson } : {}), mode: (await stat(file)).mode & 0o777 })
  await chmod(process.env.CLAUDE_CONFIG_DIR!, 0o700)
}
await new Promise<void>((resolve, reject) => peer.close(error => error ? reject(error) : resolve()))
process.stdout.write(JSON.stringify(output))
