import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
import { mock } from 'bun:test'
import { dirname, join } from 'node:path'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'SDK slash blocks')
if (process.env.CC_HAHA_SDK_SLASH_BLOCKS !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned SDK slash fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg'] })
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
;(globalThis as any).MACRO = { BUILD_TIME: '' }
process.chdir(input.cwd)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
const bootstrap = await import('../../../../src/bootstrap/state')
bootstrap.setCwdState(input.cwd); bootstrap.setOriginalCwd(input.cwd); bootstrap.setProjectRoot(input.cwd); bootstrap.setIsInteractive(false)
bootstrap.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../../src/utils/config'); enableConfigs()
const { getEmptyToolPermissionContext } = await import('../../../../src/Tool')
const { getCommands } = await import('../../../../src/commands')
const { processUserInput } = await import('../../../../src/utils/processUserInput/processUserInput')
const { getDefaultAppState } = await import('../../../../src/state/AppStateStore')
let state: any = { ...getDefaultAppState(), toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: input.permissionMode ?? 'default' }, mcp: { commands: [], clients: [], tools: [] }, effortValue: 'high' }
const history = input.history ?? []
const context: any = { getAppState: () => state, setAppState: (update: any) => { state = update(state) }, abortController: new AbortController(), messages: history, options: { commands: await getCommands(input.cwd), tools: [], agents: [], agentDefinitions: { activeAgents: [] }, mainLoopModel: 'fixture-model', isNonInteractiveSession: true } }
const result = await processUserInput({ input: input.content, mode: 'prompt', setToolJSX: () => { throw new Error('Unexpected local JSX') }, context, messages: history, uuid: input.uuid, querySource: 'sdk', skipAttachments: true })
const messages = result.messages.map((message: any) => { const { uuid, timestamp, ...rest } = message; return rest })
process.stdout.write(JSON.stringify({ messages, shouldQuery: result.shouldQuery, model: result.model, effort: result.effort, allowedTools: result.allowedTools, originalContent: input.content, history, blocked: capabilities.blocked }) + '\n')
