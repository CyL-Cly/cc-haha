import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Goal state')
if(process.env.CC_HAHA_GOAL_STATE_FIXTURE!=='1')throw new Error('Unowned goal state fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const source=await import('../../../../src/goals/goalState')
const stop=await import('../../../../src/query/stopHooks')
const continuations=(input.continuations??[]).map((reason:string)=>stop.formatGoalContinuationStatusOutput(reason))
const parsed=input.arguments.map((args:string)=>{try{return source.parseGoalCommand(args)}catch(e:any){return {error:e.message}}})
const outputs=input.contents.map((content:string)=>({prompt:source.isGoalPromptHookCommand(content),objective:source.goalObjectiveFromHookCommand(content),local:source.isGoalLocalCommandOutputContent(content)}))
const appState:any={sessionHooks:new Map()};const context:any={setAppState:(updater:any)=>updater(appState)}
const saved:any[]=[];const rows=[]
for(const action of input.actions){
 let result:any
 if(action.type==='set'){result=source.setThreadGoalHook(context,action.sid,action.objective,action.now);const callback=appState.sessionHooks.get(action.sid)?.hooks.Stop?.[0]?.hooks.find((entry:any)=>entry.hook===result.hook)?.onHookSuccess;saved.push(callback)}
 if(action.type==='clear')result=source.clearThreadGoalHook(context,action.sid)
 if(action.type==='restore'){const before=source.getThreadGoal(action.sid);result=source.ensureThreadGoalHookFromTranscript(context,action.sid,action.messages,action.now);if(result&&result!==before)saved.push(appState.sessionHooks.get(action.sid)?.hooks.Stop?.[0]?.hooks.find((entry:any)=>entry.hook===result.hook)?.onHookSuccess)}
 if(action.type==='success')saved[action.index]?.()
 rows.push(JSON.parse(JSON.stringify({result:result??null,current:source.getThreadGoal(action.sid),hooks:appState.sessionHooks.get(action.sid)?.hooks.Stop?.flatMap((m:any)=>m.hooks.map((h:any)=>h.hook))??[]})))
}
if(capabilities.blocked.length)throw new Error('Blocked original goal state:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({parsed,outputs,rows,continuations})+'\n')
