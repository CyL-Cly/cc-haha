import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'SDK Worktree callback')
if (process.env.CC_HAHA_SDK_WORKTREE_FIXTURE !== '1') throw new Error('Unowned SDK Worktree callback fixture')
await import('../../preload')
// Expose the unchanged private original body, as the other Source fixtures do.
Bun.plugin({name:'owned-sdk-worktree-private-export',setup(build){build.onLoad({filter:/\/src\/utils\/hooks\.ts$/},async args=>({contents:await Bun.file(args.path).text()+'\nexport { executeHooksOutsideREPL }\n',loader:'ts'}))}})
const input = await Bun.stdin.json()
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(process.cwd())
state.switchSession(input.sid)
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const { StructuredIO } = await import('../../src/cli/structuredIO')
const { Stream } = await import('../../src/utils/stream')
const hooks = await import('../../src/utils/hooks')
const rows = []
for (const row of input.rows) {
  state.clearRegisteredHooks()
  const inbound = new Stream<string>()
  const io = new StructuredIO(inbound, row.replay)
  const seen: unknown[] = []
  const parsed = (async () => { for await (const value of io.structuredInput) seen.push(value) })()
  const callback = io.createHookCallback(row.id, row.timeout)
  state.registerHookCallbacks({ [row.event]: [{ matcher: 'nonmatching-is-ignored-for-worktree', hooks: [callback] }] })
  const controller = new AbortController()
  if (row.preAbort) controller.abort()
  // These are actual public callback and outside-REPL consumers. The peer only
  // supplies protocol frames; no hook or SDK business function is replaced.
  const operation = row.wrapper ? (row.event === 'WorktreeCreate' ? hooks.executeWorktreeCreateHook('agent-a1234567') : hooks.executeWorktreeRemoveHook(input.path)) : hooks.executeHooksOutsideREPL({ hookInput: {session_id:input.sid,transcript_path:'',cwd:process.cwd(),hook_event_name:row.event,...(row.event==='WorktreeCreate'?{name:'agent-a1234567'}:{worktree_path:input.path})}, signal:controller.signal, timeoutMs:1000 })
  let request: any
  if (!row.preAbort || row.wrapper) {
    const frame = await io.outbound.next()
    request = frame.value
    if (row.abort) controller.abort()
    else if (row.eof) inbound.done()
    else if (!row.hold) {
      const packet = { type:'control_response',response: {subtype:row.error?'error':'success',...(row.camel ? {requestId:request.request_id}:{request_id:request.request_id}),...(row.error?{error:'private peer error'}:{response:row.response})}}
      inbound.enqueue(JSON.stringify(packet)+'\n')
      if (row.duplicate) inbound.enqueue(JSON.stringify(packet)+'\n')
      if (row.orphan) inbound.enqueue(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:'unknown629',response:{}}})+'\n')
    }
  }
  const keeper = row.hold ? setInterval(() => {}, 1000) : undefined
  const result = await operation
  if (keeper !== undefined) clearInterval(keeper)
  let cancel: any
  if (row.abort || row.hold) cancel=(await io.outbound.next()).value
  inbound.done()
  await parsed
  rows.push({name:row.name,request,result,cancel,parsed:seen})
}
state.clearRegisteredHooks()
process.stdout.write(JSON.stringify({rows})+'\n')
