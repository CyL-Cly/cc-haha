import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native agent continuation')
if (process.env.CC_HAHA_NATIVE_AGENT_CONTINUATION_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned agent continuation fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd); state.setCwdState(input.cwd); state.setIsInteractive(false); state.switchSession(input.sessionId)
const framework = await import('../../../src/utils/task/framework')
const local = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
const views = await import('../../../src/state/teammateViewHelpers')
const storage = await import('../../../src/utils/sessionStorage')
const filters = await import('../../../src/utils/messages')
const resume = await import('../../../src/tools/AgentTool/resumeAgent')
const { getAgentPendingMessageAttachments } = await import('../../../src/utils/attachments')
const { SendMessageTool } = await import('../../../src/tools/SendMessageTool/SendMessageTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { mkdir, writeFile } = await import('fs/promises')
const { dirname } = await import('path')
// Execute the actual REPL disk-bootstrap updater, with only its lexical
// inputs supplied. This is test-only source extraction, never production.
const { parse } = await import('acorn')
const replPath = new URL('../../../src/screens/REPL.tsx', import.meta.url)
const replText = await Bun.file(replPath).text()
const replCode = new Bun.Transpiler({ loader: 'tsx' }).transformSync(replText)
const replAST = parse(replCode, { ecmaVersion: 'latest', sourceType: 'module' })
let bootstrapUpdater: string | undefined
function findBootstrap(node: any) {
 if (!node || typeof node !== 'object') return
 if (node.type === 'CallExpression' && node.callee?.name === 'setAppState' && node.arguments[0]?.type === 'ArrowFunctionExpression' && replCode.slice(node.arguments[0].start, node.arguments[0].end).includes('const liveUuids = new Set')) bootstrapUpdater = replCode.slice(node.arguments[0].start, node.arguments[0].end)
 for (const value of Object.values(node)) { if (Array.isArray(value)) value.forEach(findBootstrap); else if (value && typeof value === 'object') findBootstrap(value) }
}
findBootstrap(replAST)
if (!bootstrapUpdater) throw new Error('Original REPL bootstrap updater not found')
const compiledUpdater = `const update = ${bootstrapUpdater}`
const originalBootstrap = (taskId: string, messages: any[]) => new Function('taskId', 'result', 'isLocalAgentTask', `${compiledUpdater}\nreturn update`)(taskId, { messages }, local.isLocalAgentTask)
const sdkEvents = await import('../../../src/utils/sdkEventQueue')
const strip = ({ abortController, unregisterCleanup, ...data }: any) => data
const states: any[] = []
const originalNow = Date.now
for (const row of input.states ?? []) {
 Date.now = () => row.now
 let current: any = { tasks: {}, speculation: { status: 'inactive' }, viewSelectionMode: 'none' }
 const setAppState = (f: any) => { current = f(current) }
 sdkEvents.drainSdkEvents()
 for (const step of row.steps) {
  if (step.op === 'register') framework.registerTask(structuredClone(step.task), setAppState)
  else if (step.op === 'pending') local.queuePendingMessage(step.id, step.message, setAppState)
  else if (step.op === 'append') local.appendMessageToLocalAgent(step.id, step.message, setAppState)
  else if (step.op === 'enter') views.enterTeammateView(step.id, setAppState)
  else if (step.op === 'exit') views.exitTeammateView(setAppState)
  else if (step.op === 'drain') { states.push({ drained: getAgentPendingMessageAttachments({ agentId: step.id, getAppState: () => current, setAppState } as any) }); continue }
  else if (step.op === 'bootstrap') setAppState(originalBootstrap(step.id, step.messages))
  states.push({ tasks: Object.fromEntries(Object.entries(current.tasks).map(([id, task]) => [id, strip(task)])), viewingAgentTaskId: current.viewingAgentTaskId, viewSelectionMode: current.viewSelectionMode, events: sdkEvents.drainSdkEvents().map(({ uuid, session_id, ...row }: any) => row) })
 }
}
Date.now = originalNow
const foregrounds: any[] = []
for (const row of input.foregrounds ?? []) {
 Date.now = () => row.now
 let current: any = { tasks: {}, speculation: { status: 'inactive' } }
 const setAppState = (f: any) => { current = f(current) }
 sdkEvents.drainSdkEvents()
 const registration = local.registerAgentForeground({ agentId: row.id, description: row.description, prompt: row.prompt, selectedAgent: row.selectedAgent, toolUseId: row.toolUseId, setAppState, autoBackgroundMs: row.autoBackgroundMs })
 let resolved = false
 void registration.backgroundSignal.then(() => { resolved = true })
 const initial = strip(current.tasks[row.id])
 const changed = row.autoBackgroundMs ? undefined : local.backgroundAgentTask(row.id, () => current, setAppState)
 if (row.autoBackgroundMs) await new Promise(resolve => setTimeout(resolve, row.autoBackgroundMs + 10))
 await Promise.resolve()
 const promoted = strip(current.tasks[row.id])
 const duplicate = local.backgroundAgentTask(row.id, () => current, setAppState)
 local.unregisterAgentForeground(row.id, setAppState)
 foregrounds.push({ initial, changed, resolved, promoted, duplicate, retainedAfterUnregister: Boolean(current.tasks[row.id]), events: sdkEvents.drainSdkEvents().map(({ uuid, session_id, ...event }: any) => event) })
 registration.cancelAutoBackground?.()
}
Date.now = originalNow
const agentSource = await Bun.file(new URL('../../../src/tools/AgentTool/AgentTool.tsx', import.meta.url)).text()
const agentCode = new Bun.Transpiler({ loader: 'tsx' }).transformSync(agentSource)
const agentAST = parse(agentCode, { ecmaVersion: 'latest', sourceType: 'module' })
const autoNode: any = (agentAST as any).body.find((node: any) => node.type === 'FunctionDeclaration' && node.id?.name === 'getAutoBackgroundMs')
if (!autoNode) throw new Error('Actual getAutoBackgroundMs not found')
const { isEnvTruthy } = await import('../../../src/utils/envUtils')
const autoFunction = new Function('isEnvTruthy', 'getFeatureValue_CACHED_MAY_BE_STALE', `${agentCode.slice(autoNode.start, autoNode.end)}; return getAutoBackgroundMs`)(isEnvTruthy, (_: string, defaultValue: boolean) => defaultValue)
const autoDelays = (input.autoValues ?? []).map((value: string | null) => { if (value === null) delete process.env.CLAUDE_AUTO_BACKGROUND_TASKS; else process.env.CLAUDE_AUTO_BACKGROUND_TASKS = value; return autoFunction() })
const transcripts: any[] = []
for (const row of input.transcripts ?? []) {
 const path = storage.getAgentTranscriptPath(row.id)
 await mkdir(dirname(path), { recursive: true }); await writeFile(path, row.content)
 const result = await storage.getAgentTranscript(row.id)
 let filtered: any = null, error: any
 try { filtered = result ? filters.filterWhitespaceOnlyAssistantMessages(filters.filterOrphanedThinkingOnlyMessages(filters.filterUnresolvedToolUses(result.messages))) : null }
 catch (failure: any) { error = { name: failure.name, message: failure.message } }
 transcripts.push({ result, filtered, error })
}
const schemas = [await toolToAPISchema(SendMessageTool, { tools: [SendMessageTool], agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => (await import('../../../src/Tool')).getEmptyToolPermissionContext() })]
const parsers = (input.parsers ?? []).map((value: any) => { const result = SendMessageTool.inputSchema.safeParse(value); return result.success ? { data: result.data } : { issues: result.error.issues } })
const messages: any[] = []
for (const row of input.messages ?? []) {
 let current: any = { tasks: row.task ? { [row.id]: row.task } : {}, agentNameRegistry: new Map(row.name ? [[row.name, row.id]] : []) }
 const context: any = { getAppState: () => current, setAppState: (f: any) => { current = f(current) } }
 const inValue = structuredClone(row.input); SendMessageTool.backfillObservableInput!(inValue)
 const parsed = SendMessageTool.inputSchema.parse(row.input)
 const validated = await SendMessageTool.validateInput!(parsed, context)
 const called = row.call ? await SendMessageTool.call(parsed, context, async () => ({ behavior: 'allow' })) : undefined
 messages.push({ observable: inValue, validated, data: called?.data, task: row.call ? current.tasks[row.id] : undefined, mapped: called ? SendMessageTool.mapToolResultToToolResultBlockParam(called.data, 'source-message-tool') : undefined, concurrency: SendMessageTool.isConcurrencySafe(parsed), readOnly: SendMessageTool.isReadOnly(parsed) })
}
const owners = (input.owners ?? []).map((row: any) => resume.resolveResumedAgentOwnerAgentId(row.current, row.existing, row.metadata) ?? null)
const models = (input.models ?? []).map((row: any) => resume.resolveResumedAgentModelOverride(row) ?? null)
const runAgentModule = await import('../../../src/tools/AgentTool/runAgent')
const runAgentCode = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/tools/AgentTool/runAgent.ts', import.meta.url)).text())
const runAgentAST = parse(runAgentCode, { ecmaVersion: 'latest', sourceType: 'module' })
let metadataExpression: string | undefined
function findMetadata(node: any) {
 if (!node || typeof node !== 'object') return
 if (node.type === 'CallExpression' && node.callee?.name === 'writeAgentMetadata' && node.arguments[1]?.type === 'ObjectExpression' && runAgentCode.slice(node.arguments[1].start, node.arguments[1].end).includes('resolvePersistedAgentType')) metadataExpression = runAgentCode.slice(node.arguments[1].start, node.arguments[1].end)
 for (const value of Object.values(node)) { if (Array.isArray(value)) value.forEach(findMetadata); else if (value && typeof value === 'object') findMetadata(value) }
}
findMetadata(runAgentAST)
if (!metadataExpression) throw new Error('Actual runAgent metadata expression unavailable')
const projectMetadata = new Function('resolvePersistedAgentType', 'persistedAgentType', 'agentDefinition', 'model', 'worktreePath', 'description', 'spawningToolUseId', 'ownerAgentId', 'workflow', `return ${metadataExpression}`)
const metadata = (input.metadata ?? []).map((row: any) => projectMetadata(runAgentModule.resolvePersistedAgentType, row.persistedAgentType, { agentType: row.runtimeAgentType }, row.model, row.worktreePath, row.description, row.toolUseId, row.ownerAgentId, undefined))
const replacement = await import('../../../src/utils/toolResultStorage')
const inactiveReplacements = (input.replacementRecords ?? []).map((records: any[]) => replacement.reconstructForSubagentResume(undefined, [], records) ?? null)
process.stdout.write(JSON.stringify({ states, foregrounds, autoDelays, transcripts, schemas, parsers, messages, owners, models, metadata, inactiveReplacements, capabilities: { blocked: capabilities.blocked } }) + '\n')
