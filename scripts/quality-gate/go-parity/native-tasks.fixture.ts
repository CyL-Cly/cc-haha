import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { join } from 'node:path'
import { readdir, readFile, mkdir, writeFile } from 'node:fs/promises'

type Step = { name: string; tool: string; operation: 'parse' | 'call' | 'seed'; inputSource: string; agentId?: string; taskListOverride?: string; path?: string }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; steps: Step[] }
assertDisposableFixtureHome(input.homeDir, 'Native TaskV2')
if (process.env.CC_HAHA_NATIVE_TASKS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native TaskV2 requires isolated production profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setInitialMainLoopModel('deepseek-flash')
bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
const { TaskCreateTool } = await import('../../../src/tools/TaskCreateTool/TaskCreateTool')
const { TaskGetTool } = await import('../../../src/tools/TaskGetTool/TaskGetTool')
const { TaskUpdateTool } = await import('../../../src/tools/TaskUpdateTool/TaskUpdateTool')
const { TaskListTool } = await import('../../../src/tools/TaskListTool/TaskListTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const taskStore = await import('../../../src/utils/tasks')
const tools = [TaskCreateTool, TaskGetTool, TaskUpdateTool, TaskListTool]
const schemas = await Promise.all(tools.map(tool => toolToAPISchema(tool, { tools, agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() })))
let appState: any = { todos: {}, expandedView: 'none' }
let updates = 0
taskStore.onTasksUpdated(() => { updates++ })
async function files(list: string) {
  const directory = taskStore.getTasksDir(list)
  const result: Record<string, string> = {}
  for (const name of (await readdir(directory).catch(() => [])).sort()) if (!name.endsWith('.lock')) result[name] = await readFile(join(directory, name), 'utf8')
  return result
}
const observations: unknown[] = []
for (const step of input.steps) {
  if (step.taskListOverride === undefined) delete process.env.CLAUDE_CODE_TASK_LIST_ID
  else process.env.CLAUDE_CODE_TASK_LIST_ID = step.taskListOverride
  const taskListId = taskStore.getTaskListId(step.agentId)
  if (step.operation === 'seed') {
    const path = join(input.homeDir, '.claude', 'tasks', step.path!)
    await mkdir(join(path, '..'), { recursive: true })
    await writeFile(path, step.inputSource)
    observations.push({ step, observation: { taskListId, files: await files(taskListId), updates } })
    continue
  }
  const tool = tools.find(tool => tool.name === step.tool)!
  const parsed = tool.inputSchema.safeParse(JSON.parse(step.inputSource))
  if (!parsed.success) {
    observations.push({ step, observation: { success: false, issuesSource: JSON.stringify(parsed.error.issues) } })
    continue
  }
  if (step.operation === 'parse') {
    observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data) } })
    continue
  }
  const context: any = { agentId: step.agentId, abortController: new AbortController(), getAppState: () => appState, setAppState: (update: any) => { appState = update(appState) } }
  try {
    const result = await (tool.call as any)(parsed.data, context)
    const mapped = (tool.mapToolResultToToolResultBlockParam as any)(result.data, 'actual-task-use-id')
    observations.push({ step, observation: { success: true, taskListId, dataSource: JSON.stringify(parsed.data), resultSource: JSON.stringify(result.data), mappedSource: JSON.stringify(mapped), stateSource: JSON.stringify(appState), files: await files(taskListId), updates } })
  } catch (error) {
    observations.push({ step, observation: { success: true, taskListId, dataSource: JSON.stringify(parsed.data), error: (error as Error).message, stateSource: JSON.stringify(appState), files: await files(taskListId), updates } })
  }
}
process.stdout.write(JSON.stringify({ schemasSource: JSON.stringify(schemas), observations, sourceOnly: { consumer: 'actual TaskCreate/TaskGet/TaskUpdate/TaskList tools + src/utils/tasks.ts filesystem transactions + proper-lockfile', runtime: process.version, capabilities } }))
