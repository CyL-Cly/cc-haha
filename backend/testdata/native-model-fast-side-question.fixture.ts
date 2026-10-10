import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MODEL_COMMAND_FIXTURE !== '1') throw new Error('Original fast SideQuestion requires a private fixture and pinned Bun')
const home = assertDisposableFixtureHome(input.home, 'fast SideQuestion actual query')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'], loopbackFetchOrigin: input.origin })
fs.writeFileSync(join(home, 'root.pem'), input.certificate)
process.env.NODE_EXTRA_CA_CERTS = join(home, 'root.pem')
await import('../../preload')
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
const config = await import('../../src/utils/config')
config.enableConfigs()
const { normalizeApiKeyForConfig } = await import('../../src/utils/authPortable')
config.saveGlobalConfig(current => ({ ...current, penguinModeOrgEnabled: true, customApiKeyResponses: { approved: [normalizeApiKeyForConfig(process.env.ANTHROPIC_API_KEY!)], rejected: [] } }))
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify({ model: 'opus', fastMode: true, future: true }))
const { resetSettingsCache } = await import('../../src/utils/settings/settingsCache')
resetSettingsCache()
const fast = await import('../../src/utils/fastMode')
fast.resolveFastModeStatusFromCache()
const { createStore } = await import('../../src/state/store')
const { getDefaultAppState } = await import('../../src/state/AppState')
const { onChangeAppState } = await import('../../src/state/onChangeAppState')
const store = createStore({ ...getDefaultAppState(), mainLoopModel: 'opus', fastMode: fast.getInitialFastModeSetting('opus') }, onChangeAppState)
if (!store.getState().fastMode) throw new Error('Actual original fast initializer denied fixture')
const { query } = await import('../../src/query')
const { runSideQuestion } = await import('../../src/utils/sideQuestion')
const { createUserMessage } = await import('../../src/utils/messages')
const { createFileStateCacheWithSizeLimit } = await import('../../src/utils/fileStateCache')
const { asSystemPrompt } = await import('../../src/utils/systemPromptType')
const { parseUserSpecifiedModel } = await import('../../src/utils/model/model')
const systemPrompt = asSystemPrompt(['Use the actual tools to execute the requested test.'])
const history: any[] = []
const context = { options: { commands: [], debug: false, mainLoopModel: parseUserSpecifiedModel('opus'), tools: [], verbose: false, thinkingConfig: { type: 'adaptive' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [], allAgents: [] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(100), getAppState: () => store.getState(), setAppState: store.setState, setInProgressToolUseIDs: () => {}, setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {}, messages: history } as any
const parent = async () => {
  history.push(createUserMessage({ content: 'PARENT_FAST' }))
  for await (const message of query({ messages: history, systemPrompt, userContext: {}, systemContext: {}, canUseTool: async () => ({ behavior: 'deny', message: 'fixture' }), toolUseContext: context, querySource: 'repl_main_thread', maxTurns: 1 })) {
    if (message.type === 'assistant') history.push(message)
  }
}
if (!input.fallback) await parent()
if (input.switchModel) {
  const React = await import('react')
  const { AppStoreContext } = await import('../../src/state/AppState')
  const { call } = await import('../../src/commands/model/model')
  const internals = (React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE
  const previous = internals.H
  const effects: Array<() => void> = []
  internals.H = { useMemoCache: (n: number) => Array(n).fill(Symbol.for('react.memo_cache_sentinel')), useContext: (context: unknown) => context === AppStoreContext ? store : null, useSyncExternalStore: (_subscribe: unknown, snapshot: () => unknown) => snapshot(), useEffect: (effect: () => void) => effects.push(effect), useCallback: (callback: unknown) => callback }
  try {
    const element = await call(() => {}, { options: context.options, getAppState: () => store.getState() } as any, 'sonnet')
    element.type(element.props)
    for (const effect of effects) effect()
  } finally { internals.H = previous }
}

const before = JSON.stringify(history)
const result = await runSideQuestion({ question: 'SIDE_FAST', cacheSafeParams: { systemPrompt, userContext: {}, systemContext: {}, toolUseContext: context, forkContextMessages: history } })
const unchanged = before === JSON.stringify(history)
if (input.fallback) await parent()
context.options.mainLoopModel = parseUserSpecifiedModel(store.getState().mainLoopModel ?? 'opus')
history.push(createUserMessage({ content: 'PARENT_NEXT_FAST' }))
for await (const message of query({ messages: history, systemPrompt, userContext: {}, systemContext: {}, canUseTool: async () => ({ behavior: 'deny', message: 'fixture' }), toolUseContext: context, querySource: 'repl_main_thread', maxTurns: 1 })) {}
if (capabilities.blocked.length) throw new Error('Original fast SideQuestion denied: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ response: result.response, parentUnchanged: unchanged, fast: store.getState().fastMode }) + '\n')
process.exit(0)
