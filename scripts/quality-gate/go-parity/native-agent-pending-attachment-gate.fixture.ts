import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual running Agent attachment gate')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_AGENT_PENDING_GATE !== '1') throw new Error('Pinned owned Agent gate fixture required')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [
  ...absentManagedSettingsFixturePaths(),
  '/home/claude/.claude/remote/.api_key',
  '/home/claude/.claude/remote/.oauth_token',
  join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg',
] })
// Replace only the absent external keychain primitive, as in the existing
// original Skill attachment fixture. All attachment producers remain original.
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
;(globalThis as any).MACRO = { VERSION: 'fixture', BUILD_TIME: '' }
await import('../../../preload')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
;(await import('../../../src/utils/config')).enableConfigs()
const framework = await import('../../../src/utils/task/framework')
const { queuePendingMessage } = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
const { getAttachments } = await import('../../../src/utils/attachments')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const results = []
for (const row of input.cases) {
  process.env.CLAUDE_CODE_DISABLE_ATTACHMENTS = row.disabled
  process.env.CLAUDE_CODE_SIMPLE = row.simple
  const id = 'a0123456789abcdef'
  let current: any = { tasks: {}, toolPermissionContext: getEmptyToolPermissionContext(), mcp: { commands: [] }, speculation: { status: 'inactive' } }
  const setAppState = (update: any) => { current = update(current) }
  framework.registerTask({ id, type: 'local_agent', status: 'running', description: 'Actual pending gate', startTime: 1, pendingMessages: [], agentType: 'general-purpose' } as any, setAppState)
  queuePendingMessage(id, 'ACTUAL_PENDING_GATE_C7A2', setAppState)
  const context: any = { agentId: id, getAppState: () => current, setAppState, abortController: new AbortController(), options: { tools: [], mcpClients: [], agents: [], agentDefinitions: { activeAgents: [] }, mainLoopModel: 'fixture-model' }, messages: [], readFileState: new Map(), nestedMemoryAttachmentTriggers: new Set(), dynamicSkillDirTriggers: new Set() }
  const attachments = await getAttachments(null, context, null, [], [], 'agent')
  results.push({ disabled: row.disabled, simple: row.simple, pending: current.tasks[id].pendingMessages, attachments: attachments.filter(value => value.type === 'queued_command') })
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, blocked: capabilities.blocked }), error => error ? reject(error) : resolve()))
process.exit(0)
