import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { Module } from 'node:module'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { mock } from 'bun:test'
import * as actualCrypto from 'node:crypto'
assertDisposableFixtureHome(process.env.HOME, 'Feishu transport Source')
if (process.env.CC_HAHA_FEISHU_TRANSPORT_FIXTURE !== '1') throw new Error('Unowned Feishu transport fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = console.info = console.debug = console.trace = () => {}
let clock = input.now ?? 1770000000000
const OriginalDate = Date
class FixtureDate extends OriginalDate { constructor(...args: any[]) { super(...(args.length ? args : [clock]) as [any]) } static now() { return clock } }
globalThis.Date = FixtureDate as any
const intervals: any[] = []
const originalInterval = globalThis.setInterval
globalThis.setInterval = ((...args: any[]) => { const t = (originalInterval as any)(...args); intervals.push(t); return t }) as any
const indexPath = Bun.resolveSync('../../adapters/feishu/index.ts', import.meta.dir)
const sdkPath = Bun.resolveSync('@larksuiteoapi/node-sdk', dirname(indexPath))
Bun.plugin({ name: 'actual-feishu-transport-exports', setup(build) {
  build.onLoad({ filter: /[\/]adapters[\/]feishu[\/]index\.ts$/ }, args => ({ contents: readFileSync(args.path, 'utf8') + '\nexport { sendText as edgeSendText, sendCard as edgeSendCard, buildPermissionCard as edgePermissionCard, buildProjectPickerCard as edgeProjectCard };\n', loader: 'ts' }))
} })
// Bun's onLoad CJS wrapper loses the SDK exports. Compile the unmodified
// installed CommonJS body with its original resolution paths; append only the
// private protobuf export, then expose those actual exports to the producer.
const sdkModule: any = new Module(sdkPath)
sdkModule.filename = sdkPath
sdkModule.paths = (Module as any)._nodeModulePaths(dirname(sdkPath))
sdkModule._compile(readFileSync(sdkPath, 'utf8') + '\nexports.edgeFrame = pbbp2.Frame;\n', sdkPath)
const sdk: any = sdkModule.exports
mock.module(sdkPath, () => sdk)
const configDir = process.env.CLAUDE_CONFIG_DIR!
mkdirSync(configDir, { recursive: true })
writeFileSync(join(configDir, 'adapters.json'), JSON.stringify({ feishu: { appId: input.appId ?? 'fixture-feishu', appSecret: 'fixture-secret', domain: input.domain ?? 'feishu' } }))
// Keep all actual SDK auth/payload/HTTP serializers, replacing only the low
// level destination with this test's owned peer. There is no production URL flag.
if (input.peer) sdk.defaultHttpInstance.interceptors.request.use((req: any) => {
  const target = new URL(req.url)
  if (!['open.feishu.cn', 'open.larksuite.com'].includes(target.hostname)) throw new Error('Unowned Feishu destination')
  req.url = input.peer + target.pathname + target.search
  return req
}, undefined, { synchronous: true })
mock.module('../../adapters/common/ws-bridge', () => ({ WsBridge: class { destroy() {} } }))
const domain = input.domain === 'lark' ? sdk.Domain.Lark : sdk.Domain.Feishu
const client = new sdk.Client({ appId: input.appId ?? 'fixture-feishu', appSecret: 'fixture-secret', appType: sdk.AppType.SelfBuild, domain })
if (input.mode === 'streaming' || input.mode === 'collision') mock.module('node:crypto', () => ({ ...actualCrypto, randomUUID: () => 'fixture-stream-uuid' }))
const index: any = await import(indexPath)
const kit = await import('../../adapters/feishu/cardkit')
const { FeishuMediaService } = await import('../../adapters/feishu/media')
const { AttachmentStore } = await import('../../adapters/common/attachment/attachment-store')
const media = new FeishuMediaService(client, new AttachmentStore())
function failure(err: any) { if (err?.response?.status) return `HTTP:${err.response.status}`; return err?.message ?? String(err) }
function frameJSON(frame: any) {
  const result: any = { SeqID: frame.SeqID, LogID: frame.LogID, service: frame.service, method: frame.method, headers: frame.headers ?? [] }
  for (const name of ['payloadEncoding', 'payloadType', 'LogIDNew']) if (Object.hasOwn(frame, name)) result[name] = frame[name]
  if (Object.hasOwn(frame, 'payload')) result.payload = Buffer.from(frame.payload).toString('base64')
  return result
}
function frameInput(value: any) { return { ...value, ...(value.payload === undefined ? {} : { payload: Buffer.from(value.payload, 'base64') }) } }
let result: any
if (input.mode === 'timeout') {
  process.env.CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS = String(input.timeout)
  let pending: any
  let error = ''
  try {
    await kit.withImCardRequestTimeout('cardElement.content', () => {
      pending = client.cardkit.v1.cardElement.content({ path: { card_id: 'card-held', element_id: 'streaming_content' }, data: { content: 'submitted', sequence: 2 } })
      return pending
    })
  } catch (err) { error = failure(err) }
  const before = await (await fetch(input.peer + '/control/snapshot')).json()
  await fetch(input.peer + '/control/release')
  await pending
  result = { error, before, completed: true }
} else if (input.mode === 'collision') {
  const { StreamingCard } = await import('../../adapters/feishu/streaming-card')
  const card: any = new StreamingCard({ larkClient: client, chatId: 'chat-fixture' })
  const timers = new Map<number, () => void>()
  let next = 0
  const originalTimeout = globalThis.setTimeout, originalClear = globalThis.clearTimeout
  globalThis.setTimeout = ((fn: () => void, ms: number, ...args: any[]) => { if (ms <= 2000) { const id = --next; timers.set(id, fn); return id as any }; return originalTimeout(fn, ms, ...args) }) as any
  globalThis.clearTimeout = ((id: any) => { if (timers.has(id)) timers.delete(id); else originalClear(id) }) as any
  await card.ensureCreated()
  card.appendText('first')
  card._getFlushController().cancelPendingFlush()
  const first = card._getFlushController().flush()
  await (await fetch(input.peer + '/control/started')).json()
  card.appendText(' second')
  clock += 100
  const scheduled = [...timers.entries()]
  for (const [id, fn] of scheduled) { timers.delete(id); fn() }
  const during = await (await fetch(input.peer + '/control/snapshot')).json()
  await fetch(input.peer + '/control/release')
  await first
  const reflush = [...timers.entries()]
  for (const [id, fn] of reflush) { timers.delete(id); fn() }
  await card._getFlushController().waitForFlush()
  await card.finalize()
  result = { during, phase: card._getPhase(), sequence: card._getSequence(), text: card._getAccumulatedText() }
  card._getFlushController().cancelPendingFlush()
  globalThis.setTimeout = originalTimeout
  globalThis.clearTimeout = originalClear
} else if (input.mode === 'markdown') {
  const md = await import('../../adapters/feishu/markdown-style')
  result = input.rows.map((row: any) => ({ optimized: md.optimizeMarkdownForFeishu(row.text, row.version), sanitized: md.sanitizeTextForCard(row.text, row.limit), tables: md.findMarkdownTablesOutsideCodeBlocks(row.text) }))
} else if (input.mode === 'cards') {
  const cards = await import('../../adapters/feishu/streaming-card')
  result = input.rows.map((row: any) => row.kind === 'permission' ? index.edgePermissionCard(row.tool, row.input, row.request, row.work) : row.kind === 'initial' ? cards.buildInitialStreamingCard() : row.kind === 'rendered' ? cards.buildRenderedCard(row.text) : cards.buildErrorCard(row.text))
} else if (input.mode === 'streaming') {
  const { StreamingCard } = await import('../../adapters/feishu/streaming-card')
  const card: any = new StreamingCard({ larkClient: client, chatId: 'chat-fixture' })
  let nextTimer = 0
  const timers = new Map<number, { fn: () => void, delay: number, due: number }>()
  const originalTimeout = globalThis.setTimeout, originalClear = globalThis.clearTimeout
  globalThis.setTimeout = ((fn: () => void, ms: number, ...args: any[]) => {
    if (ms <= 2000) { const id = --nextTimer; timers.set(id, { fn, delay: ms, due: clock + ms }); return id as any }
    return originalTimeout(fn, ms, ...args)
  }) as any
  globalThis.clearTimeout = ((timer: any) => { if (timers.has(timer)) timers.delete(timer); else originalClear(timer) }) as any
  const rows = []
  for (const action of input.actions) {
    let error = ''
    try {
      switch (action.method) {
        case 'create': await card.ensureCreated(); break
        case 'append': card.appendText(action.text); break
        case 'reason': card.appendReasoning(action.text); break
        case 'tool': card.startTool(action.id, action.name); break
        case 'done': card.completeTool(action.id, action.name); break
        case 'flush': await card._getFlushController().flush(); break
        case 'tick': {
          clock += action.ms
          const pending = [...timers.entries()]
          for (const [id, timer] of pending) if (timers.has(id) && timer.due <= clock) { timers.delete(id); timer.fn() }
          await card._getFlushController().waitForFlush()
          break
        }
        case 'final': await card.finalize(); break
        case 'abort': await card.abort(new Error(action.text)); break
      }
      await card._getFlushController().waitForFlush()
    } catch (err) { error = failure(err) }
    rows.push({ error, phase: card._getPhase(), cardId: card._getCardId(), messageId: card._getMessageId(), sequence: card._getSequence(), active: card._isCardKitStreamActive(), failures: card.consecutiveStreamFailures, text: card._getAccumulatedText(), reasoning: card._getAccumulatedReasoning(), tools: card._getToolSteps().map((step: any) => ({ ...step })), rendered: card.renderedText(), terminal: card.terminalText(), delays: [...timers.values()].map(t => t.delay) })
  }
  card._getFlushController().cancelPendingFlush()
  globalThis.setTimeout = originalTimeout
  globalThis.clearTimeout = originalClear
  result = rows
} else if (input.mode === 'extract') {
  const { extractInboundPayload } = await import('../../adapters/feishu/extract-payload')
  result = input.rows.map((row: any) => { try { return { value: extractInboundPayload(row.content, row.type) } } catch (err: any) { return { error: /null/.test(err.message) ? 'null' : /flat/.test(err.message) ? 'flat' : 'other' } } })
} else if (input.mode === 'api') {
  const rows = []
  for (const action of input.actions) {
    if (action.now !== undefined) clock = action.now
    try {
      let value: any
      switch (action.method) {
        case 'bot': value = await client.request({ method: 'GET', url: '/open-apis/bot/v3/info' }); break
        case 'text': value = await index.edgeSendText('chat-fixture', action.text, action.reply); break
        case 'card': value = await index.edgeSendCard('chat-fixture', action.card); break
        case 'create': value = await kit.createCardEntity(client, action.card); break
        case 'send': value = await kit.sendCardAsMessage(client, 'chat-fixture', action.id, action.reply, 'fixture-uuid'); break
        case 'stream': value = await kit.streamCardContent(client, action.id, 'streaming_content', action.text, action.sequence); break
        case 'settings': value = await kit.setCardStreamingMode(client, action.id, action.streaming, action.sequence); break
        case 'update': value = await kit.updateCardKitCard(client, action.id, action.card, action.sequence); break
        case 'patch': value = await client.im.message.patch({ path: { message_id: action.id }, data: { content: JSON.stringify(action.card) } }); break
        case 'download': {
          const local = await media.downloadResource({ messageId: 'message-fixture', fileKey: action.id, kind: action.kind, fileName: action.name, sessionId: 'session-fixture' })
          value = { kind: local.kind, name: local.name, size: local.size, mimeType: local.mimeType, bytes: local.buffer.toString('base64'), saved: readFileSync(local.path).toString('base64') }; break
        }
        case 'image': value = await media.uploadImage(Buffer.from(action.bytes, 'base64'), 'image/png'); break
        case 'file': value = await media.uploadFile(Buffer.from(action.bytes, 'base64'), action.name); break
        case 'sendImage': value = await media.sendImageMessage('chat-fixture', action.id); break
        case 'sendFile': value = await media.sendFileMessage('chat-fixture', action.id); break
      }
      rows.push({ value: value ?? null })
    } catch (err: any) { rows.push({ error: failure(err), ...(err?.name === 'CardKitApiError' ? { code: err.code } : {}) }) }
  }
  result = { rows, origin: client.domain }
} else if (input.mode === 'codec') {
  result = input.rows.map((row: any) => {
    try {
      const bytes = row.bytes ? Buffer.from(row.bytes, 'base64') : sdk.edgeFrame.encode(frameInput(row.frame)).finish()
      return { bytes: Buffer.from(bytes).toString('base64'), decoded: frameJSON(sdk.edgeFrame.decode(bytes)) }
    } catch { return { error: true } }
  })
} else if (input.mode === 'event' || input.mode === 'frames' || input.mode === 'ws') {
  const events: any[] = [], frames: any[] = []
  const dispatcher = new sdk.EventDispatcher({ encryptKey: input.encryptKey ?? '', verificationToken: 'fixture-verify' })
  dispatcher.register(Object.fromEntries(['im.message.receive_v1', 'card.action.trigger', 'fixture.fail'].map(name => [name, async (body: any) => {
    events.push({ name, body })
    clock += input.elapsed ?? 0
    if (name === 'fixture.fail') throw new Error('fixture callback failed')
    if (name === 'card.action.trigger') return { toast: { type: 'info', content: '✅ 已允许' } }
  }])))
  if (input.mode === 'event') {
    const rows = []
    for (const row of input.rows) {
      try { const own = JSON.parse(row.raw); const body = row.headers ? Object.assign(Object.create({ headers: row.headers }), own) : own; const value = await dispatcher.invoke(body, { needCheck: row.check }); rows.push({ value: value ?? null }) } catch { rows.push({ error: true }) }
    }
    result = { rows, events }
  } else {
    const ws: any = new sdk.WSClient({ appId: input.appId ?? 'fixture-feishu', appSecret: 'fixture-secret', domain, autoReconnect: false })
    if (input.mode === 'frames') {
      ws.eventDispatcher = dispatcher
      ws.wsConfig.setWSInstance({ readyState: 1, send(bytes: any, callback: any) { frames.push(frameJSON(sdk.edgeFrame.decode(bytes))); callback?.() } })
      const rows = []
      for (const row of input.rows) {
        const f = sdk.edgeFrame.decode(sdk.edgeFrame.encode(frameInput(row)).finish())
        try { if (f.method === 0) await ws.handleControlData(f); else if (f.method === 1) await ws.handleEventData(f); rows.push({ error: false }) } catch { rows.push({ error: true }) }
      }
      ws.wsConfig.setWSInstance(null)
      const state = ws.wsConfig.getWS()
      result = { rows, events, frames, config: { pingInterval: state.pingInterval, reconnectCount: state.reconnectCount, reconnectInterval: state.reconnectInterval, reconnectNonce: state.reconnectNonce } }
    } else {
      await ws.start({ eventDispatcher: dispatcher })
      await (await fetch(input.peer + '/control/done')).json()
      result = { events }
    }
    ws.close({ force: true })
  }
} else throw new Error('Unknown Feishu Source fixture mode')
index.dedup.destroy()
index.bridge.destroy()
for (const timer of intervals) clearInterval(timer)
process.stdout.write(JSON.stringify(result))
