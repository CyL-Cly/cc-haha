import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import * as fsPromises from 'node:fs/promises'
import { lstatSync, realpathSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as {
  home: string, currentSessionId: string, source?: string, jsonlPath?: string,
  bare: boolean, checkpointing: boolean, environmentKind: string, policyPath: string,
  stat: Record<string, { modified: number, birth: number, size: number }>, holdCopies: boolean,
  request?: { origin: string, promptSource: string, runtime: { platform: string, arch: string, version: string } },
  uuidProbes?: string[],
  liteReadFault?: { path: string, atRead: 2 },
}
const home = assertDisposableFixtureHome(input.home, 'Resume state')
if (process.env.CC_HAHA_RESUME_STATE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || typeof input.bare !== 'boolean' || typeof input.checkpointing !== 'boolean' || typeof input.holdCopies !== 'boolean' || !input.stat || Object.keys(input).some(name => !['home', 'currentSessionId', 'source', 'jsonlPath', 'bare', 'checkpointing', 'environmentKind', 'policyPath', 'stat', 'holdCopies', 'request', 'uuidProbes', 'liteReadFault'].includes(name))) throw new Error('Resume state requires original offline inputs')
if (input.uuidProbes && (!Array.isArray(input.uuidProbes) || input.uuidProbes.some(value => typeof value !== 'string') || input.source === undefined || typeof JSON.parse(input.source) !== 'string' || input.jsonlPath)) throw new Error('UUID probes require an original SID selector')
if (input.request) {
  const request = input.request, origin = new URL(request.origin)
  if (origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port || origin.origin !== request.origin || request.origin !== process.env.ANTHROPIC_BASE_URL || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key' || typeof request.promptSource !== 'string' || request.runtime.platform !== process.platform || request.runtime.arch !== process.arch || request.runtime.version !== process.version || Object.keys(request).some(name => !['origin', 'promptSource', 'runtime'].includes(name))) throw new Error('Resume state request requires original private loopback inputs')
}
function guard(path: string) {
  const part = relative(home, path)
  if (part === '..' || part.startsWith('..' + sep) || isAbsolute(part)) throw new Error('Resume state requires ordinary private files')
  for (let probe = path;; probe = dirname(probe)) {
    try {
      const info = lstatSync(probe), part = relative(realpathSync(home), realpathSync(probe))
      if (info.isSymbolicLink() || part === '..' || part.startsWith('..' + sep) || isAbsolute(part)) throw new Error('Resume state requires ordinary private files')
      return
    } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT' || probe === dirname(probe)) throw error }
  }
}
guard(input.policyPath); guard(join(home, 'resume-state-result.json'))
if (input.jsonlPath) guard(input.jsonlPath)
for (const path of Object.keys(input.stat)) guard(path)
if (input.liteReadFault) {
  if (input.source !== undefined || input.jsonlPath || input.liteReadFault.atRead !== 2 || !input.stat[input.liteReadFault.path] || input.stat[input.liteReadFault.path]!.size <= 65536 || Object.keys(input.liteReadFault).some(name => !['path', 'atRead'].includes(name))) throw new Error('Lite read fault requires exact original private continue path')
  guard(input.liteReadFault.path)
}
if (input.request) guard(join(home, '.claude', '.claude.json'))
// The observed non-bare auth caller probes this fixed CCR credential path.
// Provide an explicit absent snapshot, never a grant to read external state.
const capabilities = await installClientFixtureCapabilities(home, input.request ? { loopbackFetchOrigin: input.request.origin, absentPaths: ['/home/claude/.claude/remote/.oauth_token'] } : undefined)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = dirname(input.policyPath)
process.env.CLAUDE_CODE_SIMPLE = input.bare ? '1' : '0'
process.env.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = input.checkpointing ? '1' : '0'
process.env.CLAUDE_CODE_ENVIRONMENT_KIND = input.environmentKind
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
bootstrap.switchSession(input.currentSessionId as Parameters<typeof bootstrap.switchSession>[0])
const analytics = await import('../../../src/services/analytics'), logging = await import('../../../src/utils/log'), debug = await import('../../../src/utils/debug')
const diagnostics: unknown[] = [], errors: unknown[] = [], rawErrors: unknown[] = [], allocations: unknown[] = []
analytics.attachAnalyticsSink({ logEvent: (name, metadata) => { diagnostics.push({ kind: 'event', name, fields: metadata }) }, logEventAsync: async (name, metadata) => { diagnostics.push({ kind: 'event', name, fields: metadata }) } })
logging.attachErrorLogSink({ logError: error => { errors.push({ name: error.name, message: error.message }); rawErrors.push({ name: error.name, message: error.message, stack: error.stack }) }, logMCPError: (name, error) => rawErrors.push({ name, error: String(error) }), logMCPDebug: (name, message) => rawErrors.push({ name, message }), getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: name => join(home, 'mcp-' + name) })
const originalDebug = debug.logForDebugging
spyOn(debug, 'logForDebugging').mockImplementation((name, fields) => { diagnostics.push({ kind: 'debug', name, ...(fields === undefined ? {} : { fields }) }); originalDebug(name, fields) })
const storage = await import('../../../src/utils/sessionStorage'), plans = await import('../../../src/utils/plans'), fileHistory = await import('../../../src/utils/fileHistory'), hooks = await import('../../../src/utils/sessionStart'), attachments = await import('../../../src/utils/attachments')
const recovery = await import('../../../src/utils/conversationRecovery')
const settings = await import('../../../src/utils/settings/settings')
// A real CLI caller has loaded its initial settings before resume. Warm this
// genuine snapshot from original files; do not substitute processed settings.
settings.getInitialSettings()
// The private managed-path override is cached by the real resolver. Restore
// the external release's logging profile before the observed call.
process.env.USER_TYPE = 'external'
const startupDiagnostics = structuredClone(diagnostics)
diagnostics.length = 0
const phases: string[] = [], snapshotRecords: unknown[] = [], backgrounds: Promise<unknown>[] = []
let suppressed = false
const suppress = attachments.suppressNextSkillListing
spyOn(attachments, 'suppressNextSkillListing').mockImplementation(() => { suppressed = true; suppress() })
const copyPlan = plans.copyPlanForResume
spyOn(plans, 'copyPlanForResume').mockImplementation(async (...args) => { phases.push('plan:start'); const result = await copyPlan(...args); phases.push('plan:end'); return result })
const copyHistory = fileHistory.copyFileHistoryForResume
spyOn(fileHistory, 'copyFileHistoryForResume').mockImplementation((...args) => { phases.push('history:start'); const promise = copyHistory(...args); backgrounds.push(promise); return promise })
const start = hooks.processSessionStartHooks
spyOn(hooks, 'processSessionStartHooks').mockImplementation(async (...args) => { phases.push('hooks:start'); const result = await start(...args); phases.push('hooks:end'); return result })
const record = storage.recordFileHistorySnapshot
spyOn(storage, 'recordFileHistorySnapshot').mockImplementation(async (...args) => { snapshotRecords.push({ type: 'file-history-snapshot', messageId: args[0], snapshot: args[1], isSnapshotUpdate: args[2] }); return record(...args) })
let release: () => void = () => {}, held = new Promise<void>(resolve => { release = resolve })
const link = fsPromises.link
spyOn(fsPromises, 'link').mockImplementation(async (old, target) => { guard(String(old)); guard(String(target)); if (input.holdCopies) await held; return link(old, target) })
let probingUUIDs = false, transcriptReadsDuringProbes = 0
const probePath = input.uuidProbes ? join(home, '.claude', 'projects', home.replace(/[^a-zA-Z0-9]/g, '-'), JSON.parse(input.source!) + '.jsonl') : undefined
const readOriginal = fsPromises.readFile
spyOn(fsPromises, 'readFile').mockImplementation((...args) => { if (probingUUIDs && String(args[0]) === probePath) transcriptReadsDuringProbes++; return readOriginal(...args as Parameters<typeof readOriginal>) })
const liteReadFaults: Array<{ operation: string, ordinal: number, position: number }> = []
if (input.liteReadFault) {
  const originalOpen = fsPromises.open
  spyOn(fsPromises, 'open').mockImplementation(async (...args) => {
    guard(String(args[0]))
    const handle = await originalOpen(...args)
    if (String(args[0]) === input.liteReadFault!.path) {
      let ordinal = 0
      const originalRead = handle.read.bind(handle)
      spyOn(handle, 'read').mockImplementation(async (...readArgs: Parameters<typeof handle.read>) => {
        ordinal++
        if (ordinal === input.liteReadFault!.atRead) {
          liteReadFaults.push({ operation: 'read', ordinal, position: Number(readArgs[3]) })
          throw Object.assign(new Error('original-private-tail-read-fault'), { code: 'EIO' })
        }
        return originalRead(...readArgs)
      })
    }
    return handle
  })
}
let uuid = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => { const value = '00000000-0000-4000-8000-' + (++uuid).toString().padStart(12, '0'); allocations.push({ kind: 'uuid', value }); return value as ReturnType<typeof crypto.randomUUID> })
const OriginalDate = Date
if (input.request) await (await import('./resume-state-request-source')).initializeStateRequest()
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1_700_000_000_000]) as [number]); if (!args.length) allocations.push({ kind: 'time', value: this.toISOString() }) }
  static now() { allocations.push({ kind: 'now', value: 1_700_000_000_000 }); return 1_700_000_000_000 }
} as DateConstructor
let result: Awaited<ReturnType<typeof recovery.loadConversationForResume>> | undefined, failure: unknown
try { result = await recovery.loadConversationForResume(input.source === undefined ? undefined : JSON.parse(input.source), input.jsonlPath); phases.push('return') } catch (error) { failure = { name: (error as Error).name, message: (error as Error).message }; rawErrors.push({ ...failure as object, stack: (error as Error).stack }); phases.push('throw') }
const state = () => ({ skills: [...bootstrap.getInvokedSkills()].map(([key, value]) => ({ key, value })), planSlugs: [...bootstrap.getPlanSlugCache()].map(([key, value]) => ({ key, value })), suppressed, pendingEntries: structuredClone(snapshotRecords) })
const before = state()
release(); await Promise.allSettled(backgrounds); await storage.flushSessionStorage()
const after = state()
const uuidProbes: Array<{ uuid: string, exists: boolean }> = []
if (input.uuidProbes) {
  probingUUIDs = true
  for (const uuid of input.uuidProbes) uuidProbes.push({ uuid, exists: await storage.doesMessageExistInSession(JSON.parse(input.source!) as Parameters<typeof storage.doesMessageExistInSession>[0], uuid as Parameters<typeof storage.doesMessageExistInSession>[1]) })
  probingUUIDs = false
}
const effects: Record<string, string> = {}
async function readTree(directory: string) {
  guard(directory)
  try { for (const entry of await fsPromises.readdir(directory, { withFileTypes: true })) { const path = join(directory, entry.name); guard(path); if (entry.isDirectory()) await readTree(path); else if (entry.isFile()) effects[relative(home, path)] = (await fsPromises.readFile(path)).toString('hex') } } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
}
await readTree(join(home, '.claude', 'plans')); await readTree(join(home, 'private-plans')); await readTree(join(home, '.claude', 'file-history', input.currentSessionId))
if (capabilities.blocked.length) throw new Error('Resume state blocked capability: ' + capabilities.blocked.join(', '))
guard(join(home, 'resume-state-result.json'))
const observation = structuredClone({ resultSource: JSON.stringify(result) ?? null, before, after, phases, effects, allocations, diagnostics, errors, failure: failure ?? null, ...(input.uuidProbes ? { uuidProbes, transcriptReadsDuringProbes } : {}) })
let unprimedControlReads: number | undefined
if (input.uuidProbes) {
  storage.clearSessionMessagesCache()
  probingUUIDs = true
  await storage.doesMessageExistInSession(JSON.parse(input.source!) as Parameters<typeof storage.doesMessageExistInSession>[0], input.uuidProbes[0]! as Parameters<typeof storage.doesMessageExistInSession>[1])
  probingUUIDs = false
  unprimedControlReads = transcriptReadsDuringProbes
}
const request = input.request ? await (await import('./resume-state-request-source')).executeStateRequest(input.request, result, allocations) : undefined
if (capabilities.blocked.length) throw new Error('Resume state request blocked capability: ' + capabilities.blocked.join(', '))
await fsPromises.writeFile(join(home, 'resume-state-result.json'), JSON.stringify({ observation, ...(request ? { requestObservation: request.observation } : {}), sourceOnly: { capabilities, rawErrors, startupDiagnostics, ...(input.liteReadFault ? { liteReadFaults } : {}), ...(unprimedControlReads === undefined ? {} : { unprimedControlReads }), ...(request ? { request: request.sourceOnly } : {}) } }))
console.log(JSON.stringify({ result: 'resume-state-result.json' }))
