import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { probeCli } from './cliInventory'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Teams entrypoint and enabled consumers')
if (process.env.CC_HAHA_TEAM_ENABLED_FIXTURE !== '1') throw new Error('Missing Teams enabled fixture')
// Run the entire real Commander registration/parse before installing the
// in-process fixture's subprocess guard. The child has its own original guard.
const cli = await probeCli(['--agent-teams'])
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(input.homeDir, 'policy')
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { isAgentSwarmsEnabled } = await import('../../../src/utils/agentSwarmsEnabled')
const { TeamCreateTool } = await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
const { SendMessageTool } = await import('../../../src/tools/SendMessageTool/SendMessageTool')
const keys = ['USER_TYPE', 'CC_HAHA_AGENT_TEAMS_ENABLED', 'CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS', 'CC_HAHA_AGENT_TEAMS_DEFAULT']
const rows = input.rows.map((row: any) => {
  for (const key of keys) delete process.env[key]
  Object.assign(process.env, row.env)
  process.argv = [process.execPath, 'actual-cli', ...(row.args ?? [])]
  return { enabled: isAgentSwarmsEnabled(), create: TeamCreateTool.isEnabled(), send: SendMessageTool.isEnabled() }
})
process.stdout.write(JSON.stringify({ cli: { exitCode: cli.exitCode, stdout: cli.stdout, stderr: cli.stderr, parsed: cli.parsed, blocked: cli.blocked }, rows, capabilities }))
