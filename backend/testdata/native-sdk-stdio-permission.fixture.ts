import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_STDIO_PERMISSION_FIXTURE !== '1') throw new Error('Explicit Source SDK stdio permission fixture required')
const home = assertDisposableFixtureHome(process.env.HOME, 'SDK stdio permission')
await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
const { enableConfigs } = await import('../../src/utils/config'); enableConfigs()
const { StructuredIO } = await import('../../src/cli/structuredIO')
const { Stream } = await import('../../src/utils/stream')
const { outputSchema } = await import('../../src/utils/permissions/PermissionPromptToolResultSchema')
const input = await Bun.stdin.json(), rows = []
for (const row of input.rows) {
 let parsed, error
 try { parsed = outputSchema().parse(row.response) } catch (e: any) { error = { name:e.name, message:e.message } }
 const raw = new Stream<string>(), io = new StructuredIO(raw)
 const reading = (async () => { for await (const _ of io.structuredInput) {} })()
 let appState: any = { toolPermissionContext: { mode:'default', additionalWorkingDirectories:new Map(), alwaysAllowRules:{},alwaysDenyRules:{},alwaysAskRules:{} } }
 const controller = new AbortController()
 const tool: any = { name:'mcp__sdk-p684__read_sdk', userFacingName: () => 'actual SDK reader' }
 const call: any = { abortController:controller, agentId:undefined, messages:[], options:{}, getAppState:()=>appState, setAppState:(f:any)=>{appState=f(appState)} }
 const operation = io.createCanUseTool()(tool, { actual:'physical' }, call, undefined as any, 'permission-tool-P700', { behavior:'ask', suggestions:undefined })
 const event: any = (await io.outbound.next()).value
 if (row.abort) {
  controller.abort()
  if (row.abortThenEOF) raw.done()
  else if (row.abortThenResponse) raw.enqueue(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:event.request_id,response:row.response}})+'\n')
 }
 else if (row.eof) raw.done()
 else raw.enqueue(JSON.stringify({type:'control_response',response:{subtype:row.peerError?'error':'success',request_id:event.request_id,...(row.peerError?{error:'physical peer rejected'}:{response:row.response})}})+'\n')
 const decision = await operation
 let cancel
 if (row.abort) cancel = (await io.outbound.next()).value
 raw.done(); await reading
 event.request_id = '<request>'
 if (cancel) cancel.request_id = '<request>'
 rows.push({name:row.name,parsed,error,request:event,decision,cancel,parentAborted:controller.signal.aborted,pending:io.getPendingPermissionRequests().length,permissionMode:appState.toolPermissionContext.mode,sessionAllowRules:appState.toolPermissionContext.alwaysAllowRules.session})
}
process.stdout.write(JSON.stringify({rows})+'\n')
