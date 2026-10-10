import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native background tasks')
if (process.env.CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned task fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { TaskOutputTool } = await import('../../../src/tools/TaskOutputTool/TaskOutputTool')
const { TaskStopTool } = await import('../../../src/tools/TaskStopTool/TaskStopTool')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd ?? home); state.setCwdState(input.cwd ?? home); state.setIsInteractive(false)
state.switchSession(input.startupSessionId ?? input.sessionId ?? '11111111-1111-4111-8111-111111111111')
if (input.startupSessionId) state.switchSession(input.sessionId)
const disk = await import('../../../src/utils/task/diskOutput')
const { mkdir, writeFile } = await import('fs/promises')
await mkdir(disk.getTaskOutputDir(), { recursive: true })
const directorySequence = [disk.getTaskOutputDir()]
for (const sid of input.diskSessionSequence ?? []) { state.switchSession(sid); directorySequence.push(disk.getTaskOutputDir()) }
const toolMap: any = { TaskOutput: TaskOutputTool, TaskStop: TaskStopTool }
if ((input.maps ?? []).some((row: any) => row.name === 'Agent')) toolMap.Agent = (await import('../../../src/tools/AgentTool/AgentTool')).AgentTool
const activities: any[] = []
if ((input.activities ?? []).length) {
 const modules: any = {
  Agent: ['AgentTool/AgentTool', 'AgentTool'], Bash: ['BashTool/BashTool', 'BashTool'],
  Read: ['FileReadTool/FileReadTool', 'FileReadTool'], Write: ['FileWriteTool/FileWriteTool', 'FileWriteTool'], Edit: ['FileEditTool/FileEditTool', 'FileEditTool'],
  NotebookEdit: ['NotebookEditTool/NotebookEditTool','NotebookEditTool'], Glob: ['GlobTool/GlobTool','GlobTool'], Grep: ['GrepTool/GrepTool','GrepTool'],
  WebFetch: ['WebFetchTool/WebFetchTool','WebFetchTool'], WebSearch: ['WebSearchTool/WebSearchTool','WebSearchTool'],
 }
 for (const row of input.activities) {
  const spec = modules[row.name]
  const tool = spec ? (await import(`../../../src/tools/${spec[0]}`))[spec[1]] : undefined
  activities.push(tool?.getActivityDescription?.(JSON.parse(row.input)) ?? null)
 }
}
const { toolToAPISchema } = await import('../../../src/utils/api')
const tools = [TaskOutputTool, TaskStopTool]
const schemas = await Promise.all(tools.map(tool => toolToAPISchema(tool, { tools, agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => (await import('../../../src/Tool')).getEmptyToolPermissionContext() })))
const parsers = (input.parsers ?? []).map((row: any) => {
 const parsed = toolMap[row.name].inputSchema.safeParse(JSON.parse(row.input))
 return parsed.success ? { success: true, data: parsed.data } : { success: false, issues: parsed.error.issues }
})
const mapped = (input.maps ?? []).map((row: any) => {
 process.env.TASK_MAX_OUTPUT_LENGTH = row.limit ?? ''
 return toolMap[row.name].mapToolResultToToolResultBlockParam(JSON.parse(row.data), 'actual-task-use')
})
const validations: any[] = [], calls: any[] = []
const shellCases: any[] = []
const { normalizeAttachmentForAPI } = await import('../../../src/utils/messages')
const { normalizeMessage } = await import('../../../src/utils/queryHelpers')
const progressNormalized = (input.progress ?? []).map((row: string) => [...normalizeMessage(JSON.parse(row))])
const queued = (input.queued ?? []).map((row: string) => normalizeAttachmentForAPI(JSON.parse(row)).map(({ uuid, timestamp, ...record }: any) => record))
const localShell = await import('../../../src/tasks/LocalShellTask/LocalShellTask')
const queues = await import('../../../src/utils/messageQueueManager')
const sdkEvents = await import('../../../src/utils/sdkEventQueue')
const partitions = (input.partitions ?? []).map((rows: any[]) => {
 queues.clearCommandQueue()
 for (const row of rows) queues.enqueue(row)
 const root: any[] = []
 let command
 while ((command = queues.dequeue(row => row.agentId === undefined))) root.push(command.value)
 return { root, remaining: queues.getCommandQueue().map(({ value, priority, agentId }: any) => ({ value, priority, agentId })) }
})
const agentCases: any[] = []
if ((input.agents ?? []).length) {
 const localAgent = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
 const { emitTaskProgress } = await import('../../../src/tools/AgentTool/agentToolUtils')
 const { bindBackgroundTaskNotifications } = await import('../../../src/cli/print')
 const strip = ({ abortController, unregisterCleanup, ...data }: any) => data
 const originalNow = Date.now
 for (const row of input.agents) {
  Date.now = () => row.now
  queues.clearCommandQueue(); sdkEvents.drainSdkEvents()
  let current: any = { tasks: {}, speculation: { status: 'inactive' } }
  const setAppState = (f: any) => { current = f(current) }
  const published: any[] = []
  const binding = bindBackgroundTaskNotifications({ outbound: { enqueue: (event: any) => { const { uuid, session_id, ...value } = event; published.push(value) } } } as any)
  const request: any = { agentId: row.id, description: row.description, prompt: row.prompt, selectedAgent: row.selectedAgent, setAppState, toolUseId: row.toolUseId, ownerAgentId: row.ownerAgentId }
  if (row.background) localAgent.registerAsyncAgent(request)
  else localAgent.registerAgentForeground(request)
  const initial = strip(current.tasks[row.id])
  const tracker = localAgent.createProgressTracker()
  for (const message of row.messages ?? []) {
   localAgent.updateProgressFromMessage(tracker, message)
   let lastToolName: string | undefined
   if (message.type === 'assistant') for (const block of message.message.content) if (block.type === 'tool_use') lastToolName = block.name
   if (lastToolName) emitTaskProgress(tracker, row.id, row.toolUseId, row.description, row.now, lastToolName, row.ownerAgentId)
  }
  const events = sdkEvents.drainSdkEvents().map(({ uuid, session_id, ...event }: any) => event)
  if (row.background) {
   if (row.status === 'killed') localAgent.killAsyncAgent(row.id, setAppState)
   else if (row.status === 'failed') localAgent.failAgentTask(row.id, row.error, setAppState)
   else localAgent.completeAgentTask(row.result, setAppState)
   const lifecycleUsage = row.usage ? { ...row.usage, totalTokens: localAgent.getTokenCountFromTracker(tracker) } : undefined
   const finalMessage = row.status === 'killed' ? (await import('../../../src/tools/AgentTool/agentToolUtils')).extractPartialResult(row.messages ?? []) : row.finalMessage
   localAgent.enqueueAgentNotification({ taskId: row.id, description: row.description, status: row.status, error: row.error, setAppState, finalMessage, usage: lifecycleUsage, toolUseId: row.toolUseId, worktreePath: row.worktreePath, worktreeBranch: row.worktreeBranch })
  } else localAgent.unregisterAgentForeground(row.id, setAppState)
  const terminal = current.tasks[row.id] ? strip(current.tasks[row.id]) : null
  const pending = queues.getCommandQueue().map(({ value, mode, priority, agentId }: any) => ({ value, mode, priority, agentId }))
  const terminalEvents = sdkEvents.drainSdkEvents().map(({ uuid, session_id, ...event }: any) => event)
  agentCases.push({ initial, events, terminal, pending, published, terminalEvents })
  binding.unsubscribe()
 }
 Date.now = originalNow
 await disk._clearOutputsForTest()
}
for (const row of input.shells ?? []) {
 queues.clearCommandQueue(); sdkEvents.drainSdkEvents()
 let current: any = { tasks: {}, speculation: { status: "inactive" } }
 let settle: any
 const result = new Promise(resolve => { settle = resolve })
 const shellCommand: any = { taskOutput: { taskId: row.id, flush: async () => {} }, result, background: () => true, cleanup: () => {}, kill: () => {} }
 await localShell.spawnShellTask({ command: row.command, description: row.description, shellCommand, toolUseId: row.toolUseId, agentId: row.agentId }, { getAppState: () => current, setAppState: (fn: any) => { current = fn(current) }, abortController: new AbortController() } as any)
 const started = sdkEvents.drainSdkEvents().map(({ uuid, session_id, ...event }: any) => event)
 settle({ code: row.code, interrupted: false })
 for (let n = 0; n < 50 && current.tasks[row.id].status === 'running'; n++) await new Promise(resolve => setTimeout(resolve, 1))
 shellCases.push({ started, notifications: queues.getCommandQueue().map(({ value, mode, priority, agentId }: any) => ({ value, mode, priority, agentId })), status: current.tasks[row.id].status, notified: current.tasks[row.id].notified })
}
for (const row of input.calls ?? []) {
 let current: any = { tasks: row.task ? { [row.task.id]: structuredClone(row.task) } : {} }
 const context: any = { getAppState: () => current, setAppState: (fn: any) => { current = fn(current) }, abortController: new AbortController() }
 if (row.output !== undefined && row.task) await writeFile(disk.getTaskOutputPath(row.task.id), row.output)
 const tool: any = toolMap[row.name]
 const parsed = tool.inputSchema.parse(JSON.parse(row.input))
 validations.push(await tool.validateInput(parsed, context))
 try { const result = await tool.call(parsed, context); calls.push({ data: result.data, notified: row.task ? current.tasks[row.task.id]?.notified : undefined }) }
 catch (error: any) { calls.push({ error: error.message }) }
}
process.stdout.write(JSON.stringify({ schemas, parsers, mapped, calls, validations, shellCases, queued, progressNormalized, agentCases, partitions, activities, prompts: (input.prompts ?? []).map((s: string) => localShell.looksLikePrompt(s)), outputDir: disk.getTaskOutputDir(), directorySequence, capabilities }))
