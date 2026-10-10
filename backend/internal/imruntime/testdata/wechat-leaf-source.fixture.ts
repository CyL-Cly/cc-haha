import { createServer } from 'node:http'
import * as fs from 'node:fs/promises'
import * as path from 'node:path'
import crypto from 'node:crypto'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'private WeChat leaf Source')
if (process.env.CC_HAHA_WECHAT_LEAF_FIXTURE !== '1') throw new Error('Unowned WeChat Source fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalLog = console.log
const warnings: string[] = []
console.log = () => {}
console.warn = (...args) => { warnings.push(args.map(String).join(' ')) }
crypto.randomUUID = () => 'private-fixed-uuid'
crypto.randomBytes = ((size: number) => Buffer.alloc(size, 42)) as any
Date.now = () => 1000
Math.random = () => 0.25
Bun.plugin({ name: 'wechat-leaf-actual-exports', setup(build) {
  build.onLoad({ filter: /adapters\/wechat\/index\.ts$/ }, async args => ({ contents: await Bun.file(args.path).text() + '\nexport { collectAttachments, sendText, sendTypingIndicator, getTypingTicket, contextTokens as fixtureContexts, typingTickets as fixtureTickets }\n', loader: 'ts' }))
  build.onLoad({ filter: /adapters\/wechat\/media\.ts$/ }, async args => ({ contents: await Bun.file(args.path).text() + '\nexport { parseAesKey, decryptAesEcb }\n', loader: 'ts' }))
} })
let row: any, at = 0, requests: any[] = []
const originalFetch = globalThis.fetch
const peer = createServer((req, res) => {
  const logical = req.headers['x-fixture-url'] as string
  const reply = row.replyByURL?.[logical] ?? row.replies?.[at++] ?? { raw: '{}', status: 200 }
  if (reply.hold) return
  res.writeHead(reply.status ?? 200, { 'content-type': 'application/json' })
  res.end(reply.bytes ? Buffer.from(reply.bytes, 'base64') : reply.raw ?? JSON.stringify(reply.body ?? {}))
})
await new Promise<void>(resolve => peer.listen(0, '127.0.0.1', resolve))
const port = (peer.address() as any).port
globalThis.fetch = (async (url: any, init: any = {}) => {
  const logical = String(url), headers = new Headers(init.headers)
  requests.push({ url: logical, method: init.method ?? 'GET', body: init.body ?? '', headers: Object.fromEntries(headers), timed: Boolean(init.signal) })
  const wireHeaders = new Headers(init.headers); wireHeaders.set('x-fixture-url', logical)
  return originalFetch(`http://127.0.0.1:${port}/private`, { ...init, headers: wireHeaders })
}) as typeof fetch
process.env.WECHAT_ACCOUNT_ID = 'private-account'
process.env.WECHAT_BOT_TOKEN = 'private-token'
process.env.WECHAT_BASE_URL = 'https://ilinkai.weixin.qq.com'
process.env.ADAPTER_SERVER_URL = 'ws://127.0.0.1:1'
process.env.PWD = process.env.HOME
const protocol = await import('../../../../adapters/wechat/protocol')
const media = await import('../../../../adapters/wechat/media') as any
const { AttachmentStore } = await import('../../../../adapters/common/attachment/attachment-store')
const index = await import('../../../../adapters/wechat/index') as any
const { WechatTypingController } = await import('../../../../adapters/wechat/typing')
const results: any[] = []
function failure(error: any) {
  if (error instanceof SyntaxError) return '<invalid JSON>'
  if (/endsWith/.test(error.message)) return '<baseUrl type failure>'
  if (/token.*trim/.test(error.message)) return '<token type failure>'
  if (/bad decrypt|wrong final block|WRONG_FINAL_BLOCK_LENGTH|BAD_DECRYPT|Cipher finalization failed|EVP_DecryptFinal/.test(error.message)) return '<decrypt failed>'
  return error.message
}
for (row of input.rows) {
  at = 0; requests = []; warnings.length = 0
  index.fixtureContexts.clear(); index.fixtureTickets.clear()
  const values: any[] = []
  if (row.context !== undefined) index.fixtureContexts.set('private-chat', row.context)
  if (row.kind === 'typing-controller') {
    const sends: any[] = [], pending: Promise<void>[] = [], timers: Array<() => void> = []
    const originalSet = globalThis.setInterval, originalClear = globalThis.clearInterval
    const canceled = new Set<any>()
    globalThis.setInterval = ((callback: () => void, ms: number) => { if (ms !== 5000) throw new Error('Source keepalive interval changed'); timers.push(callback); return callback }) as any
    globalThis.clearInterval = ((timer: any) => { canceled.add(timer) }) as any
    const ctl = new WechatTypingController((chat, status) => { sends.push([chat, status]); const task = index.sendTypingIndicator(chat, status); pending.push(task); return task })
    for (const item of row.actions) { const action = typeof item === 'string' ? item : item.method; if (action === 'start') ctl.start('private-chat'); else if (action === 'stop') ctl.stop('private-chat'); else if (action === 'tick') { for (const timer of timers) if (!canceled.has(timer)) timer() } else ctl.destroy(); await Promise.all(pending) }
    globalThis.setInterval = originalSet; globalThis.clearInterval = originalClear
    values.push({ value: { timers: timers.length, stopped: canceled.size } })
  } else for (const action of row.actions ?? [{ method: row.method }]) {
    try {
      const params: any = { baseUrl: row.base === undefined ? 'https://ilinkai.weixin.qq.com' : row.base, token: row.token === undefined ? ' private-token ' : row.token, getUpdatesBuf: row.cursor, to: row.chat ?? 'private-chat', text: row.text ?? 'Source <汉😀>', contextToken: row.context, ilinkUserId: row.chat ?? 'private-chat', typingTicket: row.ticket, status: row.status ?? 'typing', ...(row.timeout !== undefined ? { timeoutMs: row.timeout } : {}) }
      let value: any
      switch (action.method ?? row.method) {
        case 'updates': value = await protocol.getWechatUpdates(params); break
        case 'text': await protocol.sendWechatText(params); value = null; break
        case 'config': value = await protocol.getWechatConfig(params); break
        case 'typing': await protocol.sendWechatTyping(params); value = null; break
        case 'sender': await index.sendText('private-chat', row.text); value = null; break
        case 'indicator': await index.sendTypingIndicator('private-chat', action.status ?? 'typing'); value = null; break
        case 'extract': value = protocol.extractWechatText(row.items); break
        case 'candidates': value = media.collectWechatMediaCandidates(row.items); break
        case 'key': value = media.parseAesKey(row.key).toString('hex'); break
        case 'collect': {
          index.sessionStore.set('private-chat', 'private-session', process.env.HOME!)
          value = await index.collectAttachments('private-chat', row.candidates)
          const physical: any[] = []
          for (const candidate of row.candidates) {
            const target = path.join(process.env.HOME!, '.claude', 'im-downloads', 'wechat', 'private-session', path.basename(candidate.name))
            try { physical.push({ path: target, bytes: (await fs.readFile(target)).toString('base64') }) } catch {}
          }
          value = { attachments: value, physical }
          await fs.rm(path.join(process.env.HOME!, '.claude', 'im-downloads'), { recursive: true, force: true })
          break
        }
        case 'download': {
          const store = new AttachmentStore({ root: path.join(process.env.HOME!, '.claude', 'im-downloads') })
          const local = await new media.WechatMediaService(store).downloadCandidate(row.candidate, 'private-session')
          value = { kind: local.kind, name: local.name, path: local.path, size: local.size, mimeType: local.mimeType, bytes: local.buffer.toString('base64'), physical: (await fs.readFile(local.path)).toString('base64'), mode: (await fs.stat(local.path)).mode & 0o777 }
          await fs.rm(path.join(process.env.HOME!, '.claude', 'im-downloads'), { recursive: true, force: true })
          break
        }
      }
      values.push({ value })
    } catch (error) { values.push({ error: failure(error) }) }
  }
  results.push({ values, requests, warnings: [...warnings], ticket: index.fixtureTickets.get('private-chat') ?? null, context: index.fixtureContexts.get('private-chat') ?? null })
}
index.typingController.destroy();index.bridge.destroy();index.dedup.destroy()
peer.closeAllConnections()
await new Promise<void>(resolve => peer.close(() => resolve()))
console.log = originalLog
process.stdout.write(JSON.stringify(results))
