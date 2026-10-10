import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
if (process.env.CC_HAHA_DINGTALK_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const root = new URL('../../', import.meta.url)
const input = JSON.parse(await Bun.stdin.text())
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(input.base)) throw new Error('loopback only')
let now = input.now
Date.now = () => now
Math.random = () => 0.25
const warnings: unknown[] = []
console.warn=(...v)=>{warnings.push(v.map(String))}
const nativeFetch = globalThis.fetch
globalThis.fetch = (url, options) => nativeFetch(input.base + new URL(String(url)).pathname, options)
process.env.CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS = input.timeout ?? '15000'
const { DingTalkAiCardService } = await import(new URL('adapters/dingtalk/ai-card.ts', root).href)
const { splitMessage } = await import(new URL('adapters/common/format.ts', root).href)
const source = readFileSync(fileURLToPath(new URL('adapters/dingtalk/index.ts', root)), 'utf8')
const start = source.indexOf('async function getAccessToken(')
const end = source.indexOf('\nfunction getAiCardBuffer(', start)
if (start < 0 || end < 0) throw new Error('Source index token/text boundary changed')
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(start, end))
const methods = new Function('config', 'DINGTALK_API', 'sessionWebhooks', 'splitMessage', 'console', 'let accessTokenCache=null;' + code + ';return {getAccessToken,sendText}')(
  {dingtalk:input.credentials}, 'https://api.dingtalk.com', new Map([['chat','https://private.example/hook']]), splitMessage, { warn(...v) {warnings.push(v.map(String))} },
)
const service = new DingTalkAiCardService(methods.getAccessToken, input.credentials.clientId)
const cards = new Map()
const values = []
for (const action of input.actions) {
 if (action.now !== undefined) now = action.now
 try {
  let value
  if (action.kind === 'token') value = await methods.getAccessToken()
  else if (action.kind === 'concurrent_tokens') value = await Promise.all([methods.getAccessToken(),methods.getAccessToken()])
  else if (action.kind === 'text') {await methods.sendText('chat', action.text);value=null}
  else if (action.kind === 'create') {value=await service.createForTarget(action.target, action.options);cards.set(action.id,value)}
  else if (action.kind === 'stream') {await service.stream(cards.get(action.id), action.text, action.finished ?? false);value=cards.get(action.id)}
  else if (action.kind === 'finish') {await service.finish(cards.get(action.id),action.text);value=cards.get(action.id)}
  values.push({value:structuredClone(value)})
 } catch(error) { values.push({error:error instanceof Error?error.message:error}) }
}
process.stdout.write(JSON.stringify({values,warnings}))
