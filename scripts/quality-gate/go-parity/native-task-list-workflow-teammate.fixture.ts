import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { createRequire } from 'node:module'
import { mock } from 'bun:test'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original Workflow/in-process TaskUpdate state callbacks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TASK_WORKFLOW_SOURCE !== '1') throw new Error('Pinned original task caller required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const requests: any[] = []
let cancelledEntered!: () => void, releaseCancelled!: () => void
const cancelledRequest = new Promise<void>(resolve => cancelledEntered = resolve)
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  if (request.method === 'HEAD') return new Response(null, { status: 204 })
  const body = await request.json()
  requests.push(body)
  if (JSON.stringify(body.messages).includes('ACTUAL_CANCEL_WORKFLOW')) {
    cancelledEntered()
    await new Promise<void>(resolve => releaseCancelled = resolve)
    return new Response(null, { status: 499 })
  }
  if (new URL(request.url).pathname !== '/v1/messages' || requests.length > 2) throw new Error('Undeclared original Workflow model request')
  const tool = requests.length === 1
  const events: any[] = [
    ['message_start', { type: 'message_start', message: { id: 'source-task-workflow-' + requests.length, type: 'message', role: 'assistant', model: 'claude-sonnet-4-6', content: [], usage: { input_tokens: 1, output_tokens: 0 } } }],
    ['content_block_start', { type: 'content_block_start', index: 0, content_block: tool ? { type: 'tool_use', id: 'actual-workflow-task-update', name: 'TaskUpdate', input: {} } : { type: 'text', text: '' } }],
    ['content_block_delta', { type: 'content_block_delta', index: 0, delta: tool ? { type: 'input_json_delta', partial_json: JSON.stringify({ taskId: '99', status: 'in_progress' }) } : { type: 'text_delta', text: 'ACTUAL_WORKFLOW_TASK_DONE' } }],
    ['content_block_stop', { type: 'content_block_stop', index: 0 }],
    ['message_delta', { type: 'message_delta', delta: { stop_reason: tool ? 'tool_use' : 'end_turn', stop_sequence: null }, usage: { output_tokens: 1 } }],
    ['message_stop', { type: 'message_stop' }],
  ]
  return new Response(events.map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`).join(''), { headers: { 'content-type': 'text/event-stream', 'request-id': 'actual-workflow-task-source' } })
} })
process.env.ANTHROPIC_BASE_URL = server.url.origin
process.env.ANTHROPIC_API_KEY = 'task-workflow-private-api-key'
process.env.USER_TYPE = 'external'
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'
process.env.CLAUDE_CODE_ENABLE_TASKS = '1'
process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = '1'
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude', 'settings.json'), '{}')
fs.writeFileSync(join(home, '.claude', '.claude.json'), JSON.stringify({ hasCompletedOnboarding: true, showExpandedTodos: false, showSpinnerTree: false, customApiKeyResponses: { approved: [process.env.ANTHROPIC_API_KEY.slice(-20)], rejected: [] }, taskWorkflowFuture: { keep: true } }))
const absent = [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const path of [join(dirname(absent[0]), 'CLAUDE.md'), join(dirname(absent[0]), '.claude/rules')]) {
  if (fs.existsSync(path)) throw new Error('Original task fixture requires verified absent external managed instructions')
  absent.push(path)
}
for (let directory = dirname(home);; directory = dirname(directory)) {
  for (const suffix of ['.git', '.claude/agents', '.claude/skills', '.claude/commands', '.claude/workflows', '.claude/settings.json', '.claude/settings.local.json']) {
    const path = join(directory, suffix)
    if (!fs.existsSync(path)) absent.push(path)
  }
  if (dirname(directory) === directory) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: server.url.origin })
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const fallback = createFallbackStorage({ name: 'owned-unavailable-keychain', read: () => null, readAsync: async () => null, update: () => ({ success: false }), delete: () => false }, plainTextStorage)
mock.module('../../../src/utils/secureStorage/index.ts', () => ({ getSecureStorage: () => fallback }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-task-workflow-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package', VERSION_CHANGELOG: '', FEEDBACK_CHANNEL: 'local' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
bootstrap.setMainLoopModelOverride('claude-sonnet-4-6')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createStore } = await import('../../../src/state/store')
const { onChangeAppState } = await import('../../../src/state/onChangeAppState')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { TaskUpdateTool } = await import('../../../src/tools/TaskUpdateTool/TaskUpdateTool')
const { runWorkflowAgent } = await import('../../../src/utils/workflows/runWorkflowAgent')
const { runAgent } = await import('../../../src/tools/AgentTool/runAgent')
const { createTeammateContext, runWithTeammateContext } = await import('../../../src/utils/teammateContext')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { parseAgentsFromJson } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const definitions = parseAgentsFromJson(input.agents ?? { 'task-context-fixture': { description: 'Original TaskUpdate state callback', prompt: 'Original Workflow TaskUpdate parent state', tools: ['TaskUpdate'] } }, 'flagSettings')
const definition = definitions.find(agent => agent.agentType === 'task-context-fixture')
if (!definition) throw new Error('Actual CLI JSON agent parser rejected task fixture definition')
const makeParent = (store: any) => ({ options: { commands: [], debug: false, mainLoopModel: 'claude-sonnet-4-6', tools: [TaskUpdateTool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [definition], allAgents: [definition] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: store.getState, setAppState: store.setState, setAppStateForTasks: store.setState, setResponseLength: () => {}, setInProgressToolUseIDs: () => {}, messages: [] })
const store = createStore({ ...getDefaultAppState(), expandedView: 'none' }, onChangeAppState)
const outcome = await runWorkflowAgent({ prompt: 'Run the actual TaskUpdate for missing task 99', opts: { agentType: definition.agentType }, toolUseContext: makeParent(store) as any, canUseTool: (async () => ({ behavior: 'allow' })) as any, runId: 'wf_task_callback-actual', abortController: new AbortController(), onAgentId: () => {}, onProgress: () => {} })
const workflow = { outcome, expandedView: store.getState().expandedView, global: config.getGlobalConfig(), requests: [...requests] }

// Preserve the real inProcessRunner runAgent call's async/context properties.
// The observer stops before any additional provider query, then the captured
// original tool context executes the same original TaskUpdate business call.
const source = ts.createSourceFile('inProcessRunner.ts', fs.readFileSync(new URL('../../../src/utils/swarm/inProcessRunner.ts', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
let call: any
function visit(node: any) {
  if (ts.isCallExpression(node) && node.expression.getText(source) === 'runAgent' && node.arguments.length === 1 && node.arguments[0].properties?.some((property: any) => property.name?.getText(source) === 'preserveToolUseResults')) call = node.arguments[0]
  ts.forEachChild(node, visit)
}
visit(source)
if (!call) throw new Error('Original in-process runAgent caller missing')
const compiledCall = ts.transpileModule(`const value = (${call.getText(source)})`, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
const createCall = new Function('iterationAgentDefinition', 'promptMessages', 'toolUseContext', 'createInProcessCanUseTool', 'identity', 'currentWorkAbortController', 'updateTaskState', 'taskId', 'setAppState', 'allowPermissionPrompts', 'forkContextMessages', 'transcriptAgentId', 'recordedUuids', 'description', 'transcriptMetadata', 'currentPermissionMode', 'model', 'allowedTools', 'teammateReplacementState', `${compiledCall}; return value`)
config.saveGlobalConfig(global => ({ ...global, showExpandedTodos: false, showSpinnerTree: false }))
const teammateStore = createStore({ ...getDefaultAppState(), expandedView: 'none' }, onChangeAppState)
const { TeamCreateTool } = await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
await TeamCreateTool.call({ team_name: 'task-source-team', description: 'Actual shared task subscription' }, makeParent(teammateStore) as any)
const React = await import('react')
const { render, Box } = await import('../../../src/ink')
const { AppStoreContext, useAppState } = await import('../../../src/state/AppState')
const { useTasksV2WithCollapseEffect } = await import('../../../src/hooks/useTasksV2')
const { TaskListV2 } = await import('../../../src/components/TaskListV2')
const { ThemeProvider } = await import('../../../src/components/design-system/ThemeProvider')
const repl = ts.createSourceFile('REPL.tsx', fs.readFileSync(new URL('../../../src/screens/REPL.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let taskRegion: any
function visitRegion(node: any) {
  if (ts.isJsxExpression(node) && node.expression?.getText(repl).startsWith('!showSpinner && !toolJSX?.isLocalJSXCommand && showExpandedTodos')) taskRegion = node.expression
  ts.forEachChild(node, visitRegion)
}
visitRegion(repl)
if (!taskRegion) throw new Error('Actual parent REPL task region missing')
let parentTasks: any, parentView: string | undefined, painted = ''
const parentCode = ts.transpileModule(`function Parent() {
 const tasksV2=useTasksV2WithCollapseEffect();
 const expandedView=useAppState(s=>s.expandedView);
 const showExpandedTodos=expandedView==='tasks',showSpinner=false,toolJSX=undefined;
 observe(tasksV2,expandedView);
 return <Box flexDirection="column">{${taskRegion.getText(repl)}}</Box>;
}`, { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022 } }).outputText
const Parent = new Function('React','Box','useTasksV2WithCollapseEffect','useAppState','TaskListV2','observe',parentCode+';return Parent')(React,Box,useTasksV2WithCollapseEffect,useAppState,TaskListV2,(tasks: any,view: string)=>{parentTasks=tasks;parentView=view})
const stdin = new PassThrough() as any
stdin.isTTY=true;stdin.setRawMode=()=>{};stdin.ref=()=>stdin;stdin.unref=()=>stdin
const stdout = new PassThrough() as any
stdout.isTTY=false;stdout.columns=100;stdout.rows=36
stdout.on('data',(chunk:any)=>painted+=chunk.toString())
const mounted = await render(React.createElement(AppStoreContext.Provider,{value:teammateStore},React.createElement(ThemeProvider,{initialState:'dark'},React.createElement(Parent))),{stdin,stdout,stderr:stdout,patchConsole:false,exitOnCtrlC:false})
await new Promise(resolve=>setTimeout(resolve,120))
const identity = { agentId: 'worker@task-source-team', agentName: 'worker', teamName: 'task-source-team', parentSessionId: bootstrap.getSessionId(), planModeRequired: false, abortController: new AbortController() }
const teammate = await runWithTeammateContext(createTeammateContext(identity), async () => {
  let captured: any
  const stopped = new Error('Actual teammate tool context captured before query')
  const args = createCall(definition, [], makeParent(teammateStore), () => async () => ({ behavior: 'allow' }), identity, new AbortController(), () => {}, 'actual-task-teammate', teammateStore.setState, false, [], 'actual-task-teammate-transcript', new Set(), 'Actual teammate task callback', {}, 'default', 'claude-sonnet-4-6', undefined, undefined)
  const iterator = runAgent({ ...args, override: { ...args.override, userContext: {}, systemContext: {}, systemPrompt: asSystemPrompt([]) }, onCacheSafeParams: params => { captured = params.toolUseContext; throw stopped } })
  try { await iterator.next(); throw new Error('Original teammate capture missing') } catch (error) { if (error !== stopped) throw error }
  const childViewBefore = captured.getAppState().expandedView
  const result = await TaskUpdateTool.call({ taskId: '99', status: 'in_progress' }, captured)
  const childViewAfter = captured.getAppState().expandedView
  const { TaskCreateTool } = await import('../../../src/tools/TaskCreateTool/TaskCreateTool')
  const created = await TaskCreateTool.call({subject:'ACTUAL_TEAMMATE_SHARED_TASK',description:'Original child write reaches mounted parent store'},captured)
  return { expandedView: teammateStore.getState().expandedView, childViewBefore, childViewAfter, childViewAfterCreate:captured.getAppState().expandedView, global: config.getGlobalConfig(), tools: captured.options.tools.map((tool: any) => tool.name), result, created }
})
const parentDeadline=Date.now()+3000
while(!parentTasks?.some((task:any)=>task.subject==='ACTUAL_TEAMMATE_SHARED_TASK')) {
  if(Date.now()>parentDeadline) throw new Error('Actual shared task write never reached mounted parent subscription')
  await new Promise(resolve=>setTimeout(resolve,20))
}
;(teammate as any).parentConsumer={tasks:parentTasks,expandedView:parentView,painted}
mounted.unmount()
const cancelController = new AbortController()
const cancelRun = runWorkflowAgent({ prompt: 'ACTUAL_CANCEL_WORKFLOW', opts: {agentType:definition.agentType}, toolUseContext:makeParent(teammateStore) as any, canUseTool:(async()=>({behavior:'allow'})) as any, runId:'wf_task_cancel-actual', abortController:cancelController, onAgentId:()=>{}, onProgress:()=>{} })
await cancelledRequest
cancelController.abort()
let cancelOutcome: any, cancelError: string | undefined
try { cancelOutcome=await cancelRun } catch(error) { cancelError=error instanceof Error?error.message:String(error) }
releaseCancelled()
const canceled={outcome:cancelOutcome,error:cancelError,expandedView:teammateStore.getState().expandedView,requests:requests.slice(2)}
server.stop(true)
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original task callback escaped its private Source fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ workflow, teammate, canceled, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
