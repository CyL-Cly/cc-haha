import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process kill lifecycle')
if (process.env.CC_HAHA_TEAM_KILL_FIXTURE !== '1') throw new Error('Missing teammate kill fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setCwdState(input.homeDir)
bootstrap.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { killInProcessTeammate } = await import('../../../src/utils/swarm/spawnInProcess')
const helpers = await import('../../../src/utils/swarm/teamHelpers')
const { drainSdkEvents } = await import('../../../src/utils/sdkEventQueue')
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index]
  const teamName = `kill-${index}`
  const controller = new AbortController()
  let cleanup = 0, idle = 0
  let state: any = { teamContext: { teamName, teammates: { [`worker@${teamName}`]: { name: 'worker' }, other: { name: 'other' } } }, tasks: { actual: { ...row.task, identity: { agentId: `worker@${teamName}`, teamName, agentName: 'worker' }, abortController: controller, unregisterCleanup: () => cleanup++, onIdleCallbacks: [() => idle++] } } }
  await helpers.writeTeamFileAsync(teamName, { name: teamName, createdAt: row.now, members: [{ agentId: `worker@${teamName}`, name: 'worker' }, ...(row.duplicate ? [{ agentId: `worker@${teamName}`, name: 'duplicate' }] : []), { agentId: 'other', name: 'other' }] } as any)
  const originalNow = Date.now
  Date.now = () => row.now
  const killed = await killInProcessTeammate('actual', (update: any) => { state = update(state) })
  Date.now = originalNow
  const task = { ...state.tasks.actual }
  delete task.abortController
  delete task.unregisterCleanup
  delete task.currentWorkAbortController
  delete task.onIdleCallbacks
  rows.push({ killed, task, context: state.teamContext, members: (await helpers.readTeamFileAsync(teamName))?.members, aborted: controller.signal.aborted, cleanup, idle, sdk: drainSdkEvents().map(({ uuid, session_id, ...message }: any) => message) })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
