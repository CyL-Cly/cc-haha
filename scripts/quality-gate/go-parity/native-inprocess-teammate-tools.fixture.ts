import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual async in-process teammate tool assembly')
if (process.env.CC_HAHA_INPROCESS_TEAM_TOOLS_FIXTURE !== '1') throw new Error('Missing teammate tools fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'
const { createTeammateContext, runWithTeammateContext } = await import('../../../src/utils/teammateContext')
await import('../../../src/utils/swarm/inProcessRunner')
const { resolveAgentTools } = await import('../../../src/tools/AgentTool/agentToolUtils')
const context = createTeammateContext({ agentId: 'worker@actual', agentName: 'worker', teamName: 'actual', planModeRequired: false, parentSessionId: 'parent', abortController: new AbortController() })
const rows = runWithTeammateContext(context, () => input.rows.map((row: any) => {
  const result = resolveAgentTools(row.definition, row.available.map((name: string) => ({ name, aliases: [] })), true)
  return { ...result, resolvedTools: result.resolvedTools.map(tool => tool.name) }
}))
process.stdout.write(JSON.stringify({ rows, capabilities }))
