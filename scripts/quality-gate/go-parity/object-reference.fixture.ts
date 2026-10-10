import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Read-only cache identity probe')
if (Object.keys(input).some(key => !['home','kind','userSource','assistantSource'].includes(key)) || !['factory','legacy'].includes(input.kind) || process.env.CC_HAHA_CACHE_IDENTITY_PROBE !== '1' || Bun.version !== '1.3.14' || process.env.NODE_ENV !== 'production' || process.env.TZ !== 'UTC') throw new Error('Original private cache identity inputs required')
fs.mkdirSync(join(home,'.claude'),{recursive:true})
fs.writeFileSync(join(home,'.claude','.claude.json'),'{}')
const capabilities = await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
process.env.CLAUDE_CODE_SIMPLE = '1'
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home); bootstrap.switchSession('identity-main'); bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const {getDefaultAppState} = await import('../../../src/state/AppStateStore')
const messages = await import('../../../src/utils/messages')
const fork = await import('../../../src/utils/forkedAgent')
const stop = await import('../../../src/query/stopHooks')
const side = await import('../../../src/utils/sideQuestion')
const api = await import('../../../src/services/api/claude')
const state = getDefaultAppState()
const original = JSON.parse(input.userSource)
const user = input.kind === 'factory' ? messages.createUserMessage({content:original.message.content,uuid:original.uuid,timestamp:original.timestamp}) : original
const assistant = JSON.parse(input.assistantSource)
const history = [user]
const system = ['original system'], userContext={}, systemContext={}
const toolContext: any = {options:{tools:[],isNonInteractiveSession:true,mainLoopModel:'deepseek-flash',mcpClients:[]},abortController:new AbortController(),getAppState:()=>state,setAppState:(fn:any)=>{throw new Error('Undeclared business state mutation')},messages:history}
const generator=stop.handleStopHooks(history,[assistant],system as any,userContext,systemContext,toolContext,'sdk',false)
const yields=[];let result
for(;;){const next=await generator.next();if(next.done){result=next.value;break}yields.push(JSON.stringify(next.value))}
const saved=fork.getLastCacheSafeParams()!
const view=(phase:string)=>{
 const prepared=messages.normalizeMessagesForAPI(saved.forkContextMessages,[],'fixture-model')
 const sideView=side.prepareSideQuestionContext(saved.forkContextMessages)
 return {phase,sourceRaw:JSON.stringify(user),cacheRaw:JSON.stringify(saved.forkContextMessages[0]),sourceKeys:Object.keys(user),cacheKeys:Object.keys(saved.forkContextMessages[0]),identity:saved.forkContextMessages[0]===user,sideIdentity:sideView[0]===user,inputArrayIdentity:saved.forkContextMessages===history,request:JSON.stringify(api.addCacheBreakpoints(prepared,false,'sdk')),sourceUnchangedByPreparation:JSON.stringify(user)}
}
const observations=[view('after-actual-stop-cache')]
// These are literal finite object operations probing the exported reference
// contract, deliberately NOT a claimed post-cache production producer.
user.origin={kind:'channel',server:'probe'}
observations.push(view('assign-origin'))
user.isVirtual=true
observations.push(view('assign-isVirtual'))
delete user.isVirtual
observations.push(view('delete-isVirtual'))
delete user.origin
user.origin={kind:'channel',server:'readded'}
observations.push(view('delete-readd-origin'))
const originalUUID = user.uuid
delete user.uuid
user.uuid = originalUUID
observations.push(view('delete-readd-uuid-nonfinal-slot'))
const lastCacheBeforeNonMain=saved
const nonMain=stop.handleStopHooks(history,[assistant],system as any,userContext,systemContext,{...toolContext,agentId:'probe-subagent',agentType:'fixture-agent'},'agent:probe',false)
for(;;){const next=await nonMain.next();if(next.done)break}
process.stdout.write(JSON.stringify({kind:input.kind,result,yields,observations,nonMainRetainsLastCache:fork.getLastCacheSafeParams()===lastCacheBeforeNonMain,capabilities,runtime:{bun:Bun.version,revision:Bun.revision}}))
