import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native agent progress')
if (process.env.CC_HAHA_NATIVE_AGENT_PROGRESS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned agent progress fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd); state.setCwdState(input.cwd); state.setIsInteractive(false)
state.switchSession('11111111-1111-4111-8111-111111111111')
const tools = [
 (await import('../../../src/tools/BashTool/BashTool')).BashTool,
 (await import('../../../src/tools/FileReadTool/FileReadTool')).FileReadTool,
 (await import('../../../src/tools/GrepTool/GrepTool')).GrepTool,
 (await import('../../../src/tools/GlobTool/GlobTool')).GlobTool,
 (await import('../../../src/tools/FileWriteTool/FileWriteTool')).FileWriteTool,
 (await import('../../../src/tools/FileEditTool/FileEditTool')).FileEditTool,
 (await import('../../../src/tools/PowerShellTool/PowerShellTool')).PowerShellTool,
]
const { getToolSearchOrReadInfo } = await import('../../../src/utils/collapseReadSearch')
const { splitCommandWithOperators } = await import('../../../src/utils/bash/commands')
const local = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
const classification = (input.classifications ?? []).map((row: any) => {
 const original = getToolSearchOrReadInfo(row.name, row.input, tools)
 return { isSearch: original.isSearch, isRead: original.isRead }
})
const splits = (input.splits ?? []).map((command: string) => splitCommandWithOperators(command))
let current: any = { tasks: { a0123456789abcdef: { id: 'a0123456789abcdef', type: 'local_agent', status: 'running' } } }
const tracker = local.createProgressTracker()
const progress: any[] = []
const set = (f: any) => { current = f(current) }
for (const step of input.steps ?? []) {
 if (step.summary !== undefined) current.tasks.a0123456789abcdef.progress = { ...current.tasks.a0123456789abcdef.progress, summary: step.summary }
 if (step.status !== undefined) current.tasks.a0123456789abcdef.status = step.status
 if (step.message) {
  local.updateProgressFromMessage(tracker, step.message, local.createActivityDescriptionResolver(tools), tools)
  local.updateAgentProgress('a0123456789abcdef', local.getProgressUpdate(tracker), set)
 }
 progress.push(structuredClone({ tracker: local.getProgressUpdate(tracker), task: current.tasks.a0123456789abcdef }))
}
process.stdout.write(JSON.stringify({ classification, splits, progress, capabilities: { blocked: capabilities.blocked } }) + '\n')
