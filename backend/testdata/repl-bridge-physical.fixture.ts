import fs from 'node:fs'
import { mock } from 'bun:test'
import { feature } from 'bun:bundle'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.home, 'physical REPL remote bridge')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REPL_BRIDGE_FIXTURE !== '1' || input.bridgeBuildEnabled !== (feature('BRIDGE_MODE')?true:false)) throw new Error('Physical bridge requires explicit private enabled Source build')
const peer = new URL(input.origin)
if (peer.protocol !== 'http:' || peer.hostname !== '127.0.0.1' || !peer.port) throw new Error('Owned loopback only')
const http = require('node:http'), https = require('node:https'), net = require('node:net')
const original = {httpRequest:http.request,httpGet:http.get,httpsRequest:https.request,httpsGet:https.get,socketConnect:net.Socket.prototype.connect,WebSocket:globalThis.WebSocket}
const capabilities = await installClientFixtureCapabilities(home, {absentPaths:[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token'],loopbackFetchOrigin:peer.origin})
const allow = (v:any) => { const u = v instanceof URL ? v : typeof v==='string'?new URL(v):new URL((v.protocol??'http:')+'//'+(v.hostname??v.host)+':'+v.port+(v.path??'/'));if(u.origin!==peer.origin||u.username||u.password)throw new Error('Unowned bridge HTTP'); }
for (const [mod,name,fn] of [[http,'request',original.httpRequest],[http,'get',original.httpGet],[https,'request',original.httpsRequest],[https,'get',original.httpsGet]] as const) mod[name]=(...args:any[])=>{allow(args[0]);return fn(...args)}
net.Socket.prototype.connect=function(...args:any[]){const v=Array.isArray(args[0])?args[0][0]:args[0];const h=typeof v==='object'?v.host??v.hostname:args[1];const port=typeof v==='object'?v.port:v;if(h!=='127.0.0.1'||Number(port)!==Number(peer.port))throw new Error('Unowned bridge socket');return original.socketConnect.apply(this,args)}
const wsOpens:any[]=[]
globalThis.WebSocket=class extends original.WebSocket {constructor(url:any,options:any){const target=new URL(String(url));if(target.protocol!=='ws:'||target.host!==peer.host||target.pathname!=='/v2/session_ingress/ws/session_ownedbridge'||options.headers.Authorization!=='Bearer owned-Source-bridge-oauth')throw new Error('Unowned authenticated bridge WS');wsOpens.push({path:target.pathname,authorization:options.headers.Authorization});super(url,options)}} as any
const defaultSecure={claudeAiOauth:{accessToken:'owned-Source-bridge-oauth',refreshToken:'owned-unused-refresh',expiresAt:2000000000000,scopes:['user:profile','user:inference'],subscriptionType:'max',rateLimitTier:'default_claude_max_5x'}}
const secure=Object.prototype.hasOwnProperty.call(input,'secure')?input.secure:defaultSecure
mock.module(new URL('../../src/utils/secureStorage/index.ts',import.meta.url).pathname,()=>({getSecureStorage:()=>({name:'owned-repl-bridge-store',read:()=>secure,readAsync:async()=>secure,update:()=>{throw new Error('No bridge credential write')},delete:()=>{throw new Error('No bridge credential delete')}})}))
mock.module('../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:null}),clearLegacyApiKeyPrefetch:()=>{},startKeychainPrefetch:()=>{throw new Error('No actual keychain process')},ensureKeychainPrefetchCompleted:async()=>{}}))
// No repository exists in the owned project. Preserve original bridge callers;
// supply only their external git metadata capability's empty physical result.
await import('../../preload')
// Unavailable native Chrome package is outside this probe; no browser tools or native process is admitted.
mock.module('@ant/claude-for-chrome-mcp',()=>({BROWSER_TOOLS:[],createClaudeForChromeMcpServer:()=>{throw new Error('Chrome native capability unavailable')}}))
mock.module('color-diff-napi',()=>({ColorDiff:class {constructor(){throw new Error('Color native capability unavailable')}},ColorFile:class {constructor(){throw new Error('Color native capability unavailable')}},getSyntaxTheme:()=>{throw new Error('Color native capability unavailable')}}))
const originalGit = await import('../../src/utils/git')
mock.module(new URL('../../src/utils/git.ts',import.meta.url).pathname,()=>({...originalGit,getBranch:async()=>'',getRemoteUrl:async()=>null,getDefaultBranch:async()=>''}))
await import('../../preload')
const bootstrap=await import('../../src/bootstrap/state');bootstrap.setCwdState(home);bootstrap.setOriginalCwd(home);bootstrap.setProjectRoot(home);bootstrap.setAllowedSettingSources(['userSettings']);bootstrap.setIsInteractive(true);bootstrap.setMainLoopModelOverride('opus')
const actualAntModels = await import('../../src/utils/model/antModels');Object.assign(globalThis,{resolveAntModel:actualAntModels.resolveAntModel,getAntModelOverrideConfig:actualAntModels.getAntModelOverrideConfig})
const config=await import('../../src/utils/config');config.enableConfigs();config.saveGlobalConfig(c=>({...c,oauthAccount:{accountUuid:'owned-account',organizationUuid:'owned-org',emailAddress:'owned@example.invalid'},remoteControlAtStartup:true,...input.global}))
fs.mkdirSync(join(home,'.claude'),{recursive:true});fs.writeFileSync(join(home,'.claude/settings.json'),JSON.stringify({model:'opus',effortLevel:'low',future:true}))
const {createStore}=await import('../../src/state/store');const {getDefaultAppState,AppStoreContext}=await import('../../src/state/AppState');const {onChangeAppState}=await import('../../src/state/onChangeAppState');const React=await import('react')
const {useReplBridge}=await import('../../src/hooks/useReplBridge');const {useMainLoopModel}=await import('../../src/hooks/useMainLoopModel');const {queryWithModel}=await import('../../src/services/api/claude');const queue=await import('../../src/utils/messageQueueManager');const {getReplBridgeHandle}=await import('../../src/bridge/replBridgeHandle')
const store=createStore({...getDefaultAppState(),mainLoopModel:'opus',mainLoopModelForSession:null,fastMode:false,replBridgeEnabled:true,replBridgeOutboundOnly:false,replBridgeInitialName:'owned physical Bridge'},onChangeAppState)
const internals=(React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;const prior=internals.H;const effects:Array<()=>any>=[];const refs:any[]=[];let refIndex=0
const dispatcher={useContext:(context:any)=>context===AppStoreContext?store:null,useSyncExternalStore:(_s:any,s:any)=>s(),useCallback:(c:any)=>c,useEffect:(e:any)=>effects.push(e),useRef:(v:any)=>refs[refIndex++]??(refs[refIndex-1]={current:v}),useReducer:()=>[0,()=>{}],useMemoCache:(n:number)=>Array(n).fill(Symbol.for('react.memo_cache_sentinel'))}
internals.H=dispatcher
const cleanups:Array<()=>any>=[];const rows:any[]=[]
try{
 useReplBridge([],()=>{}, {current:new AbortController()}, [], 'claude-opus-4-6')
 for(const effect of effects.splice(0)){const cleanup=effect();if(typeof cleanup==='function')cleanups.push(cleanup)}
 const deadline=Date.now()+15000
 if(input.expectedPrompts===0)await Bun.sleep(150)
 while(rows.length<input.expectedPrompts){
  if(Date.now()>deadline)throw new Error('Physical bridge timed out: '+JSON.stringify({state:store.getState().replBridgeError,connected:store.getState().replBridgeConnected,wsOpens,blocked:capabilities.blocked}))
  const cmd=queue.dequeue()
  if(!cmd){await Bun.sleep(10);continue}
  internals.H=dispatcher
  const resolved=useMainLoopModel()
  rows.push({queued:{value:cmd.value,uuid:cmd.uuid,bridgeOrigin:cmd.bridgeOrigin,skipSlashCommands:cmd.skipSlashCommands},rawBase:store.getState().mainLoopModel,forSession:store.getState().mainLoopModelForSession,bootstrapOverride:bootstrap.getMainLoopModelOverride(),resolved})
  await queryWithModel({userPrompt:typeof cmd.value==='string'?cmd.value:'owned blocks',signal:new AbortController().signal,options:{model:resolved,effortValue:'low',fastMode:false,maxOutputTokensOverride:1,querySource:'repl_main_thread',isNonInteractiveSession:false,agents:[],mcpTools:[],hasAppendSystemPrompt:false,enablePromptCaching:false}})
 }
 await getReplBridgeHandle()?.teardown()
 if(capabilities.blocked.length)throw new Error('Source capabilities blocked '+capabilities.blocked.join(', '))
 process.stdout.write(JSON.stringify({rows,wsOpens,handlePresent:getReplBridgeHandle()!==null,bridgeBuildEnabled:feature('BRIDGE_MODE')?true:false,sessionOverride:store.getState().mainLoopModelForSession,settings:JSON.parse(fs.readFileSync(join(home,'.claude/settings.json'),'utf8')),blocked:capabilities.blocked})+'\n')
}finally{internals.H=prior;for(const cleanup of cleanups)cleanup();}
