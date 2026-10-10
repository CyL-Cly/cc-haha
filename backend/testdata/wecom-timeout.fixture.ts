import https from 'node:https'
import { WSClient } from '../../adapters/node_modules/@wecom/aibot-node-sdk/dist/index.esm.js'

if (process.env.CC_HAHA_WECOM_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const input = JSON.parse(await Bun.stdin.text())
const url = new URL(input.url)
if (url.protocol !== 'https:' || url.hostname !== '127.0.0.1') throw new Error('private TLS only')
const client = new WSClient({ logger: { debug() {}, info() {}, warn() {}, error() {} }, requestTimeout: input.timeoutMs })
const agent = new https.Agent({ ca: input.ca })
const abort = new AbortController()
client.apiClient.httpClient.defaults.httpsAgent = agent
client.apiClient.httpClient.defaults.signal = abort.signal
let settled = false
let outcome
const request = client.downloadFile(url.href).then(v => { settled = true; outcome = { value: v.buffer.toString('base64') } }, e => { settled = true; outcome = { error: e.message } })
await Promise.race([request, new Promise(r => setTimeout(r, 200))])
const observed = settled ? outcome : { pending: true }
abort.abort()
await request
agent.destroy()
process.stdout.write(JSON.stringify({ observed }))
