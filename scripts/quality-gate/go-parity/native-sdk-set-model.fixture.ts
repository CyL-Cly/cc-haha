import { dirname, join } from 'node:path'
import { writeFileSync } from 'node:fs'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual native SDK model switch Source')
if (process.env.CC_HAHA_SDK_SET_MODEL_FIXTURE !== '1' || new URL(input.origin).hostname !== '127.0.0.1') throw new Error('Missing private SDK model switch fixture')
const absent = [...absentManagedSettingsFixturePaths(), join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg', '/Library/Application Support/ClaudeCode/managed-mcp.json']
for (let path = dirname(input.homeDir); ; path = dirname(path)) {
  for (const name of ['.git', '.mcp.json', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) absent.push(join(path, name))
  if (dirname(path) === path) break
}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent, loopbackFetchOrigin: input.origin })
await import('../../../preload')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(input.homeDir, 'policy')
const state = await import('../../../src/bootstrap/state')
process.chdir(input.homeDir)
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd())
state.setIsInteractive(false);state.switchSession('10000000-0000-4000-8000-000000000001' as any)
const settings = await import('../../../src/utils/settings/settings');settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config');config.enableConfigs()
const { createStore } = await import('../../../src/state/store')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const permission = await import('../../../src/utils/permissions/permissionSetup')
const p = await permission.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
const store = createStore({ ...getDefaultAppState(), toolPermissionContext:p.toolPermissionContext })
const requests: unknown[] = [], histories: unknown[] = [], replays: unknown[] = [], controls: unknown[] = []
const engine = await import('../../../src/QueryEngine'), originalAsk = engine.ask
spyOn(engine,'ask').mockImplementation(async function* (args) { histories.push(args.mutableMessages.map((m:any)=>({type:m.type,message:m.message,isMeta:m.isMeta})));yield* originalAsk(args) })
const originalFetch = fetch
globalThis.fetch = (async (url:any, init?:RequestInit) => { if(init?.method==='POST')requests.push(JSON.parse(String(init.body)));return originalFetch(url,init) }) as typeof fetch
let completed = 0, resolveResult: () => void = () => {}
const { Stream } = await import('../../../src/utils/stream'), originalEnqueue = Stream.prototype.enqueue
spyOn(Stream.prototype,'enqueue').mockImplementation(function(message:any) {
  if(message?.type==='user'&&message.isReplay) replays.push({message:message.message,isReplay:message.isReplay,parent_tool_use_id:message.parent_tool_use_id})
  if(message?.type==='control_response') controls.push(message.response)
  if(message?.type==='result') { completed++;resolveResult() }
  return originalEnqueue.call(this,message)
})
process.on('exit',code=>writeFileSync(join(input.homeDir,'model-switch-result.json'),JSON.stringify({code,requests,histories,replays,controls,capabilities})))
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless((async function* () {
  for(let i=0;i<2;i++) {
    if(i) yield JSON.stringify({type:'control_request',request_id:'private-set-model',request:{subtype:'set_model',model:input.requested}})+'\n'
    const wait = new Promise<void>(resolve=> {resolveResult=resolve})
    yield JSON.stringify({type:'user',uuid:`20000000-0000-4000-8000-00000000000${i+1}`,message:{role:'user',content:`ordinary turn ${i+1}`}})+'\n'
    if(completed<=i) await wait
  }
})(),()=>store.getState(),store.setState,[],[],{},[],{ continue:undefined,resume:undefined,resumeSessionAt:undefined,verbose:true,outputFormat:'stream-json',jsonSchema:undefined,permissionPromptToolName:undefined,allowedTools:[],thinkingConfig:{type:'disabled'},maxTurns:1,maxBudgetUsd:undefined,taskBudget:undefined,systemPrompt:'Actual switch system.',appendSystemPrompt:undefined,userSpecifiedModel:'first-model',fallbackModel:undefined,teleport:undefined,sdkUrl:undefined,replayUserMessages:false,includePartialMessages:false,forkSession:undefined,rewindFiles:undefined,enableAuthStatus:false,agent:undefined,workload:undefined })
