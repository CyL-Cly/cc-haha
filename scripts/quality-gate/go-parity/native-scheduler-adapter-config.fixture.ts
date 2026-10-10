import { mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Raw adapter config')
if(process.env.CC_HAHA_SCHEDULER_ADAPTER_CONFIG_FIXTURE!=='1')throw new Error('Missing raw adapter config fixture')
const absent=[...absentManagedSettingsFixturePaths()]
for(let directory=dirname(input.homeDir);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
await import('../../../preload')
const {adapterService}=await import('../../../src/server/services/adapterService')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
const path=join(process.env.CLAUDE_CONFIG_DIR!,'adapters.json'),oldLog=console.error,logs:any[]=[]
console.error=(message:any,error:any)=>logs.push({message,error:error.message})
const rows=[]
for(const initial of input.rows){
 try{unlinkSync(path)}catch{}
 if(initial!==null)writeFileSync(path,initial)
 try{rows.push({value:await adapterService.getRawConfig(),logs:logs.splice(0)})}
 catch(error){rows.push({error:{statusCode:(error as any).statusCode,code:(error as any).code,message:(error as Error).message},logs:logs.splice(0)})}
}
console.error=oldLog
process.stdout.write(JSON.stringify({rows,capabilities}))
