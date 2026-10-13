import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import {mock} from 'bun:test'
import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
// Bind the immutable pinned source through its physical path before the capability boundary.
const hooksSourcePath = fs.realpathSync(fileURLToPath(new URL('../../../../src/utils/hooks.ts', import.meta.url)))
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Prompt Stop aggregation')
if(process.env.CC_HAHA_PROMPT_STOP_FIXTURE!=='1')throw new Error('Unowned prompt stop fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const state=await import('../../../../src/bootstrap/state');state.setIsInteractive(false);state.switchSession('goal-source-session')
const config=await import('../../../../src/utils/config');config.enableConfigs()
const api=await import('../../../../src/services/api/claude')
let current:any
mock.module('../../../../src/services/api/claude',()=>({...api,queryModelWithoutStreaming:async(args:any)=>{
 if(current.waitAbort){if(args.signal.aborted)throw new Error('aborted');await new Promise((_,reject)=>args.signal.addEventListener('abort',()=>reject(new Error('aborted')),{once:true}))}
 if(current.error)throw new Error(current.error)
 return {type:'assistant',message:{content:[{type:'text',text:current.response}]}}
}}))
const {execPromptHook}=await import('../../../../src/utils/hooks/execPromptHook')
const physicalHooks=await import('../../../../src/utils/hooks')
const attachments=await import('../../../../src/utils/attachments')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(hooksSourcePath).text())
const ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const executor=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='executeHooks')
const loop=executor?.body.body.find((n:any)=>n.type==='ForOfStatement'&&n.await)
if(!loop)throw new Error('Actual aggregate Source loop missing')
const noop=()=>{}
const aggregate=new Function('all','createAttachmentMessage','logForDebugging','getHookDisplayText','getSessionId','getSessionHookCallback','logError',`return async function* (hookPromises,hookEvent,hookName,toolUseID){let permissionBehavior;const outcomes={success:0,blocking:0,non_blocking_error:0,cancelled:0};const matchingHooks=hookPromises.map(result=>({hook:result.hook,hookSource:'settings'}));const appState=undefined,matchQuery=undefined;${code.slice(loop.start,loop.end)}}`)(async function* (values:any[]){for(const v of values)yield v},attachments.createAttachmentMessage,noop,(hook:any)=>hook.statusMessage||hook.prompt,()=>state.getSessionId(),()=>undefined,noop)
mock.module('../../../../src/utils/hooks',()=>({...physicalHooks,executeStopHooks:async function*(_mode:any,signal:any,_timeout:any,_active:any,_id:any,context:any){
 const display=current.hook.statusMessage||current.hook.prompt
 yield {message:{type:'progress',data:{type:'hook_progress',hookEvent:'Stop',hookName:'Stop',command:display,promptText:current.hook.prompt,...(current.hook.statusMessage!=null?{statusMessage:current.hook.statusMessage}:{})},parentToolUseID:'prompt-stop-id',toolUseID:'prompt-stop-id',timestamp:'source',uuid:'source'}}
 const {createCombinedAbortSignal}=await import('../../../../src/utils/combinedAbortSignal')
 const outer=createCombinedAbortSignal(signal,{timeoutMs:current.hook.timeout?current.hook.timeout*1000:600000})
 const result=await execPromptHook(current.hook,'Stop','Stop','{"session_id":"goal-source-session"}',outer.signal,context,[],'prompt-stop-id');outer.cleanup()
 if(result.message?.type==='attachment'){result.message.attachment.command=display;result.message.attachment.durationMs=0}
 yield* aggregate([result],'Stop','Stop','prompt-stop-id')
}}))
const {handleStopHooks}=await import('../../../../src/query/stopHooks')
const rows=[]
for(const row of input.rows){
 current=row
 const app:any={toolPermissionContext:{mode:'default'},sessionHooks:new Map(),mcp:{clients:[]},tasks:row.teamWork?{worker:{type:'in_process_teammate',status:'running',isIdle:false}}:{},...(row.teamWork?{teamContext:{teamName:'goal-source-team',leadAgentId:'team-lead@goal-source-team',isLeader:true,teammates:{}}}:{})}
 const context:any={options:{tools:[]},getAppState:()=>app,setAppState:(updater:any)=>Object.assign(app,updater(app)),setResponseLength:()=>{},abortController:new AbortController()}
 const generator=handleStopHooks([],[],[],{}, {},context,'sdk')
 const messages=[];let result:any
 for(;;){const step=await generator.next();if(step.done){result=step.value;break};const {uuid,timestamp,...record}=step.value as any;messages.push(record)}
 const strip=(m:any)=>{const {uuid,timestamp,...record}=m;return record}
 rows.push({messages,result:{...result,blockingErrors:result.blockingErrors.map(strip)}})
}
if(capabilities.blocked.length)throw new Error('Blocked Source Prompt Stop:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
