import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Frozen JSON agent definition')
if (process.env.CC_HAHA_AGENT_JSON_FIXTURE !== '1') throw new Error('Missing JSON agent fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { parseAgentFromJson } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const runtimePrompts: (string | null)[] = []
const rows = input.cases.map((row: any) => {
  process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = row.memory ? 'false' : 'true'
  const agent = parseAgentFromJson(row.name, row.definition, 'flagSettings')
  if (input.runtimePrompts) runtimePrompts.push(agent ? agent.getSystemPrompt() : null)
  return agent ? Object.fromEntries(Object.entries(agent).filter(([_, value]) => typeof value !== 'function')) : null
})
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities, ...(input.runtimePrompts ? { runtimePrompts } : {}) }), () => resolve()))
process.exit(0)
