import * as http from 'node:http'
import * as https from 'node:https'
import * as net from 'node:net'
import { syncBuiltinESMExports } from 'node:module'
import { spyOn } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const originalRequest=http.request,originalConnect=net.Socket.prototype.connect,originalFetch=globalThis.fetch
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'WebFetch HTTP')
if(process.env.CC_HAHA_WEBFETCH_HTTP_FIXTURE!=='1')throw new Error('Missing WebFetch HTTP fixture')
const endpoint=new URL(input.origin)
if(endpoint.hostname!=='127.0.0.1'||endpoint.protocol!=='http:'||!endpoint.port)throw new Error('Invalid fixture origin')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:[...absentManagedSettingsFixturePaths(),"/home/claude/.claude/remote/.oauth_token"]})
const routed=(opts:any,callback:any)=>{
 if(typeof opts!=='object'||!['127.0.0.1','one.fixture','two.fixture','api.anthropic.com','code.claude.com'].includes(opts.hostname))throw new Error('Unsafe WebFetch request')
 return originalRequest({...opts,protocol:'http:',hostname:'127.0.0.1',port:endpoint.port,agent:undefined,headers:{...opts.headers,'X-Fixture-Original-Host':opts.hostname}},callback)
}
for(const module of [http,https]){(require(module===http?'node:http':'node:https')as any).request=routed;spyOn(module,'request').mockImplementation(routed as any)}
net.Socket.prototype.connect=function(...args:any[]){const opts=args[0];if(typeof opts!=='object'||opts.host!=='127.0.0.1'||String(opts.port)!==endpoint.port)throw new Error('Unsafe WebFetch socket');return (originalConnect as any).apply(this,args)}
syncBuiltinESMExports()
if(input.modelCalls){
 process.env.ANTHROPIC_BASE_URL=input.origin;process.env.ANTHROPIC_API_KEY="loopback-fake-webfetch-key";process.env.ANTHROPIC_DEFAULT_HAIKU_MODEL="deepseek-flash";process.env.CLAUDE_CODE_ENTRYPOINT="sdk-cli";process.env.CLAUDE_CODE_ATTRIBUTION_HEADER="0";process.env.CLAUDE_CODE_MAX_RETRIES="0";process.env.CLAUDE_CODE_DISABLE_FAST_MODE="1";process.env.CLAUDE_STREAM_TRANSIENT_RETRY_MAX="0";process.env.CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK="1";
 globalThis.fetch=async function(target:any,options:any){const url=new URL(typeof target==="string"?target:target.url??target);if(url.origin!==input.origin||!url.pathname.endsWith("/messages"))throw new Error("Unsafe WebFetch model target");const request=new Request(target,options);physical.push(JSON.parse(await request.clone().text()));return originalFetch(request)} as typeof fetch
}
await import('../../../preload')
const state=await import('../../../src/bootstrap/state');state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false);if(input.sessionId)state.switchSession(input.sessionId)
const config=await import('../../../src/utils/config');config.enableConfigs();if(input.deviceId)config.saveGlobalConfig((current:any)=>({...current,userID:input.deviceId}))
const {getURLMarkdownContent,clearWebFetchCache}=await import('../../../src/tools/WebFetchTool/utils')
const {WebFetchTool}=await import("../../../src/tools/WebFetchTool/WebFetchTool")
const {resetSettingsCache}=await import('../../../src/utils/settings/settingsCache')
const now=Date.now
Date.now=()=>input.now
Math.random=()=>0.5
const physical:any[]=[]
const rows=[]
for(const row of input.cases){
 if(row.clear)clearWebFetchCache()
 process.env.CC_HAHA_DESKTOP_SERVER_URL=row.desktop?'http://desktop-fixture':''
 const {writeFileSync}=await import('node:fs');writeFileSync(join(input.homeDir,'.claude','settings.json'),JSON.stringify(row.settings??{}));resetSettingsCache()
 const url=row.url.replaceAll('$PORT',endpoint.port)
 try{const value:any=row.call?(await WebFetchTool.call({url,prompt:row.prompt??"extract"},{abortController:new AbortController(),options:{isNonInteractiveSession:true}} as any)).data:await getURLMarkdownContent(url,new AbortController());if(value.persistedPath){value.persistedBytes=Array.from(readFileSync(value.persistedPath));value.persistedPath=value.persistedPath.replace(/webfetch-[^.]+/,'webfetch-ID')};rows.push(value)}catch(error){rows.push({error:(error as Error).message,name:(error as Error).name})}
}
Date.now=now
process.stdout.write(JSON.stringify({rows,physical,capabilities}))
