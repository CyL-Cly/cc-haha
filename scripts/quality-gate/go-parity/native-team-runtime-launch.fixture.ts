import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { readFileSync,writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities,absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Team runtime actual durable launch')
if(process.env.CC_HAHA_TEAM_LAUNCH_FIXTURE!=='1')throw new Error('Missing Team launch fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const {conversationService}=await import('../../../src/server/services/conversationService')
const helpers=await import('../../../src/utils/swarm/teamHelpers')
const tasks=await import('../../../src/utils/tasks')
const mailbox=await import('../../../src/utils/teammateMailbox')
const runtime=await import('../../../src/server/services/teamPlanRuntime')
const OriginalDate=Date
globalThis.Date=class extends OriginalDate{constructor(value?:any){super(value===undefined?2000:value)}static now(){return 2000}}as DateConstructor
let interval:(()=>void)|undefined
const timers=spyOn(globalThis,'setInterval').mockImplementation(((callback:()=>void)=>{interval=callback;return {unref(){}}})as any)
spyOn(globalThis,'clearInterval').mockImplementation((()=>{})as any)
const rows=[]
for(const row of input.cases){
 const name=row.plan.teamName,plan=structuredClone(row.plan),team={name,leadSessionId:plan.sessionId,leadAgentId:`team-lead@${name}`,createdAt:1000,members:[],future:{keep:true}}
 await helpers.writeTeamFileAsync(name,team as any)
 plan.incarnationId=crypto.createHash('sha256').update(JSON.stringify([name,plan.sessionId,1000])).digest('hex')
 writeFileSync(join(helpers.getTeamDir(name),'plan.json'),JSON.stringify(plan))
 if(row.seed)for(const task of row.seed)await tasks.createTask(tasks.getCanonicalTeamTaskListId(name),task)
 const calls:any[]=[],connected=new Set([plan.sessionId]),children:string[]=[],observers=new Map<string,(message:any)=>void>(),prepare:any[]=[],releases:any[]=[]
 const patches=[
  spyOn(conversationService,'waitForTeamWorkersStopped').mockImplementation(async()=>{calls.push(['wait'])}),
  spyOn(conversationService,'hasSession').mockImplementation(id=>connected.has(id)),
  spyOn(conversationService,'getSessionPermissionMode').mockReturnValue('default'),
  spyOn(conversationService,'startSession').mockImplementation(async(id,workDir,_url,options)=>{children.push(id);prepare.push({id,workDir,options});calls.push(['prepare',children.length]);if(row.failPrepare===children.length)throw new Error('actual prepare failure');connected.add(id)}),
  spyOn(conversationService,'requestControl').mockImplementation(async(id,request)=>{if(id===plan.sessionId)calls.push(['snapshot',request]);else calls.push(['model',children.indexOf(id)+1,request]);return {}}),
  spyOn(conversationService,'stopSessionAndWait').mockImplementation(async id=>{calls.push(['stop',children.indexOf(id)+1]);connected.delete(id)}),
  spyOn(conversationService,'onOutput').mockImplementation((id,callback)=>{observers.set(id,callback)}),
  spyOn(conversationService,'sendMessage').mockImplementation(async(id,text,_uuid,options)=>{if(id===plan.sessionId){calls.push(['leader',text]);return true};releases.push({id,text});calls.push(['release',children.indexOf(id)+1,text,options?.canSend?.()??null]);if(row.failRelease===releases.length)return false;return true}),
 ]
 let result:any=null,error:string|null=null,executionStarted=false
 try{result=await runtime.launchTeamPlanRuntime(plan);if(row.idle){observers.get(children[0])?.({type:'result',is_error:row.idle==='failed',result:'actual output '+ 'x'.repeat(1010)});await new Promise(resolve=>setTimeout(resolve,20))};if(row.inbox){await mailbox.writeToMailbox('worker-one',{from:'worker-two',text:'actual inbox',timestamp:new Date().toISOString()},name);await mailbox.writeToMailbox('worker-one',{from:'legacy',text:'legacy inbox',timestamp:new Date().toISOString()},name);const path=join(input.homeDir,'.claude','teams',name,'inboxes','worker-one.json');const values=JSON.parse(readFileSync(path,'utf8'));delete values[1].id;writeFileSync(path,JSON.stringify(values));interval?.();await new Promise(resolve=>setTimeout(resolve,30))};if(row.interrupted){const entries=await tasks.listTasks(tasks.getCanonicalTeamTaskListId(name));await tasks.updateTask(tasks.getCanonicalTeamTaskListId(name),entries[0].id,{status:'in_progress'})};await runtime.stopTeamPlanRuntime(plan.planId)}catch(e){error=(e as Error).message;executionStarted=(e as any).executionStarted===true}
 const normalize=(value:any):any=>{if(typeof value==='string'){const index=children.indexOf(value);if(index>=0)return `<worker-${index+1}>`;if(value.startsWith('mailbox-'))return '<mailbox-id>';return value};if(Array.isArray(value))return value.map(normalize);if(value&&typeof value==='object')return Object.fromEntries(Object.entries(value).map(([k,v])=>[k,normalize(v)]));return value}
 rows.push(normalize({result,error,executionStarted,calls,prepare,releases,config:helpers.readTeamFile(name),tasks:await tasks.listTasks(tasks.getCanonicalTeamTaskListId(name)),inbox:await mailbox.readMailbox('worker-one',name),leaderInbox:await mailbox.readMailbox('team-lead',name)}))
 for(const patch of patches)patch.mockRestore()
}
timers.mockRestore()
await new Promise<void>(resolve=>process.stdout.write(JSON.stringify({rows,capabilities}),()=>resolve()))
process.exit(0)
