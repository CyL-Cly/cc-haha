import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import https from 'node:https'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'QQ vendor socket lifecycle')
if (process.env.CC_HAHA_QQ_HTTP_LIFECYCLE_FIXTURE !== '1') throw new Error('Unowned QQ socket fixture')
const input = await Bun.stdin.json()
let acknowledgeFirstChunk: (() => void) | undefined
let chunks = 0
const perMode = new Map<string, number>()
const peer = https.createServer({ key: input.key, cert: input.cert }, async (req, res) => {
  for await (const _ of req) { /* actual socket request is fully read */ }
  const mode = req.headers['x-fixture-mode']
  perMode.set(String(mode), (perMode.get(String(mode)) ?? 0) + 1)
  if (mode === 'reuse-no-headers' && perMode.get(String(mode)) === 1) { res.writeHead(200); res.end('{"retcode":0,"data":{"task_id":"private-task"}}'); return }
  if (mode === 'partial-body') { res.writeHead(200); res.write('{"retcode":'); res.flushHeaders() }
  else if (mode === 'reject-body') { res.writeHead(503); res.flushHeaders() }
  else if (mode === 'split-utf8') {
    const received = new Promise<void>(resolve => { acknowledgeFirstChunk = resolve })
    res.writeHead(200)
    res.write(Buffer.concat([Buffer.from('{"retcode":1,"msg":"'), Buffer.from([0xe4])]))
    await received
    res.end(Buffer.concat([Buffer.from([0xb8, 0xad]), Buffer.from('"}')]))
  }
  // The peer deliberately stays open. Vendor request.setTimeout owns abort.
})
await new Promise<void>(resolve => peer.listen(0, '127.0.0.1', resolve))
const originalRequest = https.request
const vendor = await import(join(dirname(createRequire(new URL('../../../adapters/package.json', import.meta.url)).resolve('@tencent-connect/qqbot-connector')), '../esm/qqbot-session.js'))
let mode: string
const events = new Map<string, string[]>()
const outcomes = new Map<string, any>()
const timeoutMs = input.timeoutMs ?? 10000
https.request = ((options: any, callback: any) => {
  if (options.hostname !== 'q.qq.com' || options.path !== '/lite/create_bind_task' || options.timeout !== timeoutMs) throw new Error('Unowned QQ HTTP lifecycle target')
  const current = mode
  const lifecycle: string[] = []
  events.set(current, lifecycle)
  const req = originalRequest({ ...options, hostname: '127.0.0.1', port: (peer.address() as any).port, rejectUnauthorized: false, headers: { ...options.headers, 'X-Fixture-Mode': current } }, res => {
    lifecycle.push('headers')
    res.on('data', () => { lifecycle.push('data'); if (current === 'split-utf8') { chunks++; acknowledgeFirstChunk?.() } })
    res.on('close', () => lifecycle.push('body-close'))
    callback(res)
  })
  req.on('timeout', () => lifecycle.push('timeout'))
  req.on('error', () => lifecycle.push('error'))
  req.on('close', () => lifecycle.push('close'))
  return req
}) as typeof https.request
const pending = input.modes.map((value: string) => {
  mode = value
  const invoke = () => vendor.createBindTask('production', timeoutMs)
  const result = value === 'reuse-no-headers' ? invoke().then(invoke) : invoke()
  return result.then(() => { const out = { mode: value, unexpectedSuccess: true }; outcomes.set(value,out); return out }, (error: Error) => { const out = { mode: value, error: error.message }; outcomes.set(value,out); return out })
})
let timer: ReturnType<typeof setTimeout>
await Promise.race([Promise.all(pending), new Promise(resolve => { timer = setTimeout(resolve, input.observeMs ?? 13000) })]).finally(() => clearTimeout(timer))
const output = input.modes.map((value: string) => ({ ...(outcomes.get(value) ?? { mode:value, pending:true }), events: [...(events.get(value) ?? [])] }))
peer.closeAllConnections()
await new Promise<void>(resolve => peer.close(() => resolve()))
process.stdout.write(JSON.stringify(output)+'\n')
