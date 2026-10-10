import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { Module } from 'node:module'
import * as actualCrypto from 'node:crypto'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Feishu runtime Source')
if (process.env.CC_HAHA_FEISHU_RUNTIME_FIXTURE !== '1') throw new Error('Unowned Feishu runtime fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = console.info = console.debug = console.trace = () => {}
const intervals: any[] = []
const originalInterval = globalThis.setInterval
globalThis.setInterval = ((...args: any[]) => { const t = (originalInterval as any)(...args); intervals.push(t); return t }) as any
const OriginalDate = Date
class FixtureDate extends OriginalDate { constructor(...args: any[]) { super(...(args.length ? args : [input.now]) as [any]) } static now() { return input.now } }
globalThis.Date = FixtureDate as any
process.env.ADAPTER_SERVER_URL = input.server
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify(input.config))
const indexPath = Bun.resolveSync('../../adapters/feishu/index.ts', import.meta.dir)
const sdkPath = Bun.resolveSync('@larksuiteoapi/node-sdk', dirname(indexPath))
const sdkModule: any = new Module(sdkPath)
sdkModule.filename = sdkPath
sdkModule.paths = (Module as any)._nodeModulePaths(dirname(sdkPath))
sdkModule._compile(readFileSync(sdkPath, 'utf8'), sdkPath)
const sdk = sdkModule.exports
sdk.defaultHttpInstance.interceptors.request.use((req: any) => {
  const url = new URL(req.url)
  if (!['open.feishu.cn', 'open.larksuite.com'].includes(url.hostname)) throw new Error('Unowned Feishu platform target')
  req.url = input.peer + url.pathname + url.search
  return req
}, undefined, { synchronous: true })
mock.module(sdkPath, () => sdk)
mock.module('node:crypto', () => ({ ...actualCrypto, randomUUID: () => 'fixture-runtime-uuid' }))
Bun.plugin({ name: 'actual-feishu-runtime-exports', setup(build) {
  build.onLoad({ filter: /[\/]adapters[\/]feishu[\/]index\.ts$/ }, args => ({ contents: readFileSync(args.path, 'utf8') + '\nexport { start as edgeStart, getOrCreateStreamingCard as edgeCard }; export const edgeBoot = () => ({botOpenId}); export const edgeStop = () => wsClient?.close({force:true}); export const edgeState = (id: string) => ({...getRuntimeState(id), pending: Array.from(pendingPermissions.get(id) ?? [])});\n', loader: 'ts' }))
} })
if (input.gate) {
  const originalExit=process.exit
  process.exit=((code:number)=>{throw new Error('fixture-exit-'+code)}) as any
  let missing=false
  try{await import(indexPath)}catch(err:any){if(err.message==='fixture-exit-1')missing=true;else throw err}
  process.exit=originalExit
  for(const timer of intervals)clearInterval(timer)
  process.stdout.write(JSON.stringify({missing}))
  process.exit(0)
}
const runtime: any = await import(indexPath)
const { enqueue } = await import('../../adapters/common/chat-queue')
if (input.seed) {
  runtime.sessionStore.set('chat-1', 'session-1', input.work)
  runtime.bridge.connectSession('chat-1', 'session-1')
  runtime.bridge.onServerMessage('chat-1', (m: any) => runtime.handleServerMessage('chat-1', m))
  await runtime.bridge.waitForOpen('chat-1')
}
if(input.bootstrap){await runtime.edgeStart();await fetch(input.peer+'/fixture/wait')}
if(input.concurrent){
  for(const event of input.concurrent)await runtime.handleMessage(event)
  await fetch(input.peer+'/fixture/media-start')
  await fetch(input.peer+'/fixture/release')
  await enqueue('chat-1',async()=>{})
}
const rows: any[] = []
for (const action of input.actions) {
  let value: any, error = ''
  try {
    if (action.kind === 'message') { await runtime.handleMessage(action.event); await enqueue(action.event.message?.chat_id ?? 'chat-1', async () => {}) }
    if (action.kind === 'server') await runtime.handleServerMessage('chat-1', action.message)
    if (action.kind === 'card') value = await runtime.handleCardAction(action.event)
    if (action.kind === 'clear') runtime.clearTransientChatState('chat-1')
    if (action.kind === 'ready') {
      const card = runtime.edgeCard('chat-1')
      // Observe the actual producer completion; no sleep or altered body.
      let turns = 0
      while (card._getPhase() === 'creating') {
        if (++turns > 100000) throw new Error('Original card producer did not complete')
        await new Promise(resolve => setImmediate(resolve))
      }
    }
  } catch (err: any) { error = /null/.test(err.message) ? 'null' : /flat/.test(err.message) ? 'flat' : err.message }
  const state = runtime.edgeState('chat-1')
  rows.push({ error, value: value ?? null, state })
}
await enqueue('chat-1', async () => {})
runtime.edgeStop()
for(const timer of intervals)clearInterval(timer)
runtime.dedup.destroy()
runtime.bridge.destroy()
process.stdout.write(JSON.stringify({ rows, ...(input.includeConfig ? {configBytes:readFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'adapters.json'),'utf8')} : {}), ...(input.bootstrap ? runtime.edgeBoot() : {}), sessions: JSON.parse(readFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapter-sessions.json'), 'utf8')) }))
