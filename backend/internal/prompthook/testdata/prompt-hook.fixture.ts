import {mock} from 'bun:test'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Prompt hook')
if(process.env.CC_HAHA_PROMPT_HOOK_FIXTURE!=='1')throw new Error('Unowned prompt hook fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const physicalApi=await import('../../../../src/services/api/claude')
let current:any;let captured:any
mock.module('../../../../src/services/api/claude',()=>({...physicalApi,queryModelWithoutStreaming:async(args:any)=>{
 captured=JSON.parse(JSON.stringify({...args,signal:undefined}))
 if(current.waitAbort){if(args.signal.aborted)throw new Error('aborted');await new Promise((_,reject)=>args.signal.addEventListener('abort',()=>reject(new Error('aborted')),{once:true}))}
 if(current.error)throw new Error(current.error)
 return {type:'assistant',message:{content:current.blocks??[{type:'text',text:current.response}]}}
}}))
const {execPromptHook}=await import('../../../../src/utils/hooks/execPromptHook')
const rows=[]
for(const row of input.rows){
 current=row;captured=undefined
 const controller=new AbortController();if(row.parentAborted)controller.abort()
 let length=4
 const context:any={options:{tools:[]},getAppState:()=>({toolPermissionContext:{mode:'default'}}),setResponseLength:(update:any)=>{length=update(length)}}
 const result=await execPromptHook(row.hook,'Stop','Stop','{"session_id":"actual-session"}',controller.signal,context,row.messages??[],'actual-tool-id')
 if(result.message){const {uuid,timestamp,...rest}=result.message;result.message=rest as any}
 rows.push({result,captured,length})
}
if(capabilities.blocked.length)throw new Error('Blocked original prompt hook:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
