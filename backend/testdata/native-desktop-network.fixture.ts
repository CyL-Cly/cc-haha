import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve, join } from 'node:path'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text()) as {home:string,initial:unknown,next:unknown,base:Record<string,string>}
if(process.env.CC_HAHA_DESKTOP_NETWORK_FIXTURE!=='1'||process.env.HOME!==input.home)throw new Error('Private Source network input required')
const root=resolve(import.meta.dir,'../..');await import('../../preload')
mkdirSync(join(input.home,'.claude'),{recursive:true})
writeFileSync(join(input.home,'.claude','settings.json'),JSON.stringify(input.initial))
mkdirSync(join(input.home,'.claude','cc-haha'),{recursive:true})
writeFileSync(join(input.home,'.claude','cc-haha','providers.json'),JSON.stringify({schemaVersion:6,activeId:'fixture',providers:[{id:'fixture',presetId:'custom',name:'private network producer',apiKey:'fake-private-only',baseUrl:'http://127.0.0.1:1',apiFormat:'anthropic',authStrategy:'auth_token',supportsNestedToolResultMedia:true,models:{main:'deepseek-flash',haiku:'deepseek-flash',sonnet:'deepseek-flash',opus:'deepseek-flash',fable:'deepseek-flash'}}]}))
Object.assign(process.env,input.base)
const {conversationService}=await import(join(root,'src/server/services/conversationService'))
const service=conversationService as any
const metadata={firstTokenTimeoutDerived:false,streamMaxDurationDerived:false}
const initial=await service.buildChildEnv(input.home,'ws://127.0.0.1:1/sdk/fixture?token=fake',{providerId:'fixture',model:'deepseek-flash'},undefined,metadata)
const {normalizeNetworkSettings}=await import(join(root,'src/server/services/networkSettings'))
const session={networkRoutingFingerprint:JSON.stringify({timeoutMs:normalizeNetworkSettings(input.initial).aiRequestTimeoutMs,proxyMode:normalizeNetworkSettings(input.initial).proxy.mode,manualProxyUrl:normalizeNetworkSettings(input.initial).proxy.mode==='manual'?normalizeNetworkSettings(input.initial).proxy.url.trim():'',systemProxyUrl:normalizeNetworkSettings(input.initial).proxy.mode==='system'?initial.CC_HAHA_SYSTEM_PROXY_URL?.trim()||'':'',noProxy:initial.no_proxy||initial.NO_PROXY||''}),networkDerivedFirstTokenTimeout:metadata.firstTokenTimeoutDerived,networkDerivedStreamMaxDuration:metadata.streamMaxDurationDerived,pendingOutbound:[] as string[]}
service.sessions.set('fixture',session)
writeFileSync(join(input.home,'.claude','settings.json'),JSON.stringify(input.next))
const delivered=await service.refreshNetworkEnvironmentBeforeTurn('fixture',session)
const selected=Object.fromEntries(Object.entries(initial).filter(([name])=>['API_TIMEOUT_MS','HTTP_PROXY','HTTPS_PROXY','http_proxy','https_proxy','ALL_PROXY','all_proxy','NO_PROXY','no_proxy','CLAUDE_ENABLE_STREAM_WATCHDOG','CLAUDE_STREAM_IDLE_TIMEOUT_MS','CLAUDE_STREAM_MAX_DURATION_MS','CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS','CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS','CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK'].includes(name)))
console.log(JSON.stringify({initial:selected,derived:metadata,delivered,queued:session.pendingOutbound.map(value=>JSON.parse(value)),fingerprint:session.networkRoutingFingerprint}))
service.sessions.delete('fixture')
