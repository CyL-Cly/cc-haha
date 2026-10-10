import { join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual teammate required plan approval tool')
if (process.env.CC_HAHA_TEAM_PLAN_APPROVAL_FIXTURE !== '1') throw new Error('Missing required teammate plan fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir); bootstrap.setOriginalCwd(input.homeDir); bootstrap.setProjectRoot(input.homeDir); bootstrap.switchSession(input.sessionId)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const { ExitPlanModeV2Tool } = await import('../../../src/tools/ExitPlanModeTool/ExitPlanModeV2Tool')
const plans = await import('../../../src/utils/plans')
const teammate = await import('../../../src/utils/teammate')
const contexts = await import('../../../src/utils/teammateContext')
const { readMailbox } = await import('../../../src/utils/teammateMailbox')
const results = []
for (const row of input.rows) {
 plans.setPlanSlug(input.sessionId, row.slug)
 let state: any = { toolPermissionContext: { mode: 'plan' }, tasks: { task: { id: 'task', type: 'in_process_teammate', identity: row.identity, awaitingPlanApproval: false } } }
 const context: any = { agentId: row.agentId, options: { tools: [], mainLoopModel: 'fixture-model' }, getAppState: () => state, setAppState: (update: any) => { state = update(state) } }
 const execute = async () => {
  const path = plans.getPlanFilePath(row.agentId)
  if (row.plan !== null) { await mkdir(join(path, '..'), { recursive: true }); await writeFile(path, row.plan) }
  const permission = await ExitPlanModeV2Tool.checkPermissions({}, context)
  try {
   const result = await ExitPlanModeV2Tool.call({}, context)
   const mapped = ExitPlanModeV2Tool.mapToolResultToToolResultBlockParam(result.data, 'actual-plan-tool')
   return { result: result.data, mapped, permission, mode: state.toolPermissionContext.mode, awaiting: state.tasks.task.awaitingPlanApproval, mailbox: await readMailbox('team-lead',row.identity.teamName) }
  } catch (error) { return { error: String((error as Error).message), permission, mode: state.toolPermissionContext.mode, awaiting: state.tasks.task.awaitingPlanApproval } }
 }
 if (row.inProcess) results.push(await contexts.runWithTeammateContext(contexts.createTeammateContext({ ...row.identity, abortController: new AbortController() }), execute))
 else { teammate.setDynamicTeamContext(row.identity); results.push(await execute()); teammate.setDynamicTeamContext(undefined) }
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ results, capabilities }), () => resolve()))
process.exit(0)
