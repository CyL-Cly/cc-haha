import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual SendMessage structured protocol')
if (process.env.CC_HAHA_INPROCESS_TEAM_PROTOCOL_FIXTURE !== '1') throw new Error('Missing structured teammate fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.homeDir)
state.setCwdState(input.homeDir)
state.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { SendMessageTool } = await import('../../../src/tools/SendMessageTool/SendMessageTool')
const helpers = await import('../../../src/utils/swarm/teamHelpers')
const mailbox = await import('../../../src/utils/teammateMailbox')
const { runWithTeammateContext } = await import('../../../src/utils/teammateContext')
const Calendar = globalThis.Date
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index]
  const team = `actual-protocol-${index}`
  globalThis.Date = class extends Calendar {
    constructor(...values: any[]) { super(values.length ? values[0] : row.now) }
    static now() { return row.now }
  } as any
  await helpers.writeTeamFileAsync(team, { name: team, createdAt: row.now, leadSessionId: 'parent', members: [{ agentId: `worker@${team}`, name: 'worker', tmuxPaneId: 'in-process', backendType: 'in-process' }] } as any)
  const abort = new AbortController()
  const current: any = { teamContext: { teamName: team, leadAgentId: `team-lead@${team}` }, toolPermissionContext: { mode: row.mode ?? 'default' }, tasks: { tfixture: { type: 'in_process_teammate', status: 'running', identity: { agentId: `worker@${team}` }, abortController: abort } } }
  const context: any = { getAppState: () => current, options: {} }
  let result: any = null, failure: any = null, validation: any = null
  const run = async () => {
    validation = await SendMessageTool.validateInput(row.input, context)
    if (validation.result) {
      try { result = (await SendMessageTool.call(row.input, context, async (_tool: any, value: any) => ({ behavior: 'allow', updatedInput: value }), undefined as any)).data }
      catch (error) { failure = (error as Error).message }
    }
  }
  if (row.worker) await runWithTeammateContext({ agentId: `worker@${team}`, agentName: 'worker', teamName: team, color: 'blue', planModeRequired: false, parentSessionId: 'parent', isInProcess: true, abortController: abort }, run)
  else await run()
  rows.push({ result, failure, validation, aborted: abort.signal.aborted, leader: await mailbox.readMailbox('team-lead', team), worker: await mailbox.readMailbox('worker', team) })
}
globalThis.Date = Calendar
process.stdout.write(JSON.stringify({ rows, capabilities }))
