import {parse} from 'acorn'
import {assertDisposableFixtureHome} from './fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual teammate keyboard/view transitions')
if(process.env.CC_HAHA_TEAM_NAVIGATION_FIXTURE!=='1')throw new Error('Missing navigation fixture')
async function functions(path:string){const text=new Bun.Transpiler({loader:path.endsWith('tsx')?'tsx':'ts'}).transformSync(await Bun.file(new URL(path,import.meta.url)).text());const tree=parse(text,{ecmaVersion:'latest',sourceType:'module'});const values=new Map<string,string>();for(const outer of tree.body as any[]){const node=outer.type==='ExportNamedDeclaration'?outer.declaration:outer;if(node?.type==='FunctionDeclaration')values.set(node.id.name,text.slice(node.start,node.end))};return values}
const navigation=await functions('../../../src/hooks/useBackgroundTaskNavigation.ts')
const auto=await functions('../../../src/hooks/useTeammateViewAutoExit.ts')
const tasks=await functions('../../../src/tasks/InProcessTeammateTask/InProcessTeammateTask.tsx')
const types=await functions('../../../src/tasks/InProcessTeammateTask/types.ts')
const background=await functions('../../../src/tasks/types.ts')
const helpers=await functions('../../../src/state/teammateViewHelpers.ts')
const terminal=await functions('../../../src/Task.ts')
const helperBodies=['isLocalAgent','release','enterTeammateView','exitTeammateView'].map(name=>helpers.get(name))
if(helperBodies.some(value=>!value)||!terminal.get('isTerminalTaskStatus'))throw new Error('Missing original teammate view helpers')
const realHelpers=Function('logEvent',`const PANEL_GRACE_MS=30000;${terminal.get('isTerminalTaskStatus')}\n${helperBodies.join('\n')}\nreturn {enterTeammateView,exitTeammateView}`)(()=>{})
const required=[navigation.get('stepTeammateSelection'),navigation.get('useBackgroundTaskNavigation'),auto.get('useTeammateViewAutoExit'),tasks.get('getAllInProcessTeammateTasks'),tasks.get('getRunningTeammatesSorted'),types.get('isInProcessTeammateTask'),background.get('isBackgroundTask')]
if(required.some(value=>!value))throw new Error('Missing original navigation function')
const observations=[]
for(const row of input.rows){
  let state={...row.initial},ref:any,actions:any[]=[]
  for(const task of Object.values(state.tasks) as any[])if(task.type==='in_process_teammate')task.currentWorkAbortController={abort:()=>actions.push({Kind:'interrupt',TaskID:task.id})}
  const setAppState=(update:any)=>{state=update(state)}
  const deps={useAppState:(select:any)=>select(state),useSetAppState:()=>setAppState,useRef:(value:any)=>ref??=( {current:value}),useEffect:(effect:any)=>effect(),useInput:()=>{},KeyboardEvent:class{},enterTeammateView:(id:string,set:any)=>{realHelpers.enterTeammateView(id,set);actions.push({Kind:'enter',TaskID:id})},exitTeammateView:(set:any)=>{realHelpers.exitTeammateView(set);actions.push({Kind:'exit',TaskID:''})},InProcessTeammateTask:{kill:(id:string)=>actions.push({Kind:'kill',TaskID:id})}}
  const fn=Function('deps',`const {${Object.keys(deps).join(',')}}=deps;${required.join('\n')}\nreturn {useBackgroundTaskNavigation,useTeammateViewAutoExit}`)(deps)
  fn.useBackgroundTaskNavigation()
  const steps=[]
  for(const step of row.steps){actions=[];let prevented=false
    if(step.tasks){state={...state,tasks:step.tasks};fn.useBackgroundTaskNavigation();fn.useTeammateViewAutoExit()}
    if(step.key){const handler=fn.useBackgroundTaskNavigation({onOpenBackgroundTasks:()=>actions.push({Kind:'background-dialog',TaskID:''})}).handleKeyDown;handler({...step.key,preventDefault:()=>{prevented=true}})}
    steps.push({state:JSON.parse(JSON.stringify(state,(key,value)=>key==='currentWorkAbortController'?undefined:value)),prevented,actions})
  }
  observations.push(steps)
}
process.stdout.write(JSON.stringify({observations}))
