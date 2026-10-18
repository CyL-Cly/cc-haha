import {mkdir,readFile,writeFile,stat} from 'node:fs/promises'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original TeamsDialog file consumers')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_TEAMS_FILES_SOURCE!=='1')throw new Error('Missing original Teams file guard')
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const helpers=await import('../../../src/utils/swarm/teamHelpers')
const {getTeammateStatuses}=await import('../../../src/utils/teamDiscovery')
const rows=[]
for(const row of input.rows){
 const path=helpers.getTeamFilePath(row.name)
 if(row.initial!==undefined){await mkdir(helpers.getTeamDir(row.name),{recursive:true});await writeFile(path,row.initial)}
 const steps=[]
 for(const step of row.steps){
  let result:any=null,error:any=null,unchanged:any=null
  let before:any
  try{before=(await stat(path)).mtimeMs}catch{}
  try{
   if(step.op==='statuses')result=getTeammateStatuses(row.name)
   else if(step.op==='remove')result=await helpers.removeMemberFromTeam(row.name,step.pane)
   else if(step.op==='mode')result=await helpers.setMemberMode(row.name,step.name,step.mode)
   else if(step.op==='modes')result=await helpers.setMultipleMemberModes(row.name,step.updates)
   else throw new Error('Unknown original Teams file step')
  }catch(value){error=(value as Error).message}
  let physical:any=null,mode:any=null
  try{physical=await readFile(path,'utf8');const info=await stat(path);mode=info.mode&0o777;unchanged=before===info.mtimeMs}catch{}
  steps.push({result,error,physical,mode,unchanged})
 }
 rows.push(steps)
}
if(caps.blocked.length)throw new Error('Original Teams file boundary '+JSON.stringify(caps.blocked))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({rows,blocked:caps.blocked}),error=>error?reject(error):resolve()))
process.exit(0)
