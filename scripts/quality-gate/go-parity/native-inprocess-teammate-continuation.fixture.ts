import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual teammate continuation/result')
if(process.env.CC_HAHA_INPROCESS_CONTINUATION_FIXTURE!=='1')throw new Error('private continuation fixture only')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const runner=await import('../../../src/utils/swarm/inProcessRunner')
const mailbox=await import('../../../src/utils/teammateMailbox')
process.stdout.write(JSON.stringify({rows:input.histories.map((history:any)=>({result:runner.getTeammateTurnResult(history)??null,failure:runner.classifyTeammateTurnFailure(history)??null})),caps:input.caps.map((row:any)=>({result:mailbox.capIdleResult(row.result,row.reachable),failure:mailbox.capIdleFailureReason(row.failure)})),unicode:(input.unicode??[]).map((row:any)=>{const result=runner.getTeammateTurnResult(row.history);const failure=runner.classifyTeammateTurnFailure(row.history);const units=(text:any)=>text==null?null:Array.from({length:text.length},(_,i)=>text.charCodeAt(i));return {result:units(result),reason:units(failure?.reason),cappedResult:units(mailbox.capIdleResult(row.result,row.reachable)),cappedFailure:units(mailbox.capIdleFailureReason(row.failure))}}),capabilities}))
