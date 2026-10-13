import { mock, spyOn } from 'bun:test'
import { readFileSync, realpathSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'SDK system output')
if (process.env.CC_HAHA_NATIVE_SDK_SYSTEM_OUTPUT_FIXTURE !== '1') throw new Error('Unowned SDK system output fixture')
const absent = absentManagedSettingsFixturePaths()
for (const start of new Set([home, realpathSync(home)])) {
  for (let path = dirname(start); ; path = dirname(path)) {
    absent.push(join(path, '.git'), join(path, '.claude', 'skills'), join(path, '.claude', 'workflows'))
    if (dirname(path) === path) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture', BUILD_TIME: '' }
const query = { ...await import('../../../src/query.js') }
const userInput = { ...await import('../../../src/utils/processUserInput/processUserInput.js') }
const context = { ...await import('../../../src/utils/queryContext.js') }
let messages: any[] = []
mock.module('../../../src/utils/queryContext.js', () => ({ ...context, fetchSystemPromptParts: async () => ({ defaultSystemPrompt: [], userContext: {}, systemContext: {} }) }))
mock.module('../../../src/utils/processUserInput/processUserInput.js', () => ({ ...userInput, processUserInput: async (options: any) => ({ messages: [{ type: 'user', uuid: options.uuid, timestamp: '2026-10-08T00:00:00.000Z', message: { role: 'user', content: options.input } }], shouldQuery: true, allowedTools: [] }) }))
// Supply original query yields only. The actual QueryEngine, system dispatch,
// mutable history, writer and next-ASK replay remain the Source implementation.
mock.module('../../../src/query.js', () => ({ ...query, query: async function* () { for (const message of messages) yield message; throw new Error('private bounded query boundary') } }))
const { QueryEngine } = await import('../../../src/QueryEngine.js')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore.js')
const bootstrap = await import('../../../src/bootstrap/state.js')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config.js')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage.js')
const pendingWrites: Promise<unknown>[] = []
const originalRecord = storage.recordTranscript
spyOn(storage, 'recordTranscript').mockImplementation((...args) => {
  const pending = originalRecord(...args)
  pendingWrites.push(pending)
  return pending
})
const rows = []
for (const profile of input.profiles) {
  storage.resetProjectForTesting()
  bootstrap.setSessionPersistenceDisabled(false)
  bootstrap.switchSession(profile.sessionId)
  messages = profile.messages
  let state = getDefaultAppState()
  const engine = new QueryEngine({ cwd: home, tools: [], commands: [], mcpClients: [], agents: [], readFileCache: new Map() as any,
    customSystemPrompt: 'fixture', userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' },
    canUseTool: async () => ({ behavior: 'allow', updatedInput: {} }), getAppState: () => state, setAppState: update => { state = update(state) },
  } as any)
  const output: any[] = []
  for (let turn = 0; turn < 2; turn++) {
    try { for await (const value of engine.submitMessage('ordinary user', { uuid: `original-user-${turn}` })) if (value.type === 'system' && ['api_retry', 'streaming_fallback'].includes(value.subtype)) output.push(value) } catch (error: any) { if (error.message !== 'private bounded query boundary') throw error }
    // Bare mode deliberately starts Record without awaiting it. Observe and
    // join those actual promises before inspecting the persisted transcript.
    await Promise.all(pendingWrites.splice(0))
    await storage.flushSessionStorage()
    messages = []
  }
  const persisted = readFileSync(storage.getTranscriptPath(), 'utf8').trim().split('\n').map(line => JSON.parse(line))
  rows.push({ output, history: engine.getMessages().filter(value => value.type === 'system'), persisted: persisted.filter(value => value.type === 'system') })
}
if (capabilities.blocked.length) throw new Error('SDK system fixture left disposable capabilities: ' + JSON.stringify(capabilities.blocked))
process.stdout.write(JSON.stringify({ rows, capabilities }))
