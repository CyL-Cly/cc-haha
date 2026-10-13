import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Agent promotion lifecycle')
if (process.env.CC_HAHA_NATIVE_PROMOTION_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned promotion fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(input.cwd); bootstrap.setCwdState(input.cwd); bootstrap.setIsInteractive(false); bootstrap.switchSession(input.sessionId)
const local = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
const utils = await import('../../../src/tools/AgentTool/agentToolUtils')
const { runWithAgentContext } = await import('../../../src/utils/agentContext')
const { extractTextContent } = await import('../../../src/utils/messages')
const { AbortError, errorMessage } = await import('../../../src/utils/errors')
const { clearDumpState } = await import('../../../src/services/api/dumpPrompts')
const { logForDebugging } = await import('../../../src/utils/debug')
const { logEvent } = await import('../../../src/services/analytics')
const { sleep } = await import('../../../src/utils/sleep')
const { getTaskOutputPath } = await import('../../../src/utils/task/diskOutput')
const { toolMatchesName } = await import('../../../src/Tool')
const { asAgentId } = await import('../../../src/types/ids')
const { parse } = await import('acorn')
const code = new Bun.Transpiler({ loader: 'tsx' }).transformSync(await Bun.file(new URL('../../../src/tools/AgentTool/AgentTool.tsx', import.meta.url)).text())
const ast = parse(code, { ecmaVersion: 'latest', sourceType: 'module' })
let block: string | undefined
function visit(node: any) {
 if (!node || typeof node !== 'object') return
 if (node.type === 'IfStatement' && code.slice(node.test.start, node.test.end).includes('raceResult.type === "background"') && code.slice(node.start, node.end).includes('wasBackgrounded = true')) block = code.slice(node.start, node.end)
 for (const value of Object.values(node)) { if (Array.isArray(value)) value.forEach(visit); else if (value && typeof value === 'object') visit(value) }
}
visit(ast)
if (!block) throw new Error('Original foreground promotion branch unavailable')
const invoke = new Function('context', `return (async () => { with (context) { ${block} } })()`)
const rows: any[] = []
for (const row of input.rows) {
 const events: { name: string, elapsed: number }[] = []
 const start = performance.now()
 const event = (name: string) => events.push({ name, elapsed: performance.now() - start })
 const id = row.id
 let current: any = { tasks: {}, speculation: { status: 'inactive' } }
 const rootSetAppState = (fn: any) => { current = fn(current) }
 const selectedAgent = { agentType: 'general-purpose', tools: ['Read'], description: 'Source physical phase' }
 const registration = local.registerAgentForeground({ agentId: id, description: 'Source promotion', prompt: 'Original prompt', selectedAgent, toolUseId: 'source-promotion-card', setAppState: rootSetAppState })
 local.backgroundAgentTask(id, () => current, rootSetAppState)
 let oldReturned = false
 // Native async-iterator I/O is an external primitive. The entire actual
 // business promotion branch and real task helpers/finalizer
 // below execute unchanged from AgentTool source; no production TS fallback.
 const original = (async function* () {
  try { await sleep(row.pendingMs); event('old-pending-yield'); yield { type: 'assistant', uuid: 'physical-pending-message', message: { role: 'assistant', usage: { input_tokens: 2, output_tokens: 3 }, content: [{ type: 'text', text: 'native pending response' }] } } }
  finally { event('old-finally-start'); await sleep(row.finallyMs); event('old-finally-done'); if (row.finallyReject) throw new Error('native close rejected') }
 })()
 const next = original.next()
 const agentIterator = { next() { event('iterator-next-called'); return original.next() }, return(value: any) { event('iterator-return-called'); return original.return(value).finally(() => { oldReturned = true }) } }
 let background: Promise<any> | undefined
 let complete: () => void = () => {}
 const backgroundDone = new Promise<void>(resolve => { complete = resolve })
 const context: any = {
  raceResult: { type: 'background' }, foregroundTaskId: id,
  toolUseContext: { abortController: new AbortController(), getAppState: () => current, setAppState: rootSetAppState, toolUseId: 'source-promotion-card', options: { tools: [{ name: 'Read' }] } },
  isLocalAgentTask: local.isLocalAgentTask, wasBackgrounded: false, syncAgentContext: { agentType: 'subagent', agentId: id }, syncAgentId: id,
  stopForegroundSummarization: () => event('foreground-summary-stop'),
  runWithAgentContext(context: any, callback: any) { background = runWithAgentContext(context, callback).finally(complete); return background },
  agentIterator, nextMessagePromise: next, abortFromParent() {}, releaseSubagentSlot() { event('release-slot') }, slotTransferred: false, sleep, rootSetAppState, selectedAgent, asAgentId,
  agentMessages: [], metadata: { prompt: 'Original prompt', agentType: 'general-purpose', resolvedAgentModel: 'fixture-model', startTime: Date.now(), isBuiltInAgent: true, isAsync: false },
  runAgentParams: { promptMessages: [{ uuid: 'original-prompt-uuid' }], override: { agentId: id } },
  async *runAgent(params: any) {
   event('background-start'); if (params.override.agentId !== id || !params.isAsync || params.promptMessages[0].uuid !== 'original-prompt-uuid') throw new Error('Source restart identity lost')
   yield { type: 'assistant', uuid: 'real-primitive-message', message: { role: 'assistant', content: [{ type: 'text', text: 'native background response' }], usage: { input_tokens: 2, output_tokens: 3 } } }
   event('background-generator-done')
  },
  createProgressTracker: local.createProgressTracker, createActivityDescriptionResolver: local.createActivityDescriptionResolver, updateProgressFromMessage: local.updateProgressFromMessage,
  updateAsyncAgentProgress: local.updateAgentProgress, getProgressUpdate: local.getProgressUpdate, getTokenCountFromTracker: local.getTokenCountFromTracker,
  emitAgentToolActivitiesForMessage: utils.emitAgentToolActivitiesForMessage, getLastToolUseName: utils.getLastToolUseName, emitTaskProgress: utils.emitTaskProgress,
  finalizeAgentTool: utils.finalizeAgentTool, completeAsyncAgent: local.completeAgentTask, enqueueAgentNotification: local.enqueueAgentNotification, extractTextContent,
  feature: () => false, getSdkAgentProgressSummariesEnabled: () => false,
  cleanupWorktreeIfNeeded: async () => { event('cleanup-worktree'); return undefined },
  clearInvokedSkillsForAgent: bootstrap.clearInvokedSkillsForAgent, clearDumpState, logForDebugging, logEvent, AbortError, errorMessage,
  killAsyncAgent: local.killAsyncAgent, failAsyncAgent: local.failAgentTask, extractPartialResult: utils.extractPartialResult,
  description: 'Source promotion', prompt: 'Original prompt', startTime: Date.now(), toolMatchesName, FILE_READ_TOOL_NAME: 'Read', BASH_TOOL_NAME: 'Bash', getTaskOutputPath, worktreeIsolationSkipped: undefined,
 }
 const result = await invoke(context)
 event('parent-tool-return')
 await backgroundDone
 await next.catch(() => {})
 while (!oldReturned) await sleep(1)
 registration.cancelAutoBackground?.()
 const { abortController, unregisterCleanup, ...task } = current.tasks[id]
 rows.push({ id, result, events, task, messages: context.agentMessages })
}
process.stdout.write(JSON.stringify({ rows, capabilities: { blocked: capabilities.blocked } }) + '\n')
