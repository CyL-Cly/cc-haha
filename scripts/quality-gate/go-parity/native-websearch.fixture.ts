import { readFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const originalFetch=globalThis.fetch
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'WebSearch')
if(process.env.CC_HAHA_WEBSEARCH_FIXTURE!=='1')throw new Error('Missing WebSearch fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:[...absentManagedSettingsFixturePaths(),"/home/claude/.claude/remote/.oauth_token"]})
const endpoint=input.origin?new URL(input.origin):undefined
if(endpoint&&(endpoint.hostname!=='127.0.0.1'||endpoint.protocol!=='http:'))throw new Error('Unsafe search origin')
const physical:any[]=[]
if(input.native){process.env.ANTHROPIC_BASE_URL=input.origin;process.env.ANTHROPIC_API_KEY="loopback-search-fake-key";process.env.CLAUDE_CODE_ENTRYPOINT="sdk-cli";process.env.CLAUDE_CODE_ATTRIBUTION_HEADER="0";process.env.CLAUDE_CODE_MAX_RETRIES="0";process.env.CLAUDE_CODE_DISABLE_FAST_MODE="1";process.env.CLAUDE_STREAM_TRANSIENT_RETRY_MAX="0";process.env.CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK="1"}
if(endpoint)globalThis.fetch=async function(target:any,options:any){const url=new URL(typeof target==='string'?target:target.url??target);if(input.native&&url.origin===input.origin&&url.pathname.endsWith("/messages")){const request=new Request(target,options);physical.push(JSON.parse(await request.clone().text()));return originalFetch(request)};if(!['api.tavily.com','api.search.brave.com'].includes(url.hostname))throw new Error('Unsafe search request');return originalFetch(input.origin+url.pathname+url.search,{...options,headers:{...options?.headers,'X-Fixture-Original-Host':url.hostname}})} as typeof fetch
await import('../../../preload')
const state=await import('../../../src/bootstrap/state');state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false);if(input.sessionId)state.switchSession(input.sessionId)
const config=await import('../../../src/utils/config');config.enableConfigs();if(input.deviceId)config.saveGlobalConfig((current:any)=>({...current,userID:input.deviceId}))
const {cleanMessagesForLogging}=await import("../../../src/utils/sessionStorage")
const progressClean=(input.progressMessages??[]).map((message:any)=>cleanMessagesForLogging([message]))
const backend=await import('../../../src/tools/WebSearchTool/backend')
const {WebSearchTool}=await import('../../../src/tools/WebSearchTool/WebSearchTool')
const source=await readFile(new URL('../../../src/tools/WebSearchTool/WebSearchTool.ts',import.meta.url),'utf8')
const transpiler=new Bun.Transpiler({loader:'ts'})
const start=source.indexOf('function makeOutputFromSearchResponse('),end=source.indexOf('\nexport const WebSearchTool',start)
if(start<0||end<0)throw new Error('Source native reducer changed')
const reduce=new Function('logError',transpiler.transformSync(source.slice(start,end))+';return makeOutputFromSearchResponse')(()=>{})
const now=performance.now.bind(performance);performance.now=()=>1770000000000
const rows=[]
for(const row of input.rows??[]){if(row.unsupported)backend.markAnthropicNativeUnsupported(row.unsupported);const configured=backend.getConfiguredWebSearchSettings(row.settings??{});rows.push({configured,resolved:backend.resolveWebSearchProvider(row.model,configured),enabled:backend.isWebSearchEnabledForModel(row.model,configured),fallback:backend.getFallbackProvider(configured),shouldFallback:backend.shouldFallbackFromNativeError(row.error??'')})}
const external=[]
for(const row of input.external??[]){try{external.push({data:await backend.searchWithExternalProvider(row.provider,row.input,'fake-search-key',new AbortController().signal)})}catch(error){external.push({error:(error as Error).message})}}
const reduced=(input.reducers??[]).map((row:any)=>{const data=reduce(row.blocks,row.query,0);return{data,mapped:WebSearchTool.mapToolResultToToolResultBlockParam(data,'source-search-id')}})
const nativeProgress:any[]=[];let nativeResult:unknown
if(input.native){const {writeFileSync}=await import("node:fs");const {join}=await import("node:path");writeFileSync(join(input.homeDir,".claude","settings.json"),JSON.stringify({webSearch:{mode:"anthropic"}}));const {resetSettingsCache}=await import("../../../src/utils/settings/settingsCache");resetSettingsCache();const {getEmptyToolPermissionContext}=await import("../../../src/Tool");nativeResult=(await WebSearchTool.call(input.native,{options:{mainLoopModel:input.model,thinkingConfig:{type:"disabled"},isNonInteractiveSession:true,agentDefinitions:{activeAgents:[]}},getAppState:()=>({toolPermissionContext:getEmptyToolPermissionContext()}),abortController:new AbortController()} as any,undefined as any,undefined as any,(progress:any)=>nativeProgress.push(progress))).data}
performance.now=now
process.stdout.write(JSON.stringify({rows,external,reduced,progressClean,physical,nativeResult,nativeProgress,capabilities}))
