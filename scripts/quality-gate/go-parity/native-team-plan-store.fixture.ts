import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team plan store')
if (process.env.CC_HAHA_TEAM_PLAN_STORE_FIXTURE !== '1') throw new Error('Missing team plan store fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const source = await import('../../../src/utils/swarm/teamPlanStore')
const { writeTeamFileAsync, getTeamDir } = await import('../../../src/utils/swarm/teamHelpers')
await writeTeamFileAsync('source-team', { name: 'source-team', members: [], leadSessionId: 'source-session', createdAt: 1000, future: true } as any)
let count = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `00000000-0000-4000-8000-${String(++count).padStart(12, '0')}`)
spyOn(Date, 'now').mockImplementation(() => 2000)
const rows = []
for (const action of input.actions) {
  const current = await source.readTeamPlan('source-team')
  const identity = current ? { planId: current.planId, sessionId: current.sessionId, incarnationId: current.incarnationId, expectedRevision: action.stale ? 0 : current.revision } : null
  let result: any = null, error: string | null = null, status: number | null = null
  try {
    if (action.type === 'ensure') result = await source.ensureTeamDraft('source-team', 'source-session', action.runtime, { ...action.initial, workDir: input.homeDir })
    if (action.type === 'replace') result = await source.replaceTeamPlan('source-team', identity!, action.patch, { preserveReview: action.preserveReview })
    if (action.type === 'submit') result = await source.submitTeamPlan('source-team', identity!, action.patch)
    if (action.type === 'approve') result = await source.approveTeamPlan('source-team', identity!, action.requestId, current!)
    if (action.type === 'mutate') result = await source.mutateTeamPlan('source-team', identity!, plan => ({ ...plan, ...action.patch }))
    if (action.type === 'stage') result = await source.stageMember('source-team', action.member)
    if (action.type === 'approved') result = await source.isTeamExecutionApproved('source-team', action.member)
    if (action.type === 'find') result = await source.findTeamPlanForSession('source-session')
  } catch (err) { error = (err as Error).message; status = (err as any).status ?? null }
  let physical: any = null
  try { physical = JSON.parse(await readFile(`${getTeamDir('source-team')}/plan.json`, 'utf8')) } catch {}
  rows.push({ result, error, status, physical })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
