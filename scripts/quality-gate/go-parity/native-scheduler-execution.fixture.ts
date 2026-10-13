import { mkdirSync, writeFileSync, readFileSync, realpathSync } from 'node:fs'
import { dirname, join } from 'node:path'
import * as crypto from 'node:crypto'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler execution')
if (process.env.CC_HAHA_SCHEDULER_EXECUTION_FIXTURE !== '1') throw new Error('Missing scheduler execution fixture')
const privateSpawn = Bun.spawn.bind(Bun)
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {absent.push(join(directory, '.git'));if (directory === dirname(directory)) break}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
const actualTerminal = await import('../../../src/utils/terminalShellEnvironment')
let current:any, currentNow=input.now, random=0
mock.module('../../../src/utils/terminalShellEnvironment',()=>({...actualTerminal,getProcessEnvWithTerminalShellEnvironment:async()=>({HOME:input.homeDir,CLAUDE_CONFIG_DIR:process.env.CLAUDE_CONFIG_DIR!})}))
const RealDate=Date
class OwnedDate extends RealDate {constructor(value?:any){super(arguments.length ? value : currentNow)}static now(){return currentNow}}
;(globalThis as any).Date=OwnedDate
spyOn(crypto,'randomBytes').mockImplementation((count:number)=>Buffer.alloc(count,++random) as any)
const { SettingsService }=await import('../../../src/server/services/settingsService')
const { SessionService }=await import('../../../src/server/services/sessionService')
const network=await import('../../../src/server/services/networkSettings')
spyOn(SettingsService.prototype,'getAgentTeamsEnabled').mockImplementation(async()=>false)
spyOn(network,'loadNetworkSettings').mockImplementation(async()=>({} as any))
spyOn(network,'buildNetworkEnvironment').mockImplementation(()=>({}))
const metadata:any[]=[],launches:any[]=[]
spyOn(SessionService.prototype,'createSession').mockImplementation(async()=>{if(current.createFailure)throw new Error('session fixture failed');return {sessionId:'00000000-0000-4000-8000-000000000001'} as any})
spyOn(SessionService.prototype,'deleteSessionFile').mockImplementation(async()=>{})
spyOn(SessionService.prototype,'appendSessionMetadata').mockImplementation(async(sid:any,value:any)=>{metadata.push({sid,value})})
const child=join(input.homeDir,'scheduled-child.ts'),capture=join(input.homeDir,'input.json')
// This private physical subprocess is the Source scheduler's lower execution
// boundary. It derives stream output from the actual input; it is not live-model evidence.
// MAIN intentionally leaves stdin open until the terminal result. The lower
// CLI boundary must consume an NDJSON user frame, rather than waiting for EOF.
writeFileSync(child, `import {writeFileSync} from 'node:fs';const reader=Bun.stdin.stream().getReader();const decoder=new TextDecoder();let text='';while(!text.includes('\\n')){const row=await reader.read();if(row.done)throw new Error('missing scheduled user frame');text+=decoder.decode(row.value,{stream:true})}reader.releaseLock();const payload=JSON.parse(text.slice(0,text.indexOf('\\n')));writeFileSync(${JSON.stringify(capture)},JSON.stringify(payload));const prompt=payload.message.content[0].text;console.log(JSON.stringify({type:'assistant',message:{content:[{type:'thinking',thinking:'hidden'},{type:'text',text:'  child:'+prompt+'  '},{type:'tool_use',name:'Read',input:{}}]}}));console.log(JSON.stringify({type:'result',result:'child:'+prompt}));console.error('error:'+prompt);process.exit(Number(process.argv.at(-1)));`)
process.env.CLAUDE_CLI_PATH=child
process.env.CC_HAHA_LOCAL_INDEX='off'
;(Bun as any).spawn=(args:any, options:any)=>{
 if (args[0]!=='bun'||args[1]!==child||options.cwd!==realpathSync(input.homeDir)||options.stdin!=='pipe'||options.stdout!=='pipe'||options.stderr!=='pipe'||options.windowsHide!==true) throw new Error('Unexpected private scheduler child launch')
 const proc=privateSpawn([process.execPath,'--no-env-file',child,String(current.exitCode)],{...options,env:{...options.env,BUN_OPTIONS:'--no-env-file',PATH:'/usr/bin:/bin',TMPDIR:join(input.homeDir,'tmp')}})
 const exited=proc.exited.then((code)=>{currentNow+=current.duration;if(current.failure)throw new Error(current.failure);return code})
 launches.push({args:args.slice(2),env:options.env})
 return {stdin:proc.stdin,stdout:proc.stdout,stderr:proc.stderr,exited,kill:()=>proc.kill()}
}
const {CronService}=await import('../../../src/server/services/cronService')
const {CronScheduler}=await import('../../../src/server/services/cronScheduler')
const store=new CronService(), scheduler=new CronScheduler(store)
mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
mkdirSync(join(process.env.CLAUDE_CONFIG_DIR!,'cc-haha'),{recursive:true})
// Explicit fake custom desktop state disables official OAuth without requesting credentials.
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'cc-haha/settings.json'),JSON.stringify({env:{ANTHROPIC_AUTH_TOKEN:'fake'}}))
const oldLog=console.log;console.log=()=>{}
const rows=[]
for(current of input.rows){
 const task={...current.task,folderPath:input.homeDir}
 writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'scheduled_tasks.json'),JSON.stringify({tasks:[task]}))
 writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'scheduled_tasks_log.json'),JSON.stringify({future:'keep',runs:[]}))
 process.env.CC_HAHA_TASK_TIMEOUT_MS=String(current.timeout??600000)
 const run=await scheduler.executeTask(task,{createSession:current.manual})
 rows.push({run,log:JSON.parse(readFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'scheduled_tasks_log.json'),'utf8')),tasks:await store.listTasks(),input:JSON.parse(readFileSync(capture,'utf8')),metadata:metadata.splice(0),launch:launches.at(-1)})
}
console.log=oldLog
;(globalThis as any).Date=RealDate
process.stdout.write(JSON.stringify({rows,homeDir:input.homeDir,capabilities}))
