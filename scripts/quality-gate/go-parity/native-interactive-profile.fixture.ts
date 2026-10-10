import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native interactive tool profile')
if (process.env.CC_HAHA_INTERACTIVE_PROFILE_FIXTURE !== '1') throw new Error('Missing interactive profile fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
process.chdir(home)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { TaskCreateTool } = await import('../../../src/tools/TaskCreateTool/TaskCreateTool')
const { TodoWriteTool } = await import('../../../src/tools/TodoWriteTool/TodoWriteTool')
const { fileHistoryEnabled } = await import('../../../src/utils/fileHistory')
const rows = []
for (const row of input.rows) {
  state.setIsInteractive(row.interactive)
  for (const [key, value] of Object.entries(row.environment)) {
    if (value === null) delete process.env[key]
    else process.env[key] = String(value)
  }
  config.saveGlobalConfig(previous => ({ ...previous, fileCheckpointingEnabled: row.checkpointing }))
  rows.push({ task: TaskCreateTool.isEnabled(), todo: TodoWriteTool.isEnabled(), history: fileHistoryEnabled() })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
process.exit(0)
