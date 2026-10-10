import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import crypto from 'node:crypto'
import https from 'node:https'

if (process.env.CC_HAHA_WECOM_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const root = new URL('../../', import.meta.url)
const sdk = await import(new URL('adapters/node_modules/@wecom/aibot-node-sdk/dist/index.esm.js', root).href)
const { extractWecomPayload } = await import(new URL('adapters/wecom/extract-payload.ts', root).href)
const { collectWecomMediaCandidates } = await import(new URL('adapters/wecom/media.ts', root).href)
const { splitMessageByBytes, utf8Length } = await import(new URL('adapters/common/format.ts', root).href)
const input = JSON.parse(await Bun.stdin.text())
const logger = { debug() {}, info() {}, warn() {}, error() {} }
Date.now = () => 1770000000123
const capture = async (fn: () => unknown) => {
  try { return { value: await fn() } } catch (error) { return { error: error instanceof Error ? error.message : error } }
}
const result: Record<string, unknown> = {}
if (input.credentials) result.credentials = input.credentials.map(value => {
  const client = new sdk.WSClient({ botId: value, secret: true, logger })
  let body
  client.wsManager.ws = { readyState: 1, send(wire) { body = JSON.parse(wire).body } }
  client.wsManager.sendAuth()
  return body
})
if (input.payloads) {
  result.payloads = await Promise.all(input.payloads.map(body => capture(() => extractWecomPayload(body))))
  result.media = await Promise.all(input.payloads.map(body => capture(() => collectWecomMediaCandidates(body))))
}
if (input.chunks) result.chunks = input.chunks.map(v => splitMessageByBytes(v.text, v.limit))
if (input.decrypt) result.decrypt = await Promise.all(input.decrypt.map(v => capture(() => sdk.decryptFile(Buffer.from(v.data, 'base64'), v.key).toString('base64'))))
if (input.filenames) result.filenames = await Promise.all(input.filenames.map(v => capture(async () => {
  const api = new sdk.WeComApiClient(logger)
  api.httpClient.get = async () => ({ data: Buffer.from('physical bytes'), headers: { 'content-disposition': v } })
  const got = await api.downloadFileRaw('https://private.invalid/file')
  return got.filename ?? null
})))
if (input.responses) {
  const source = readFileSync(fileURLToPath(new URL('adapters/wecom/index.ts', root)), 'utf8')
  const start = source.indexOf('class WecomResponse implements ResponseStream')
  const end = source.indexOf('\nconst port:', start)
  if (start < 0 || end < 0) throw new Error('Source response boundary changed')
  const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(start, end))
  result.responses = await Promise.all(input.responses.map(async v => {
    const calls: unknown[] = []
    const replies = new Map(v.frame ? [['U', structuredClone(v.frame)]] : [])
    const client = new sdk.WSClient({ botId: 'private-bot', secret: 'private-secret', logger })
    let fail = false
    client.wsManager.sendReply = async (id, body, cmd = 'aibot_respond_msg') => {
      calls.push({ cmd, ...(cmd === 'aibot_respond_msg' ? { reqId: id } : {}), body })
      if (fail) throw new Error('private ack failure')
      return { errcode: 0 }
    }
    const sendMarkdown = async (chatid, text) => {
      for (const chunk of splitMessageByBytes(text, 4000)) await client.sendMessage(chatid, { msgtype: 'markdown', markdown: { content: chunk } })
    }
    const fixedCrypto = { randomUUID: () => '12345678-1234-4321-9999-abcdefabcdef' }
    const Response = new Function('crypto', 'replyFrames', 'client', 'sendMarkdown', 'utf8Length', 'WECOM_STREAM_BYTE_LIMIT', 'console', code + ';return WecomResponse')(fixedCrypto, replies, client, sendMarkdown, utf8Length, 19000, logger)
    const response = new Response('U')
    const outcomes = []
    for (const a of v.actions) {
      if (a.kind === 'replace') replies.set('U', a.frame)
      else if (a.kind === 'pending') { if (a.value) client.wsManager.pendingAcks.set(v.frame.headers.req_id, {}); else client.wsManager.pendingAcks.clear() }
      else if (a.kind === 'fail') fail = a.value
      else outcomes.push(await capture(() => a.kind === 'append' ? response.append(a.text) : response.finish()))
    }
    return { calls, outcomes }
  }))
}
if (input.protocol) {
  const frames: unknown[] = []
  const client = new sdk.WSClient({ botId: 'private-bot', secret: 'private-secret', logger })
  client.wsManager.sendReply = async (reqId, body, cmd = 'aibot_respond_msg') => {
    frames.push({ cmd, body, ...(cmd === 'aibot_respond_msg' ? { reqId } : {}) })
    if (cmd === 'aibot_upload_media_init') return { body: { upload_id: 'upload-1' } }
    if (cmd === 'aibot_upload_media_finish') return { body: { media_id: 'media-1', type: 'image', created_at: '2026-02-02T02:40:00.123Z' } }
    return { errcode: 0 }
  }
  const resultUpload = await client.uploadMedia(Buffer.from(input.protocol.bytes, 'base64'), { type: 'image', filename: 'physical.png' })
  await client.sendMediaMessage('U', 'image', resultUpload.media_id)
  result.protocol = { frames, result: resultUpload }
}
if (input.downloads) {
  const client = new sdk.WSClient({ botId: 'private-bot', secret: 'private-secret', logger })
  client.apiClient.httpClient.defaults.httpsAgent = new https.Agent({ ca: input.ca })
  const { AttachmentStore } = await import(new URL('adapters/common/attachment/attachment-store.ts', root).href)
  const { WecomMediaService } = await import(new URL('adapters/wecom/media.ts', root).href)
  const store = new AttachmentStore({ root: input.store })
  const media = new WecomMediaService(store, (url, key) => client.downloadFile(url, key))
  result.downloads = []
  for (const candidate of input.downloads) {
    const url = new URL(candidate.url)
    if (!['127.0.0.1', 'localhost'].includes(url.hostname)) throw new Error('private download only')
    result.downloads.push(await capture(async () => {
      const got = await media.downloadCandidate(candidate, 'private-session')
      return { ...got, buffer: got.buffer.toString('base64'), path: got.path.slice(input.store.length) }
    }))
  }
  client.apiClient.httpClient.defaults.httpsAgent.destroy()
}
if (input.uploadCases) result.uploadCases = await Promise.all(input.uploadCases.map(async config => {
  const client = new sdk.WSClient({ botId: 'private-bot', secret: 'private-secret', logger })
  const calls: unknown[] = []
  const attempts = new Map()
  client.wsManager.sendReply = async (_id, body, cmd) => {
    const observed = { ...body }
    if (body.base64_data !== undefined) {
      observed.base64_data = { length: body.base64_data.length, sha256: crypto.createHash('sha256').update(Buffer.from(body.base64_data, 'base64')).digest('hex') }
    }
    calls.push({ cmd, body: observed })
    if (cmd === 'aibot_upload_media_init') return { body: { upload_id: config.missingInit ? undefined : 'upload' } }
    if (cmd === 'aibot_upload_media_finish') return { body: { media_id: config.missingFinish ? undefined : 'media', type: 'image', created_at: 'frozen' } }
    const n = (attempts.get(body.chunk_index) ?? 0) + 1
    attempts.set(body.chunk_index, n)
    if (n <= (config.failCount ?? 0)) throw { errcode: 7, errmsg: 'chunk rejected' }
    return { errcode: 0 }
  }
  const bytes = Buffer.alloc(config.size)
  for (let i = 0; i < bytes.length; i++) bytes[i] = i % 251
  const result = await capture(() => client.uploadMedia(bytes, { type: 'image', filename: 'image.png' }))
  return { calls, result }
}))
process.stdout.write(JSON.stringify(result))
