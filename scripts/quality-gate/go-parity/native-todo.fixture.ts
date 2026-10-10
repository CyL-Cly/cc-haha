import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

type Step = { name: string; operation: 'parse' | 'call' | 'enabled'; inputSource?: string; agentId?: string; interactive?: boolean; taskEnvironment?: string }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; steps: Step[] }
assertDisposableFixtureHome(input.homeDir, 'Native TodoWrite')
if (process.env.CC_HAHA_NATIVE_TODO_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native TodoWrite requires isolated production profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setInitialMainLoopModel('deepseek-flash')
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
const { TodoWriteTool } = await import('../../../src/tools/TodoWriteTool/TodoWriteTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const schema = await toolToAPISchema(TodoWriteTool, { tools: [TodoWriteTool], agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() })
let appState: any = { todos: {}, preservedOtherState: 'actual-appstate' }
const observations: unknown[] = []
for (const step of input.steps) {
  if (step.operation === 'enabled') {
    bootstrap.setIsInteractive(step.interactive ?? false)
    if (step.taskEnvironment === undefined) delete process.env.CLAUDE_CODE_ENABLE_TASKS
    else process.env.CLAUDE_CODE_ENABLE_TASKS = step.taskEnvironment
    observations.push({ step, observation: { enabled: TodoWriteTool.isEnabled() } })
    continue
  }
  const parsed = TodoWriteTool.inputSchema.safeParse(JSON.parse(step.inputSource!))
  if (!parsed.success) {
    observations.push({ step, observation: { success: false, issuesSource: JSON.stringify(parsed.error.issues) } })
    continue
  }
  if (step.operation === 'parse') {
    observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data) } })
    continue
  }
  const context = { agentId: step.agentId, getAppState: () => appState, setAppState: (update: any) => { appState = update(appState) } } as Parameters<typeof TodoWriteTool.call>[1]
  const result = await TodoWriteTool.call(parsed.data, context)
  const mapped = TodoWriteTool.mapToolResultToToolResultBlockParam(result.data, 'actual-todo-tool-use-id')
  observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data), resultSource: JSON.stringify(result.data), mappedSource: JSON.stringify(mapped), stateSource: JSON.stringify(appState) } })
}
process.stdout.write(JSON.stringify({ schemaSource: JSON.stringify(schema), observations, sourceOnly: { consumer: 'src/tools/TodoWriteTool/TodoWriteTool.ts actual inputSchema/call/mapToolResultToToolResultBlockParam/toolToAPISchema', runtime: process.version, capabilities } }))
