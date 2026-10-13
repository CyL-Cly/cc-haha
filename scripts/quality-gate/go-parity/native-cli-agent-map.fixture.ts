import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'CLI agent JSON map')
if (process.env.CC_HAHA_CLI_AGENT_MAP_FIXTURE !== '1') throw new Error('Unowned CLI agent map')
await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { parseAgentsFromJson } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { safeParseJSON } = await import('../../../src/utils/json')
const results = input.rows.map((row: any) => {
  process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = row.memory ? 'false' : 'true'
  const value = safeParseJSON(row.text)
  return value ? parseAgentsFromJson(value, 'flagSettings').map(agent => Object.fromEntries(Object.entries(agent).filter(([, value]) => typeof value !== 'function'))) : []
})
process.stdout.write(JSON.stringify(results))
