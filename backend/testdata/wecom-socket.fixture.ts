import { WSClient } from '../../adapters/node_modules/@wecom/aibot-node-sdk/dist/index.esm.js'

if (process.env.CC_HAHA_WECOM_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const input = JSON.parse(await Bun.stdin.text())
const url = new URL(input.url)
if (url.protocol !== 'wss:' || !['127.0.0.1', 'localhost'].includes(url.hostname)) throw new Error('loopback TLS only')
const logger = { debug() {}, info() {}, warn() {}, error() {} }
const client = new WSClient({ botId: input.botId ?? 'private-bot', secret: input.secret ?? 'private-secret', logger, wsUrl: url.href, wsOptions: { ca: input.ca }, heartbeatInterval: input.mode === 'heartbeat' ? 20 : 30000, reconnectInterval: 1, maxAuthFailureAttempts: 2, maxReconnectAttempts: 2 })
const events: unknown[] = []
let doneResolve: () => void
const done = new Promise<void>(r => { doneResolve = r })
let authResolve: () => void
const auth = new Promise<void>(r => { authResolve = r })
client.on('authenticated', () => { events.push({ authenticated: true }); authResolve() })
client.on('message', frame => events.push({ message: frame }))
client.on('event', frame => { events.push({ event: frame }); if (input.mode === 'heartbeat') doneResolve() })
client.on('disconnected', reason => events.push({ disconnected: reason }))
client.on('reconnecting', attempt => events.push({ reconnecting: attempt }))
client.on('error', error => { events.push({ error: error.message, ...(error.code ? { code: error.code } : {}) }); if (error.code) doneResolve() })
client.connect()
const capture = async (promise: Promise<unknown>) => {
  try { return { value: await promise } } catch (error) { return { error: error instanceof Error ? error.message : error } }
}
let results: unknown[] = []
if (input.mode === 'auth' || input.mode === 'heartbeat' || input.mode === 'reconnect') {
  await done
} else {
  await auth
  const frame = { headers: { req_id: 'r1' } }
  const first = client.replyStream(frame, 'bubble', 'one', false)
  const second = client.replyStream(frame, 'bubble', 'two', true)
  const independent = client.replyStream({ headers: { req_id: 'r2' } }, 'bubble-2', 'parallel', false)
  results = await Promise.all([first, second, independent].map(capture))
}
client.disconnect()
process.stdout.write(JSON.stringify({ results, events }))
