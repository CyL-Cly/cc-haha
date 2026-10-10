import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Frozen Team preset source authority')
if (process.env.CC_HAHA_TEAM_PRESET_SOURCE_FIXTURE !== '1') throw new Error('Missing frozen source fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { snapshotTeamPlanPresetSource, validateTeamPlanPresetSource } = await import('../../../src/utils/swarm/teamPlanPresetSource')
const rows = input.cases.map((row: any) => {
  try {
    if (row.agent) return { value: snapshotTeamPlanPresetSource(row.agent), error: null }
    validateTeamPlanPresetSource(row.snapshot)
    return { value: null, error: null }
  } catch (error) { return { value: null, error: (error as Error).message } }
})
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
