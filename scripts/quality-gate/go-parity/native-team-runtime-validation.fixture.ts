import { spyOn } from 'bun:test'
import { writeFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { createHash } from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team approval actual local validation')
if (process.env.CC_HAHA_TEAM_VALIDATION_FIXTURE !== '1') throw new Error('Missing Team validation fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { conversationService } = await import('../../../src/server/services/conversationService')
const { writeTeamFileAsync } = await import('../../../src/utils/swarm/teamHelpers')
const { validateTeamPlanRuntime } = await import('../../../src/server/services/teamPlanRuntime')
let mode = 'default'
spyOn(conversationService, 'getSessionPermissionMode').mockImplementation(() => mode as any)
mkdirSync(join(input.homeDir, '.claude', 'cc-haha'), { recursive: true })
const rows = []
for (const row of input.cases) {
  mode = row.permissionMode ?? 'default'
  writeFileSync(join(input.homeDir, '.claude', 'cc-haha', 'providers.json'), JSON.stringify({ schemaVersion: 6, activeId: null, providers: row.providers ?? [], providerOrder: [], officialProviderModels: {} }))
  if (row.team) await writeTeamFileAsync(row.plan.teamName, row.team)
  const plan = structuredClone(row.plan)
  if (row.team && !row.stale) plan.incarnationId = createHash('sha256').update(JSON.stringify([row.team.name, row.team.leadSessionId || '', row.team.createdAt])).digest('hex')
  try { rows.push({ value: await validateTeamPlanRuntime(plan), error: null }) }
  catch (error) { rows.push({ value: null, error: (error as Error).message }) }
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
