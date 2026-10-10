import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'native task publication')
if (process.env.CC_HAHA_NATIVE_TASK_PUBLICATION !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned task publication fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths(), ...(input.hydrateURL ? { loopbackFetchOrigin: input.hydrateURL } : {}) })
const bootstrap = await import('../../../../src/bootstrap/state')
bootstrap.setOriginalCwd(home); bootstrap.setCwdState(home); bootstrap.setProjectRoot(home); bootstrap.setIsInteractive(false)
const { enableConfigs } = await import('../../../../src/utils/config'); enableConfigs()
const { bindBackgroundTaskNotifications } = await import('../../../../src/cli/print')
const { StructuredIO } = await import('../../../../src/cli/structuredIO')
const queue = await import('../../../../src/utils/messageQueueManager')
const sdk = await import('../../../../src/utils/sdkEventQueue')
const { useWorkflowStore, runsForSession } = await import('../../../../desktop/src/stores/workflowStore')
async function* emptyInput() {}
queue.clearCommandQueue(); sdk.drainSdkEvents()
const io = new StructuredIO(emptyInput())
for (const command of input.before ?? []) queue.enqueuePendingNotification(command)
const binding = bindBackgroundTaskNotifications(io)
if (input.sourceWorkflow) {
  const { prepareWorkflowScript } = await import('../../../../src/utils/workflows/runtime')
  const { launchWorkflow } = await import('../../../../src/tools/WorkflowTool/launchWorkflow')
  const { getEmptyToolPermissionContext } = await import('../../../../src/Tool')
  const { IDLE_SPECULATION_STATE } = await import('../../../../src/state/AppStateStore')
  const { join } = await import('node:path')
  const w = input.sourceWorkflow
  const prepared = prepareWorkflowScript(w.script)
  if (!prepared.ok) throw new Error(prepared.error)
  let state: any = { tasks: {}, toolPermissionContext: getEmptyToolPermissionContext(), speculation: IDLE_SPECULATION_STATE }
  let finish: () => void = () => {}
  const published = new Promise<void>(resolve => { finish = resolve })
  const setAppState = (update: (state: any) => any) => {
    state = update(state)
    const task = state.tasks[w.taskId]
    if (task && ['completed', 'failed', 'killed', 'stopped'].includes(task.status) && task.notified) finish()
  }
  launchWorkflow({
    taskId: w.taskId, workflowRunId: w.runId, script: w.script, scriptPath: join(home, 'actual-source-workflow.js'),
    meta: prepared.meta, vmScript: prepared.vmScript, isResume: false, toolUseId: w.toolUseId,
    toolUseContext: { agentId: w.ownerAgentId, toolUseId: w.toolUseId, abortController: new AbortController(), setAppState, getAppState: () => state, options: { mainLoopModel: 'fixture-model', tools: [] } } as any,
    canUseTool: (() => { throw new Error('constant workflow attempted a tool') }) as never,
  })
  await published
  // The normal print consumer also drains direct owned start/progress/bookends.
  for (const event of sdk.drainSdkEvents()) io.outbound.enqueue(event)
}
for (const command of input.after ?? []) queue.enqueuePendingNotification(command)
queue.recheckCommandQueue()
const retained = queue.getCommandQueue().length
const values = []
while (queue.getCommandQueue().length) { const command = queue.dequeue()!; values.push(command.value); binding.publish(command) }
binding.unsubscribe()
for (const command of input.disposed ?? []) queue.enqueuePendingNotification(command)
io.outbound.done()
const source = []
for await (const event of io.outbound) source.push(event)
const consume = (events: any[]) => {
  useWorkflowStore.setState({ runs: {}, openRunId: null })
  for (const event of events) if (event.type === 'system' && ['task_started', 'task_progress', 'task_notification'].includes(event.subtype)) useWorkflowStore.getState().handleTaskEvent(input.sessionId, event.subtype, event)
  const runs = runsForSession(useWorkflowStore.getState(), input.sessionId)
  return { runs, completed: runs.filter(run => run.status === 'completed' && Boolean(run.runId)).length }
}
const sourceConsumer = consume(source)
const goConsumer = consume(input.goEvents ?? [])
let coldConsumer, liveAfterHydrate
if (input.hydrateURL) {
  const { setBaseUrl } = await import('../../../../desktop/src/api/client')
  setBaseUrl(input.hydrateURL)
  useWorkflowStore.setState({ runs: {}, openRunId: null })
  await useWorkflowStore.getState().hydrateSession(input.sessionId)
  const coldRuns = runsForSession(useWorkflowStore.getState(), input.sessionId)
  coldConsumer = { runs: coldRuns, completed: coldRuns.filter(run => run.status === 'completed' && Boolean(run.runId)).length }
  consume(input.goEvents ?? [])
  await useWorkflowStore.getState().hydrateSession(input.sessionId)
  const liveRuns = runsForSession(useWorkflowStore.getState(), input.sessionId)
  liveAfterHydrate = { runs: liveRuns, completed: liveRuns.filter(run => run.status === 'completed' && Boolean(run.runId)).length }
}
process.stdout.write(JSON.stringify({ source, sourceConsumer, goConsumer, coldConsumer, liveAfterHydrate, retained, values, disposedRetained: queue.getCommandQueue().length, blocked: capabilities.blocked })+'\n')
