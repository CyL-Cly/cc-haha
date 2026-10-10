import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team plan service policy')
if (process.env.CC_HAHA_TEAM_PLAN_SERVICE_FIXTURE !== '1') throw new Error('Missing team plan service fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { TeamPlanService } = await import('../../../src/server/services/teamPlanService')
const store = await import('../../../src/utils/swarm/teamPlanStore')
const { writeTeamFileAsync } = await import('../../../src/utils/swarm/teamHelpers')
spyOn(Date, 'now').mockImplementation(() => 2000)
const rows = []
for (const row of input.cases) {
  const team = `policy-${row.kind}`
  await writeTeamFileAsync(team, { name: team, members: [], leadSessionId: team, createdAt: 1000 } as any)
  let plan = await store.ensureTeamDraft(team, team, row.runtime, { ...row.initial, workDir: input.homeDir })
  plan = await store.submitTeamPlan(team, { ...plan, expectedRevision: plan.revision })
  const calls: string[] = []
  const service: any = new TeamPlanService({
    validate: async (value: any) => { calls.push('validate'); store.validateTeamPlanGraph(value, true); if (row.kind === 'invalid') throw new Error('actual validation rejected'); return value },
    launch: async () => { calls.push('launch'); if (row.kind === 'failed' || row.kind === 'started') { const error: any = new Error('actual launch rejected'); if (row.kind === 'started') error.executionStarted = true; throw error }; return { memberIds: { member: 'private-owned-worker-session' } } },
    stop: async () => { calls.push('stop') }, isRunning: async () => row.kind !== 'lost_running',
    notifyLeader: async () => { calls.push('notify'); if (row.kind === 'notify') throw new Error('actual delivery failed') },
  })
  let error: string | null = null, status: number | null = null
  try {
    if (row.kind === 'notify') await service.action(team, 'return', { ...plan, expectedRevision: plan.revision, requestId: 'human-request' })
    else {
      const approved = await service.approve(team, { ...plan, expectedRevision: plan.revision, requestId: 'human-request' })
      await service.launches.get(approved.planId)
      if (row.kind === 'retry') throw new Error('unused')
      if (row.kind === 'lost_launching') await store.mutateTeamPlan(team, { ...approved, expectedRevision: (await store.readTeamPlan(team))!.revision }, (v: any) => ({ ...v, state: 'launching' }))
      if (row.kind === 'lost_running' || row.kind === 'lost_launching') await service.getForSession(team)
      if (row.kind === 'failed') { const current = (await store.readTeamPlan(team))!; await service.action(team, 'retry', { ...current, expectedRevision: current.revision, requestId: 'retry' }) }
    }
  } catch (err) { error = (err as Error).message; status = (err as any).status ?? null }
  const current: any = await store.readTeamPlan(team)
  const normalize = (value: any) => { if (!value || typeof value !== 'object') return value; if (Array.isArray(value)) return value.map(normalize); return Object.fromEntries(Object.entries(value).map(([key, v]) => [key, key === 'planId' || key === 'incarnationId' ? `<${key}>` : normalize(v)])) }
  rows.push({ plan: normalize(current), calls, error, status })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
