import { readFileSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities,absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual Team terminal cleanup lifecycle')
if(process.env.CC_HAHA_TEAM_CLEANUP_FIXTURE!=='1')throw new Error('Missing Team cleanup fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const helpers=await import('../../../src/utils/swarm/teamHelpers')
const tasks=await import('../../../src/utils/tasks')
const OriginalDate=Date;globalThis.Date=class extends OriginalDate{constructor(v?:any){super(v===undefined?2000:v)}static now(){return 2000}}as DateConstructor
const rows=[]
for(const row of input.cases){
 const name=`cleanup-${row.kind}`,list=tasks.getCanonicalTeamTaskListId(name),team={name,createdAt:1000,leadSessionId:'private-leader',leadAgentId:`team-lead@${name}`,members:[]}
 let expected:any
 if(row.kind!=='absent'){await helpers.writeTeamFileAsync(name,team as any);expected=await tasks.beginTaskListLifecycle(list,{teamName:name,createdAt:1000,leadSessionId:'private-leader'});await tasks.createTask(list,{subject:'Actual terminal task',description:'Preserved',status:'pending',blocks:[],blockedBy:[],metadata:{unknown:true}})}
 if(row.kind==='changed')await helpers.writeTeamFileAsync(name,{...team,createdAt:1001}as any)
 if(row.kind==='stale')expected.generation--
 let result:any=null,error:string|null=null
 try{result=await helpers.cleanupTeamDirectories(name,row.kind==='inferred'?undefined:expected);if(row.kind==='retry')result=await helpers.cleanupTeamDirectories(name,expected)}catch(e){error=(e as Error).message}
 let config:any=null;try{config=JSON.parse(readFileSync(helpers.getTeamFilePath(name),'utf8'))}catch{}
 rows.push({result,error,config,tasks:await tasks.listTasks(list),lifecycle:await tasks.readTaskListLifecycleState(list)})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
