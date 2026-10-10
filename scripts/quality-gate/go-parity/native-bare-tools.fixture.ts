import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Bare tool catalog')
if (process.env.CC_HAHA_NATIVE_BARE_TOOLS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Bare catalog requires disposable production profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] })
await import('../../../preload')
const { getTools } = await import('../../../src/tools')
const rows = input.rows.map((row: { simple: string, deny: string[] }) => {
  process.env.CLAUDE_CODE_SIMPLE = row.simple
  return getTools({
    mode: 'default',
    additionalWorkingDirectories: new Map(),
    alwaysAllowRules: {},
    alwaysAskRules: {},
    alwaysDenyRules: { cliArg: row.deny },
    isBypassPermissionsModeAvailable: false,
    isAutoModeAvailable: false,
  }).map(tool => tool.name)
})
process.stdout.write(JSON.stringify({ rows, capabilities }))
