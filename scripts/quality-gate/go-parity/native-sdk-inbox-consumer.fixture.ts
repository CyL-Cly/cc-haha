import { mock } from 'bun:test'
import { readFileSync, realpathSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'SDK inbox consumer')
if (process.env.CC_HAHA_NATIVE_SDK_INBOX_CONSUMER_FIXTURE !== '1') throw new Error('Unowned inbox consumer fixture')
const absent = absentManagedSettingsFixturePaths()
for (const start of new Set([home, realpathSync(home)])) {
  for (let ancestor = dirname(start); ; ancestor = dirname(ancestor)) {
    absent.push(join(ancestor, '.git'), join(ancestor, '.claude', 'skills'), join(ancestor, '.claude', 'workflows'))
    if (dirname(ancestor) === ancestor) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture', BUILD_TIME: '' }
const originals = {
  query: { ...await import('../../../src/query.js') },
  input: { ...await import('../../../src/utils/processUserInput/processUserInput.js') },
  storage: { ...await import('../../../src/utils/sessionStorage.js') },
  context: { ...await import('../../../src/utils/queryContext.js') },
}
const { createSessionMessageInbox, sessionMessageUuid, isPendingSessionMessage } = await import('../../../src/utils/sessionMessageInbox.js')
let attachment: any
let writes: any[] = []
let mode = ''
let idle = false
let deliveryUUID = ''
let initialRecord: Promise<unknown> = Promise.resolve()
let flushBegin: () => void = () => {}
let flushRelease: Promise<void> = Promise.resolve()
mock.module('../../../src/utils/queryContext.js', () => ({ ...originals.context, fetchSystemPromptParts: async () => ({ defaultSystemPrompt: [], userContext: {}, systemContext: {} }) }))
mock.module('../../../src/utils/processUserInput/processUserInput.js', () => ({ ...originals.input, processUserInput: async (options: any) => ({ messages: [{ type: 'user', uuid: options.uuid ?? 'initial-user', isMeta: true, timestamp: '2026-10-08T00:00:00.000Z', message: { role: 'user', content: options.input } }], shouldQuery: true, allowedTools: [] }) }))
mock.module('../../../src/utils/sessionStorage.js', () => ({ ...originals.storage,
  recordTranscript: async (messages: any[]) => {
    if (messages.some(message => message === attachment || idle && message.type === 'user' && message.uuid === deliveryUUID)) {
      if (mode === 'record-failure') throw new Error('private record failure')
      writes = JSON.parse(JSON.stringify(messages))
    }
    const recorded = originals.storage.recordTranscript(messages)
    if (!messages.some(message => message === attachment)) initialRecord = recorded
    await recorded
  },
  flushSessionStorage: async () => { await originals.storage.flushSessionStorage(); flushBegin(); await flushRelease; if (mode === 'flush-failure') throw new Error('private flush failure') },
}))
mock.module('../../../src/query.js', () => ({ ...originals.query, query: async function* () { await initialRecord; if (!idle) yield attachment; throw new Error('private query boundary') } }))
const { QueryEngine } = await import('../../../src/QueryEngine.js')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore.js')
const stateModule = await import('../../../src/bootstrap/state.js')
stateModule.setCwdState(home)
stateModule.setOriginalCwd(home)
stateModule.setProjectRoot(home)
stateModule.setIsInteractive(false)
const config = await import('../../../src/utils/config.js')
config.enableConfigs()
const rows = []
for (const profile of input.profiles) {
  mode = profile.failure ?? ''
  idle = !!profile.idle
  initialRecord = Promise.resolve()
  writes = []
  originals.storage.resetProjectForTesting()
  stateModule.setSessionPersistenceDisabled(!!profile.disabled)
  stateModule.switchSession(profile.sessionId ?? crypto.randomUUID() as any)
  const receipts: any[] = []
  const id = profile.messageId ?? 'busy-durable-message'
  const sourceUUID = sessionMessageUuid(id)
  deliveryUUID = sourceUUID
  const inbox = createSessionMessageInbox(() => {}, receipt => receipts.push(receipt))
  const request = { subtype: 'enqueue_session_message', message_id: id, sender_session_id: 'peer', text: 'Please acknowledge progress' }
  inbox.accept(request)
  attachment = { type: 'attachment', uuid: 'random-attachment-id', timestamp: profile.timestamp ?? '2026-10-08T00:00:00.000Z', attachment: { type: 'queued_command', source_uuid: sourceUUID, prompt: profile.prompt ?? 'Please acknowledge progress', isMeta: true } }
  let state = getDefaultAppState()
  let release: () => void = () => {}
  let began: () => void = () => {}
  const started = new Promise<void>(resolve => { began = resolve })
  flushBegin = began
  flushRelease = new Promise<void>(resolve => { release = resolve })
  const engine = new QueryEngine({ cwd: home, tools: [], commands: [], mcpClients: [], agents: [], readFileCache: new Map() as any,
    customSystemPrompt: 'fixture', userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' },
    canUseTool: async () => ({ behavior: 'allow', updatedInput: {} }), getAppState: () => state, setAppState: update => { state = update(state) },
    replayUserMessages: profile.replay, sessionPersistenceDisabled: profile.disabled,
  } as any)
  const iterator = engine.submitMessage(idle ? 'Please acknowledge progress' : 'normal user', idle ? { uuid: sourceUUID, isMeta: true } : undefined)
  const output: any[] = []
  let failure = ''
  const running = (async () => { try { for await (const message of iterator) output.push(message) } catch (error: any) { failure = error.message } })()
  let before: any = null
  if (!profile.disabled && mode !== 'record-failure') {
    await started
    before = { pending: isPendingSessionMessage(sourceUUID), receipts: receipts.length, duplicate: inbox.accept(request), canonicalUUID: writes.at(-1)?.uuid }
    release()
  }
  await running
  await originals.storage.flushSessionStorage()
  let persisted: any[] = []
  try { persisted = readFileSync(originals.storage.getTranscriptPath(), 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) } catch (error: any) { if (error.code !== 'ENOENT') throw error }
  originals.storage.clearSessionMessagesCache()
  const persistedDelivery = await originals.storage.doesMessageExistInSession(stateModule.getSessionId(), sourceUUID as any)
  const replay = output.filter(message => message.type === 'user' && message.isReplay)
  const finalPending = isPendingSessionMessage(sourceUUID)
  inbox.dispose()
  const cold = createSessionMessageInbox(() => {}, () => {}, persisted)
  const duplicate = cold.accept(request)
  cold.dispose()
  rows.push({ sourceUUID, canonicalUUID: idle ? sourceUUID : attachment.uuid, before, finalPending, receipts, replay, duplicate, persistedDelivery, persistedTypes: persisted.map(row => row.type), failure })
}
process.stdout.write(JSON.stringify({ rows, capabilities }) + '\n')
