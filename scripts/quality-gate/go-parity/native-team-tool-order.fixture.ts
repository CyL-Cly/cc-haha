import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Team tool configuration/error ordering')
if (process.env.CC_HAHA_TEAM_TOOL_ORDER_FIXTURE !== '1') throw new Error('Missing Team order fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let path = dirname(input.homeDir);; path = dirname(path)) { absent.push(join(path, '.git')); if (dirname(path) === path) break }
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
process.chdir(input.homeDir)
bootstrap.setCwdState(process.cwd()); bootstrap.setOriginalCwd(process.cwd()); bootstrap.setProjectRoot(process.cwd()); bootstrap.setIsInteractive(false)
bootstrap.switchSession('10000000-0000-4000-8000-000000000001' as any)
const config = await import('../../../src/utils/config'); config.enableConfigs()
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'; process.env.CC_HAHA_TEAM_REVIEW_REQUIRED = '1'
const { TeamCreateTool } = await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
const { TeamPlanTool } = await import('../../../src/tools/TeamPlanTool/TeamPlanTool')
const { writeTeamFileAsync } = await import('../../../src/utils/swarm/teamHelpers')
const { ensureTeamDraft } = await import('../../../src/utils/swarm/teamPlanStore')
const { createTask, getCanonicalTeamTaskListId } = await import('../../../src/utils/tasks')
let state: any = { mainLoopModel: 'fixture-model' }
const context: any = {
  options: { mainLoopModel: 'fixture-model', get agentDefinitions() { throw new Error('ACTUAL_CATALOG_READ_UNAVAILABLE') } },
  getAppState: () => state, setAppState: (update: any) => { state = update(state) }, abortController: new AbortController(),
}
const errors = []
process.env.CC_HAHA_TEAM_LEADER_RUNTIME = 'invalid json'
for (const plan of [undefined, { members: [], tasks: [] }]) {
  try { await TeamCreateTool.call({ team_name: 'configuration-order', ...(plan ? { plan } : {}) }, context) }
  catch (error) { errors.push((error as Error).message) }
}
const team = 'task-capture-order'
await writeTeamFileAsync(team, { name: team, leadSessionId: bootstrap.getSessionId(), createdAt: 1000, members: [] } as any)
const draft = await ensureTeamDraft(team, bootstrap.getSessionId(), { providerId: 'fixture-provider', modelId: 'fixture-model' }, { workDir: input.homeDir, members: [{ id: 'm', name: 'worker', prompt: 'actual', agentType: 'general-purpose', runtime: { providerId: 'fixture-provider', modelId: 'fixture-model' } }], tasks: [] } as any)
await createTask(getCanonicalTeamTaskListId(team), { subject: 'outside dependency', description: 'actual', status: 'pending', owner: 'worker', blocks: [], blockedBy: ['missing'] })
try { await TeamPlanTool.call({ team_name: team, operation: 'submit', expected_revision: draft.revision }, context) }
catch (error) { errors.push((error as Error).message) }
process.stdout.write(JSON.stringify({ errors, capabilities }))
