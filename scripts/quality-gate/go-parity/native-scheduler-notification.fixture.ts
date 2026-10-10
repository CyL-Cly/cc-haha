import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Scheduler notification')
if(process.env.CC_HAHA_SCHEDULER_NOTIFICATION_FIXTURE!=='1')throw new Error('Missing scheduler notification fixture')
const absent=[...absentManagedSettingsFixturePaths()]
for(let directory=dirname(input.homeDir);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent,loopbackFetchOrigin:input.origin})
const guardedFetch=globalThis.fetch
const requests:any[]=[]
globalThis.fetch=async(target:any,options:any)=>{
 const url=new URL(String(target))
 if(!['https://api.telegram.org','https://open.feishu.cn'].includes(url.origin))throw new Error('Unexpected scheduler notification destination')
 const headers=options.headers??{}
 requests.push({url:url.href,method:options.method,headers,body:JSON.parse(options.body)})
 return guardedFetch(input.origin+(url.origin.includes('telegram')?'/telegram':'/feishu')+url.pathname+url.search,options)
}
await import('../../../preload')
const {sendTaskNotification}=await import('../../../src/server/services/notificationService')
const logs:any[]=[]
const oldLog=console.error,oldWarn=console.warn
console.error=(message:any,details?:any)=>{logs.push({level:'error',message,details:details instanceof Error?details.message:details})}
console.warn=(message:any)=>{logs.push({level:'warn',message})}
mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
const rows=[]
for(const item of input.rows){
 writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'adapters.json'),JSON.stringify(item.config))
 try {await sendTaskNotification(item.run,item.notification);rows.push({requests:requests.splice(0),logs:logs.splice(0)})}
 catch(error){rows.push({error:(error as Error).message,requests:requests.splice(0),logs:logs.splice(0)})}
}
console.error=oldLog;console.warn=oldWarn
process.stdout.write(JSON.stringify({rows,capabilities}))
