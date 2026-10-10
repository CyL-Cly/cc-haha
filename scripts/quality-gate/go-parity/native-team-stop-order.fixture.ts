import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual MAIN teammate Stop task/idle order')
if(process.env.CC_HAHA_TEAM_STOP_ORDER_FIXTURE!=='1')throw new Error('Missing Team stop fixture')
const absent=[...absentManagedSettingsFixturePaths()]
for(let p=dirname(input.homeDir);;p=dirname(p)){absent.push(join(p,'.git'));if(dirname(p)===p)break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setCwdState(input.homeDir);bootstrap.setOriginalCwd(input.homeDir);bootstrap.setProjectRoot(input.homeDir);bootstrap.setIsInteractive(false);bootstrap.setMainThreadAgentType('worker')
const config=await import('../../../src/utils/config');config.enableConfigs()
const {getDefaultAppState}=await import('../../../src/state/AppStateStore')
const {createUserMessage,createAssistantMessage}=await import('../../../src/utils/messages')
const tasks=await import('../../../src/utils/tasks')
const teammate=await import('../../../src/utils/teammate')
const hooks=await import('../../../src/utils/hooks')
const originalCompleted=hooks.executeTaskCompletedHooks,originalIdle=hooks.executeTeammateIdleHooks
let calls:any[]=[]
spyOn(hooks,'executeTaskCompletedHooks').mockImplementation(async function*(...args:Parameters<typeof originalCompleted>){calls.push({event:'TaskCompleted',id:args[0],subject:args[1],description:args[2],name:args[3],team:args[4],mode:args[5]});yield* originalCompleted(...args)})
spyOn(hooks,'executeTeammateIdleHooks').mockImplementation(async function*(...args:Parameters<typeof originalIdle>){calls.push({event:'TeammateIdle',name:args[0],team:args[1],mode:args[2]});yield* originalIdle(...args)})
const {handleStopHooks}=await import('../../../src/query/stopHooks')
const {isLoggableMessage}=await import('../../../src/utils/sessionStorage')
const observations=[]
for(const [index,row]of input.rows.entries()){
  calls=[]
  const team=`actual-stop-${index}`
  bootstrap.switchSession(`10000000-0000-4000-8000-${String(index).padStart(12,'0')}` as any)
  teammate.setDynamicTeamContext({agentId:`worker@${team}`,agentName:'worker',teamName:team,planModeRequired:false})
  for(const task of row.tasks)await tasks.createTask(tasks.getCanonicalTeamTaskListId(team),task)
  let app=getDefaultAppState();app={...app,toolPermissionContext:{...app.toolPermissionContext,mode:row.mode}}
  const context:any={options:{tools:[],commands:[],mainLoopModel:'fixture-model'},getAppState:()=>app,setAppState:(update:any)=>{app=update(app)},abortController:new AbortController(),messages:[]}
  const generator=handleStopHooks([createUserMessage({content:'Actual task'})],[createAssistantMessage({content:[{type:'text',text:'Actual final'}]})],['Actual system'] as any,{}, {},context,'sdk',false)
  const messages=[];let result
  while(true){const next=await generator.next();if(next.done){result=next.value;break};messages.push(next.value)}
  observations.push({calls,result,messages:messages.map(({uuid,timestamp,...message}:any)=>message)})
}
process.stdout.write(JSON.stringify({observations,progressLoggable:isLoggableMessage({type:'progress',data:{type:'hook_progress'}} as any),capabilities}))
