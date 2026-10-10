import * as fs from 'node:fs'
import * as fsp from 'node:fs/promises'
import * as crypto from 'node:crypto'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual headless session controller')
const keys = ['home', 'origin', 'temporary', 'sid', 'continue', 'forkSession', 'checkpointing', 'disabled', 'resumeInterrupted', 'resumeAt', 'model', 'sdk', 'stat', 'runtime', 'holdCopies']
if (process.env.CC_HAHA_SESSION_CONTROLLER_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.TZ !== 'UTC' || !feature('TRANSCRIPT_CLASSIFIER') || Object.keys(input).some(key => !keys.includes(key)) || !Array.isArray(input.sdk) || !input.stat || fs.realpathSync(home) !== home)
  throw new Error('Headless controller requires original canonical offline inputs')
const origin = new URL(input.origin)
if (origin.origin !== input.origin || origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port || input.origin !== process.env.ANTHROPIC_BASE_URL || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key')
  throw new Error('Headless controller requires exact original loopback')
function guard(path: string) {
  const part = relative(home, path)
  if (part === '..' || part.startsWith('..' + sep) || isAbsolute(part))
    throw new Error('Headless controller ordinary private files required')
  for (let probe = path;; probe = dirname(probe)) {
    try {
      const info = fs.lstatSync(probe)
      if (info.isSymbolicLink() || probe === path && !info.isFile())
        throw new Error('Headless controller ordinary private files required')
      const part = relative(home, fs.realpathSync(probe))
      if (part === '..' || part.startsWith('..' + sep) || isAbsolute(part))
        throw new Error('Headless controller ordinary private files required')
      break
    }
    catch (e) {
      if ((e as NodeJS.ErrnoException).code !== 'ENOENT' || probe === dirname(probe))
        throw e
    }
  }
}
for (const path of Object.keys(input.stat))
  guard(path)
guard(join(home, 'controller-result.json'))
const exactAbsentPaths: string[] = []
if (input.sdk.length)
  for (let directory = dirname(home);; directory = dirname(directory)) {
    exactAbsentPaths.push(join(directory, '.git'))
    if (dirname(directory) === directory)
      break
  }
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: input.origin, absentPaths: exactAbsentPaths })
let releaseCopy: () => void = () => { }, copyStarted = false
const copyBarrier = input.holdCopies ? new Promise<void>(resolve => {
  releaseCopy = resolve
}) : Promise.resolve()
const originalLink = fsp.link
if (input.holdCopies)
  spyOn(fsp, 'link').mockImplementation(async (oldPath, newPath) => {
    if (String(oldPath) !== join(home, '.claude', 'file-history', input.sid, 'backup') || String(newPath) !== join(home, '.claude', 'file-history', input.temporary, 'backup'))
      throw new Error('Only original private Link barrier is allowed')
    copyStarted = true
    await copyBarrier
    return originalLink(oldPath, newPath)
  })
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'policy')
process.env.CLAUDE_CODE_SIMPLE = '1'
process.env.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = String(input.checkpointing)
process.env.CLAUDE_CODE_RESUME_INTERRUPTED_TURN = input.resumeInterrupted
;(globalThis as any).MACRO = { BUILD_TIME: '', VERSION: 'fixture-version', PACKAGE_URL: '', README_URL: '', ISSUES_URL: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
bootstrap.setSessionPersistenceDisabled(input.disabled)
bootstrap.switchSession(input.temporary)
bootstrap.setSdkBetas([])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const permissions = await import('../../../src/utils/permissions/permissionSetup')
const permissionResult = await permissions.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
const { getDefaultAppState } = await import('../../../src/state/AppStateStore'), { createStore } = await import('../../../src/state/store')
const store = createStore({ ...getDefaultAppState(), toolPermissionContext: permissionResult.toolPermissionContext })
const storage = await import('../../../src/utils/sessionStorage'), recovery = await import('../../../src/utils/conversationRecovery'), engine = await import('../../../src/QueryEngine'), queue = await import('../../../src/utils/messageQueueManager')
const snapshotRecords: unknown[] = [], copyPromises: Promise<unknown>[] = [], background: {
  returnedBeforeCopy?: boolean
} = {}
const fileHistory = await import('../../../src/utils/fileHistory'), oldCopy = fileHistory.copyFileHistoryForResume
spyOn(fileHistory, 'copyFileHistoryForResume').mockImplementation((...args) => {
  const pending = oldCopy(...args)
  copyPromises.push(pending)
  return pending
})
const oldSnapshot = storage.recordFileHistorySnapshot
spyOn(storage, 'recordFileHistorySnapshot').mockImplementation((messageId, snapshot, isSnapshotUpdate) => {
  snapshotRecords.push(encode({ type: 'file-history-snapshot', messageId, snapshot, isSnapshotUpdate }))
  return oldSnapshot(messageId, snapshot, isSnapshotUpdate)
})
const { registerCleanup } = await import('../../../src/utils/cleanupRegistry')
registerCleanup(async () => {
  releaseCopy()
  await Promise.all(copyPromises)
})
const rawErrors: unknown[] = [], errors: string[] = [], phases: unknown[] = [], allocations: unknown[] = [], asks: unknown[] = []
const logging = await import('../../../src/utils/log')
logging.attachErrorLogSink({ logError: e => {
    errors.push(e.message)
    rawErrors.push({ name: e.name, message: e.message, stack: e.stack })
  }, logMCPError: (name, e) => rawErrors.push({ name, error: String(e) }), logMCPDebug: (name, message) => rawErrors.push({ name, message }), getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: name => join(home, 'mcp-' + name) })
let scope = 'startup', counter = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => {
  const value = '00000000-0000-4000-' + (scope === 'turn' ? '0002' : '0001') + '-' + (++counter).toString().padStart(12, '0')
  allocations.push({ scope, kind: 'uuid', value })
  return value as ReturnType<typeof crypto.randomUUID>
})
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) {
    super(...(args.length ? args : [1700000000000]) as [
      number
    ])
    if (!args.length)
      allocations.push({ scope, kind: 'time', value: this.toISOString() })
  }
  static now() {
    return 1700000000000
  }
} as DateConstructor
function encode(value: unknown) {
  if (value === undefined)
    return null
  return JSON.parse(JSON.stringify(value, (_k, v) => v instanceof Map ? [...v] : v instanceof Set ? [...v] : v))
}
let activationHistory: unknown[] = []
const oldLoad = recovery.loadConversationForResume
spyOn(recovery, 'loadConversationForResume').mockImplementation(async (...args) => {
  scope = 'resume'
  counter = 0
  phases.push({ phase: 'resume:start', args })
  const result = await oldLoad(...args)
  if (input.holdCopies)
    background.returnedBeforeCopy = snapshotRecords.length === 0
  phases.push({ phase: 'resume:return', result: encode(result) })
  return result
})
const restoreState = await import('../../../src/utils/sessionRestore')
const originalRestoreState = restoreState.restoreSessionStateFromLog
spyOn(restoreState, 'restoreSessionStateFromLog').mockImplementation((...args) => {
  phases.push({ phase: 'state:restore' })
  return originalRestoreState(...args)
})
const originalRestoreMetadata = storage.restoreSessionMetadata
spyOn(storage, 'restoreSessionMetadata').mockImplementation((...args) => {
  phases.push({ phase: 'metadata:restore' })
  return originalRestoreMetadata(...args)
})
const oldSwitch = bootstrap.switchSession
spyOn(bootstrap, 'switchSession').mockImplementation((...args) => {
  phases.push({ phase: 'switch', args })
  return oldSwitch(...args)
})
const oldReset = storage.resetSessionFilePointer
spyOn(storage, 'resetSessionFilePointer').mockImplementation(async () => {
  phases.push({ phase: 'reset' })
  return oldReset()
})
for (const name of ['enqueue', 'dequeue'] as const) {
  const original = queue[name]
  spyOn(queue, name).mockImplementation(((...args: unknown[]) => {
    const result = (original as any)(...args)
    phases.push({ phase: name, result: encode(result) })
    if (name === 'enqueue')
      enqueued.push(encode(queue.getCommandQueue().at(-1)))
    return result
  }) as any)
}
let activeMessages: unknown[] = []
let cache: unknown[] = []
const queryHelpers = await import('../../../src/utils/queryHelpers'), oldExtract = queryHelpers.extractReadFilesFromMessages
spyOn(queryHelpers, 'extractReadFilesFromMessages').mockImplementation((...args) => {
  const result = oldExtract(...args)
  activationHistory = encode(args[0])
  activeMessages = args[0]
  cache = [...result.entries()].map(([path, value]) => ({ path, value }))
  releaseCopy()
  return result
})
const oldAsk = engine.ask
spyOn(engine, 'ask').mockImplementation(async function* (args) {
  scope = 'turn'
  counter = 0
  activeMessages = args.mutableMessages
  activationHistory = encode(args.mutableMessages)
  cache = [...args.getReadFileCache().entries()].map(([path, value]) => ({ path, value }))
  asks.push({ before: encode(args.mutableMessages), cache: encode(cache) })
  phases.push({ phase: 'ask:start' })
  yield* oldAsk(args)
  phases.push({ phase: 'ask:end', messages: encode(args.mutableMessages) })
})
const uuidChecks: unknown[] = [], enqueued: unknown[] = [], replays: unknown[] = []
const oldExists = storage.doesMessageExistInSession
spyOn(storage, 'doesMessageExistInSession').mockImplementation(async (sid, uuid) => {
  const exists = await oldExists(sid, uuid)
  uuidChecks.push({ uuid, exists })
  return exists
})
const { Stream } = await import('../../../src/utils/stream')
const oldOutput = Stream.prototype.enqueue
spyOn(Stream.prototype, 'enqueue').mockImplementation(function (value) {
  if (value?.type === 'user' && value.isReplay)
    replays.push(encode(value))
  return oldOutput.call(this, value)
})
const lifecycle = await import('../../../src/utils/commandLifecycle'), oldLifecycle = lifecycle.notifyCommandLifecycle
spyOn(lifecycle, 'notifyCommandLifecycle').mockImplementation((uuid, event) => {
  phases.push({ phase: 'lifecycle:' + event, uuid })
  return oldLifecycle(uuid, event)
})
const records: unknown[] = [], oldRecord = storage.recordTranscript
spyOn(storage, 'recordTranscript').mockImplementation(async (...args) => {
  records.push(encode(args[0]))
  phases.push({ phase: 'record', messages: encode(args[0]) })
  return oldRecord(...args)
})
const oldFlush = storage.flushSessionStorage
spyOn(storage, 'flushSessionStorage').mockImplementation(async () => {
  phases.push({ phase: 'flush' })
  return oldFlush()
})
const requests: string[] = [], fetchOriginal = fetch
;(globalThis as any).fetch = (async (url: unknown, init?: RequestInit) => {
  if (init?.method === 'POST') {
    requests.push(String(init.body))
    phases.push({ phase: 'HTTP', messages: encode(activeMessages) })
  }
  return fetchOriginal(url as any, init)
})
function transcripts(directory = join(home, '.claude', 'projects'), result: Record<string, string> = {}) {
  try {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isDirectory())
        transcripts(path, result)
      else if (entry.isFile()) {
        guard(path)
        result[relative(home, path)] = fs.readFileSync(path, 'utf8')
      }
      else
        throw new Error('unexpected fixture symlink')
    }
  }
  catch (e) {
    if ((e as NodeJS.ErrnoException).code !== 'ENOENT')
      throw e
  }
  return result
}
process.on('exit', code => {
  const state = store.getState(), out = encode({ activationHistory, snapshotRecords, background, appState: { FileHistory: state.fileHistory, Todos: state.todos }, cache, uuidChecks, enqueued, replays, boundaryOrder: phases.slice(0, phases.some((value: any) => value.phase === 'HTTP') ? phases.findIndex((value: any) => value.phase === 'HTTP') + 1 : phases.length).filter((value: any) => ['resume:start', 'resume:return', 'switch', 'reset', 'state:restore', 'metadata:restore', 'enqueue', 'lifecycle:started', 'lifecycle:completed', 'ask:start', 'record', 'HTTP'].includes(value.phase) || value.phase === 'dequeue' && value.result).map((value: any) => value.phase), historySource: activeMessages.map(value => JSON.stringify(value)), requests, transcripts: transcripts(), copies: transcripts(join(home, '.claude', 'file-history')), errors })
  // SDK acknowledgements, allocation stream and all post-request host output are
  // retained raw alongside the finite migrated boundary; none feeds Go input.
  fs.writeFileSync(join(home, 'controller-result.json'), JSON.stringify({ observation: out, sourceOnly: { code, phases, asks, records, allocations, capabilities, exactAbsentPaths, rawErrors, permissionResult, copyStarted } }))
})
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless((async function* () {
  for (const message of input.sdk)
    yield JSON.stringify(message) + '\n'
})(), () => store.getState(), store.setState, [], [], {}, [], { resume: input.continue ? undefined : input.sid, continue: input.continue, resumeSessionAt: input.resumeAt || undefined, verbose: true, outputFormat: 'stream-json', jsonSchema: undefined, permissionPromptToolName: undefined, allowedTools: [], thinkingConfig: { type: 'disabled' }, maxTurns: 1, maxBudgetUsd: undefined, taskBudget: undefined, systemPrompt: 'original custom system', appendSystemPrompt: undefined, userSpecifiedModel: input.model, fallbackModel: undefined, teleport: undefined, sdkUrl: undefined, replayUserMessages: true, includePartialMessages: true, forkSession: input.forkSession, rewindFiles: undefined, enableAuthStatus: false, agent: undefined, workload: undefined })
