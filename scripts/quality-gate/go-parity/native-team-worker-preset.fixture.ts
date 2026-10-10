import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { dirname, join } from 'node:path'
import { realpathSync } from 'node:fs'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team worker actual preset lifecycle')
if (process.env.CC_HAHA_TEAM_WORKER_PRESET_FIXTURE !== '1') throw new Error('Missing Team preset fixture')
const absent = [...absentManagedSettingsFixturePaths(), join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']
for (const parent of [dirname(input.homeDir), dirname(input.cwd), dirname(realpathSync(input.cwd))]) {
  for (let path = parent; ; path = dirname(path)) {
    absent.push(join(path, '.git'), join(path, '.mcp.json'), join(path, '.claude', 'skills'), join(path, '.claude', 'commands'), join(path, '.claude', 'workflows'), join(path, '.agents', 'skills'), join(path, '.agents', 'commands'), join(path, '.agents', 'workflows'))
    if (dirname(path) === path) break
  }
}
absent.push('/Library/Application Support/ClaudeCode/managed-mcp.json')
if (input.origin) absent.push('/home/claude/.claude/remote/.session_ingress_token')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent, ...(input.origin ? { loopbackFetchOrigin: input.origin } : {}) })
await import('../../../preload')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${input.homeDir}/managed`
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== `${input.homeDir}/managed`) throw new Error('Managed path escaped fixture')
process.env.USER_TYPE = 'external'
const bootstrap = await import('../../../src/bootstrap/state')
// ConversationService spawns the original CLI with cwd=workDir. Reproduce
// that OS cwd transition before the ordinary CLI bootstrap reads process.cwd.
process.chdir(input.cwd)
const cwd = process.cwd()
bootstrap.setCwdState(cwd)
bootstrap.setOriginalCwd(cwd)
bootstrap.setProjectRoot(cwd)
bootstrap.setInitialMainLoopModel('fixture-team-model')
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as any)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { parseAgentFromJson } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { prepareTeamWorkerPreset, activateTeamWorkerPreset } = await import('../../../src/utils/swarm/teamWorkerPreset')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const teammate = await import('../../../src/utils/teammate')
const tasks = await import('../../../src/utils/tasks')
const client = await import('../../../src/services/mcp/client')
teammate.setDynamicTeamContext({ teamName: 'MyTeam_', agentId: 'worker@MyTeam_', agentName: 'worker', planModeRequired: false })
process.env.CLAUDE_CODE_TASK_LIST_ID = 'unrelated-standalone-list'
const rows = []
for (const row of input.cases) {
  let state = getDefaultAppState()
  const agent = parseAgentFromJson('worker', { description: 'Approved team member', prompt: 'ACTUAL_APPROVED_WORKER_SYSTEM', model: 'fixture-team-model', ...row.definition }, 'flagSettings')!
  let error = null, tools: string[] = [], messages: unknown[] = []
  try {
    const baseTools = row.available.map((name: string) => ({ name, aliases: [] }))
    const parentClients = []
    if (input.origin) parentClients.push(await client.connectToServer('named', { type: 'http', url: input.origin, scope: 'project' }))
    const prepared = await prepareTeamWorkerPreset(agent, baseTools as any, parentClients as any)
    tools = prepared.tools.map(tool => tool.name)
    const context: any = { options: { commands: [], tools: prepared.tools, mainLoopModel: 'fixture-team-model', mcpClients: prepared.clients, isNonInteractiveSession: true }, getAppState: () => state, setAppState: (fn: any) => { state = fn(state) }, abortController: new AbortController(), messages: [] }
    const activated = await activateTeamWorkerPreset(agent, context)
    messages = activated.map(message => ({ type: message.type, message: message.message, isMeta: message.isMeta }))
    if (row.call) {
      const selected = prepared.tools.find(tool => tool.name === row.call.name)
      if (!selected) throw new Error('Original preset did not return actual callable MCP tool')
      const result = await selected.call(row.call.input, context, undefined as any, { message: { content: [{ type: 'tool_use', id: 'actual-source-team-call', name: selected.name, input: row.call.input }] } } as any)
      const clients = prepared.clients.map(peer => ({ name: peer.name, status: peer.type }))
      rows.push({ tools, messages, error, taskList: tasks.getTaskListId('worker@MyTeam_'), clients, result: result.data })
      continue
    }
  } catch (err) { error = (err as Error).message }
  rows.push({ tools, messages, error, taskList: tasks.getTaskListId('worker@MyTeam_') })
}
await client.clearServerCache()
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
