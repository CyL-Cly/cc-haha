import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual bounded SDK state queue')
if(process.env.CC_HAHA_NATIVE_SESSION_EVENTS_FIXTURE!=='1')throw new Error('Missing state queue fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
const session=await import('../../../src/utils/sessionState')
const queue=await import('../../../src/utils/sdkEventQueue')
const rows=[]
for(const row of input.rows){
  state.setIsInteractive(row.interactive)
  session.setSessionStateChangedListener(null);session.setSessionMetadataChangedListener(null)
  session.notifySessionStateChanged('idle');queue.drainSdkEvents()
  const effects:any[]=[]
  session.setSessionStateChangedListener((value,details)=>effects.push({state:value,...details?{details}: {}}))
  session.setSessionMetadataChangedListener(metadata=>effects.push({metadata}))
  for(const step of row.steps){process.env.CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=step.flag;session.notifySessionStateChanged(step.state,step.details)}
  const events=queue.drainSdkEvents().map(({uuid,session_id,...event})=>event)
  rows.push({effects,events,current:session.getSessionState()})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
