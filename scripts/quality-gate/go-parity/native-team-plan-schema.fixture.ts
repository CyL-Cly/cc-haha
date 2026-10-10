import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team plan schema')
if (process.env.CC_HAHA_TEAM_PLAN_SCHEMA_FIXTURE !== '1') throw new Error('Missing team plan schema fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { teamPlanRecordSchema } = await import('../../../src/shared/teamPlan')
const { teamPlanActionSchema, teamPlanPatchRequestSchema } = await import('../../../src/server/services/teamPlanService')
const { validateTeamPlanGraph } = await import('../../../src/utils/swarm/teamPlanStore')
const rows = input.cases.map((row: any) => {
  const schema = row.kind === 'action' ? teamPlanActionSchema : row.kind === 'patch' ? teamPlanPatchRequestSchema : teamPlanRecordSchema
  const result = schema.safeParse(row.input)
  let graph: string | null = null
  if (result.success && row.kind !== 'action' && row.kind !== 'patch') {
    try { validateTeamPlanGraph(result.data, row.runnable) } catch (err) { graph = (err as Error).message }
  }
  return { valid: result.success, data: result.success ? result.data : null, graph }
})
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
