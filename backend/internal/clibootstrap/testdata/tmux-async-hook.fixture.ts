import fs from 'node:fs'
import cp from 'node:child_process'
import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await Bun.stdin.text()),home=assertDisposableFixtureHome(input.home,'Actual tmux asynchronous Hook process')
if(process.env.CC_HAHA_TMUX_ASYNC_SOURCE!=='1')throw new Error('Unowned native async source')
const spawnOriginal=cp.spawn,bunSpawnOriginal=Bun.spawn.bind(Bun),source=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/hooks.ts',import.meta.url),'utf8'))
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map(n=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find(n=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing actual '+name);return source.slice(n.start,n.end)}
await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
// Restore only the irreducible private shell primitive, after all filesystem,
// account, public-network and other process capabilities have been denied.
;(Bun as any).spawn=(...args:any[])=>{const list=Array.isArray(args[0])?args[0]:args[0]?.cmd;if(!Array.isArray(list)||list[0]!=='/bin/sh'||!list.some(arg=>typeof arg==='string'&&arg.includes(home)))throw new Error('Source primitive blocked unowned spawn');return (bunSpawnOriginal as any)(...args)}
await import('../../../../preload')
const state=await import('../../../../src/bootstrap/state');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.switchSession('tmux-source-session');state.setIsInteractive(false)
const config=await import('../../../../src/utils/config');config.enableConfigs()
const {hookJSONOutputSchema}=await import('../../../../src/types/hooks')
const {createCombinedAbortSignal}=await import('../../../../src/utils/combinedAbortSignal')
const {TaskOutput}=await import('../../../../src/utils/task/TaskOutput')
const {wrapSpawn}=await import('../../../../src/utils/ShellCommand')
const registry=await import('../../../../src/utils/hooks/AsyncHookRegistry')
const events=await import('../../../../src/utils/hooks/hookEvents')
const {firstLineOf}=await import('../../../../src/utils/stringUtils')
const children:any[]=[]
const notifications:any[]=[]
const register=(value:any)=>registry.registerPendingAsyncHook(value)
const deps:any={
 process,DEFAULT_HOOK_SHELL:'bash',TOOL_HOOK_EXECUTION_TIMEOUT_MS:600000,createCombinedAbortSignal,hookJSONOutputSchema,logError:()=>{},getPlatform:()=>process.platform==='darwin'?'macos':'linux',getProjectRoot:()=>home,getCwd:()=>home,getOriginalCwd:()=>home,subprocessEnv:()=>({...process.env}),pathExists:async(path:string)=>fs.existsSync(path),
 spawn(command:string,args:any,options:any){if(!command.includes(home)||options.cwd!==home||options.env.HOME!==home)throw new Error('Hook primitive escaped private command');const child=spawnOriginal(command,args,options);children.push(child);if(process.env.CC_HAHA_TMUX_DEBUG_SOURCE==='1'){child.stdout.on('data',data=>console.error('PRIVATE_SOURCE_BYTES',data.length));child.on('exit',code=>console.error('PRIVATE_SOURCE_EXIT',code));child.on('error',error=>console.error('PRIVATE_SOURCE_ERROR',error.message))};return child},
 TaskOutput,wrapSpawn,firstLineOf,jsonParse:JSON.parse,jsonStringify:JSON.stringify,isAsyncHookJSONOutput:(v:any)=>v?.async===true,
 registerPendingAsyncHook:register,emitHookResponse:events.emitHookResponse,enqueuePendingNotification:(v:any)=>notifications.push(v),wrapInSystemReminder:(text:string)=>'<system-reminder>\n'+text+'\n</system-reminder>',startHookProgressInterval:events.startHookProgressInterval,
 logForDebugging:(...args:any[])=>{if(process.env.CC_HAHA_TMUX_DEBUG_SOURCE==='1')console.error(...args)},logForDiagnosticsNoPII:()=>{},getErrnoCode:(e:any)=>e.code,errorMessage:(e:any)=>e instanceof Error?e.message:String(e),
}
const build=new Function('dependencies',`with(dependencies){${extract('executeInBackground')}\n${extract('execCommandHook')}\nreturn execCommandHook}`)(deps)
const parsed=new Function('hookJSONOutputSchema','jsonParse','jsonStringify','logForDebugging',extract('validateHookJson')+'\n'+extract('parseHookOutput')+';return parseHookOutput')(hookJSONOutputSchema,JSON.parse,JSON.stringify,()=>{})
deps.execCommandHook=build;deps.getSessionId=()=> 'tmux-source-session';deps.isEnvTruthy=()=>false;deps.shouldDisableAllHooksIncludingManaged=()=>false;deps.shouldSkipHookDueToTrust=()=>false;deps.isInternalHook=()=>false;deps.getPluginHookCounts=()=>undefined;deps.getHookTypeCounts=()=>({command:1});deps.logEvent=()=>{};deps.randomUUID=()=> 'owned-source-hook';deps.parseHookOutput=parsed;deps.isSyncHookJSONOutput=(v:any)=>v?.async!==true
const outside=new Function('dependencies',`with(dependencies){${extract('executeHooksOutsideREPL')};return executeHooksOutsideREPL}`)(deps)
const signal=new AbortController()
const hook={type:'command',command:'exec /bin/sh '+JSON.stringify(input.script),...(input.kind==='config'?{async:true}:{}),...(input.kind==='rewake'?{asyncRewake:true}:{}),timeout:10}
let result:any
try{
 deps.getMatchingHooks=async()=>[{hook}]
 if(input.outside)result=await outside({hookInput:{session_id:'tmux-source-session',hook_event_name:'WorktreeCreate',cwd:home,name:'private'},signal:signal.signal,timeoutMs:600000})
 else result=await build(hook,'WorktreeCreate','WorktreeCreate',JSON.stringify({session_id:'tmux-source-session',hook_event_name:'WorktreeCreate',cwd:home,name:'private'}),signal.signal,'owned-source-hook',0)
 const pending=registry.getPendingAsyncHooks().map(row=>({name:row.hookName,event:row.hookEvent,timeout:row.timeout}))
 fs.writeFileSync(input.result,JSON.stringify({result,pending,notifications,pids:children.map(child=>child.pid)}))
 // The test controls an actual filesystem gate; no synthetic process result.
 while(!fs.existsSync(input.release))await new Promise(resolve=>setTimeout(resolve,5))
 await Promise.all(children.map(child=>child.exitCode!==null?Promise.resolve():new Promise(resolve=>child.once('close',resolve))))
 // Observe the actual rewake producer's queued notification, rather than
 // assuming one event-loop tick is enough to complete its async IO read.
 const completionDeadline=Date.now()+3000
 if(input.kind==='rewake')while(notifications.length===0){if(Date.now()>completionDeadline)throw new Error('Actual Source rewake notification never delivered');await new Promise(resolve=>setTimeout(resolve,5))}
 await new Promise(resolve=>setImmediate(resolve))
 const responses=await registry.checkForAsyncHookResponses()
 await registry.finalizePendingAsyncHooks()
 process.stdout.write(JSON.stringify({result,pending,responses,notifications,pids:children.map(child=>child.pid)})+'\n')
}catch(error){for(const child of children)child.kill('SIGKILL');throw error}
