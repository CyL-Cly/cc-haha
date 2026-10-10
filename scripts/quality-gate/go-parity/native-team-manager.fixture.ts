import { spyOn } from 'bun:test'
import { readFileSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities,absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual Team create/delete tools physical transactions')
if(process.env.CC_HAHA_TEAM_MANAGER_FIXTURE!=='1')throw new Error('Missing Team manager fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
process.env.USER_TYPE='external';process.env.CC_HAHA_AGENT_TEAMS_ENABLED='1';delete process.env.CC_HAHA_TEAM_REVIEW_REQUIRED
const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setCwdState(input.homeDir);bootstrap.setOriginalCwd(input.homeDir);bootstrap.setProjectRoot(input.homeDir)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {TeamCreateTool}=await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
const {TeamDeleteTool}=await import('../../../src/tools/TeamDeleteTool/TeamDeleteTool')
const helpers=await import('../../../src/utils/swarm/teamHelpers')
const tasks=await import('../../../src/utils/tasks')
const words=await import('../../../src/utils/words')
const OriginalDate=Date
globalThis.Date=class extends OriginalDate{constructor(value?:any){super(value===undefined?1000:value)}static now(){return 1000}}as DateConstructor
spyOn(words,'generateWordSlug').mockReturnValue('collision-generated')
const rows=[]
for(const row of input.cases){
 bootstrap.switchSession(row.sessionId as any)
 let state:any={teamContext:row.existing?{teamName:row.existing}:undefined,inbox:{messages:[]}}
 const context:any={options:{mainLoopModel:'fixture-model'},getAppState:()=>state,setAppState:(update:any)=>{state=update(state)}}
 let created:any=null,deleted:any=null,error:string|null=null
 try{created=(await TeamCreateTool.call({team_name:row.name,...(row.description!==undefined?{description:row.description}:{}),...(row.agentType?{agent_type:row.agentType}:{})},context)).data
  if(row.delete){await tasks.createTask(tasks.getCanonicalTeamTaskListId(created.team_name),{subject:'Actual visible task',description:'retained',status:'pending',blocks:[],blockedBy:[]});if(row.active){const team=helpers.readTeamFile(created.team_name)!;team.members.push({agentId:`worker@${created.team_name}`,name:'worker',agentType:'reader',joinedAt:1000,model:'fixture-model',cwd:input.homeDir,tmuxPaneId:'',subscriptions:[],backendType:'process',terminated:row.terminated}as any);await helpers.writeTeamFileAsync(created.team_name,team)};deleted=(await TeamDeleteTool.call({},context)).data}
 }catch(e){error=(e as Error).message}
 const name=created?.team_name??row.name
 let physical:string|null=null;try{physical=readFileSync(helpers.getTeamFilePath(name),'utf8')}catch{}
 rows.push({created:created?{team_name:created.team_name,lead_agent_id:created.lead_agent_id}:null,deleted,error,physical,registered:helpers.getRegisteredTeamLifecycle(name)??null})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
