import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original Agent TaskUpdate AppState callback')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TASK_AGENT_SOURCE !== '1') throw new Error('Pinned original Agent task context required')
process.env.ANTHROPIC_API_KEY = 'task-agent-private-api-key'
process.env.USER_TYPE = 'external'
process.env.CLAUDE_CODE_TMPDIR = home
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude', 'settings.json'), '{}')
fs.writeFileSync(join(home, '.claude', '.claude.json'), JSON.stringify({ hasCompletedOnboarding: true, customApiKeyResponses: { approved: [process.env.ANTHROPIC_API_KEY.slice(-20)], rejected: [] }, taskAgentFuture: { keep: true } }))
const absentPaths = absentManagedSettingsFixturePaths()
for (let directory = dirname(home);; directory = dirname(directory)) {
  const probe = join(directory, '.git')
  if (!fs.existsSync(probe)) absentPaths.push(probe)
  if (dirname(directory) === directory) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-task-agent-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package', VERSION_CHANGELOG: '', FEEDBACK_CHANNEL: 'local' }
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
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { runAgent } = await import('../../../src/tools/AgentTool/runAgent')
const { registerAgentForeground, backgroundAgentTask } = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
const { TaskUpdateTool } = await import('../../../src/tools/TaskUpdateTool/TaskUpdateTool')
const { runToolUse } = await import('../../../src/services/tools/toolExecution')
const results = []
for (const [index, row] of input.rows.entries()) {
  config.saveGlobalConfig(global => ({ ...global, showExpandedTodos: false, showSpinnerTree: false }))
  const store = createStore({ ...getDefaultAppState(), expandedView: 'none' }, onChangeAppState)
  const definition = { agentType: 'task-context-fixture', whenToUse: 'Original TaskUpdate callback', rawSystemPrompt: 'Original task callback', getSystemPrompt: () => 'Original task callback' }
  const parent = { options: { commands: [], debug: false, mainLoopModel: 'claude-sonnet-4-6', tools: [TaskUpdateTool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [definition], allAgents: [definition] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: store.getState, setAppState: store.setState, setResponseLength: () => {}, messages: [] }
  const registration = row.promote ? registerAgentForeground({ agentId: `task-agent-${index}`, description: 'Actual task callback promotion', prompt: 'Original task callback', selectedAgent: definition, toolUseId: 'task-context-promotion', setAppState: store.setState }) : undefined
  let captured: any
  const stoppedBeforeQuery = new Error('Actual Agent context captured before provider query')
  const iterator = runAgent({ agentDefinition: definition, promptMessages: [], toolUseContext: parent as any, canUseTool: (async () => ({ behavior: 'allow' })) as any, isAsync: row.async, isBackgrounded: registration ? () => !!store.getState().tasks[registration.taskId]?.isBackgrounded : undefined, querySource: 'agent:custom', override: { userContext: {}, systemContext: {}, systemPrompt: asSystemPrompt([]), agentId: `task-agent-${index}` as any }, availableTools: [TaskUpdateTool], onCacheSafeParams: params => { captured = params.toolUseContext; throw stoppedBeforeQuery } })
  try {
    await iterator.next()
    throw new Error('Original Agent did not produce its actual tool context')
  } catch (error) {
    if (error !== stoppedBeforeQuery) throw error
  }
  const initialTools = captured.options.tools.map((tool: any) => tool.name)
  if (registration && !backgroundAgentTask(registration.taskId, store.getState, store.setState)) throw new Error('Actual original foreground task did not promote')
  const tools = captured.options.tools.map((tool: any) => tool.name)
  let update
  const unknownToolMessages = []
  if (tools.includes('TaskUpdate')) update = await TaskUpdateTool.call({ taskId: '99', status: 'in_progress' }, captured)
  else {
    const toolUse = { id: 'actual-task-context-use', name: 'TaskUpdate', input: { taskId: '99', status: 'in_progress' }, type: 'tool_use' }
    const assistant = { uuid: 'actual-task-context-assistant', type: 'assistant', message: { id: 'actual-task-context-message', content: [toolUse] } }
    for await (const message of runToolUse(toolUse as any, assistant as any, (async () => ({ behavior: 'allow' })) as any, captured)) unknownToolMessages.push(message.message)
  }
  results.push({ expandedView: store.getState().expandedView, global: config.getGlobalConfig(), initialTools, tools, update, unknownToolMessages })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original Agent task context escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
