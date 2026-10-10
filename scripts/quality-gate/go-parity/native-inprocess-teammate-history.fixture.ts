import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual runAgent fork-context incomplete tool filter')
if (process.env.CC_HAHA_INPROCESS_TEAM_HISTORY_FIXTURE !== '1') throw new Error('Missing teammate history fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const { filterIncompleteToolCalls } = await import('../../../src/tools/AgentTool/runAgent')
const { getLastPeerDmSummary } = await import('../../../src/utils/teammateMailbox')
const rows = input.rows.map((row: any) => {
  try { return { result: filterIncompleteToolCalls(row), error: null } }
  catch (error) { return { result: null, error: (error as Error).message } }
})
process.stdout.write(JSON.stringify({ rows, summaries: (input.summaries ?? []).map((messages: any) => getLastPeerDmSummary(messages) ?? null), capabilities }))
