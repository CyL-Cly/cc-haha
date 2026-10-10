import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'SDK callback event')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_CALLBACK_EVENT_FIXTURE !== '1') throw new Error('Explicit original SDK callback event fixture required')
await import('../../preload')
Bun.plugin({name:'owned-sdk-event-private-export',setup(build){build.onLoad({filter:/\/src\/utils\/hooks\.ts$/},async args=>({contents:await Bun.file(args.path).text()+'\nexport { executeHookCallback }\n',loader:'ts'}))}})
const input=await Bun.stdin.json()
const state=await import('../../src/bootstrap/state')
state.setOriginalCwd(process.cwd());state.switchSession(input.sid)
const {enableConfigs}=await import('../../src/utils/config');enableConfigs()
const {StructuredIO}=await import('../../src/cli/structuredIO')
const {Stream}=await import('../../src/utils/stream')
const hooks=await import('../../src/utils/hooks')
state.clearRegisteredHooks()
const matchIO=new StructuredIO((async function*(){})())
const first=matchIO.createHookCallback('same-id')
const second=matchIO.createHookCallback('same-id')
state.registerHookCallbacks({PreToolUse:[{matcher:'Read',hooks:[first,second]}]})
const matches=await hooks.getMatchingHooks(undefined,input.sid,'PreToolUse',{session_id:input.sid,transcript_path:'',cwd:process.cwd(),hook_event_name:'PreToolUse',tool_name:'Read',tool_input:{},tool_use_id:input.toolUseID})
if(matches.length!==2||matches[0]?.hook!==first||matches[1]?.hook!==second)throw new Error('Original SDK callback matching failed to retain both identities')
state.clearRegisteredHooks()
const rows:any[]=[]
for(const row of input.rows){
  const inbound=new Stream<string>()
  const io=new StructuredIO(inbound)
  const reading=(async()=>{for await(const _ of io.structuredInput){}})()
  const callback=io.createHookCallback(row.id)
  const hookInput={session_id:input.sid,transcript_path:'',cwd:process.cwd(),hook_event_name:row.event,...row.fields}
  const operation=hooks.executeHookCallback({toolUseID:input.toolUseID,hook:callback,hookEvent:row.event,hookInput,signal:new AbortController().signal,hookIndex:0})
  const request:any=(await io.outbound.next()).value
  inbound.enqueue(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:request.request_id,response:row.output}})+'\n')
  let result:any,error:string|undefined
  try{
    const original=await operation
    const {hook,message,...fields}=original
    result={fields,...(message?{attachment:message.attachment}:{})}
  }catch(failure:any){error=failure.message}
  inbound.done();await reading
  rows.push({request,...(result?{result}:{}),...(error?{error}:{})})
}
process.stdout.write(JSON.stringify(rows)+'\n')
