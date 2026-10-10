import { spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import * as crypto from 'node:crypto'
import { lstatSync, realpathSync } from 'node:fs'
import { promises as fs } from 'node:fs'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

type Step = { operation: 'reduce' | 'path' | 'append' | 'rewrite', sources?: string[], bytes?: string }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string, classifier: boolean, steps: Step[] }
const home = assertDisposableFixtureHome(input.home, 'Session resume')
if (process.env.CC_HAHA_SESSION_RESUME_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || typeof input.classifier !== 'boolean' || !Array.isArray(input.steps) || Object.keys(input).some(name => !['home', 'classifier', 'steps'].includes(name))) throw new Error('Session resume requires original explicit offline inputs')
if (input.classifier !== (feature('TRANSCRIPT_CLASSIFIER') ? true : false)) throw new Error('Session resume declared build profile mismatch')
const canonicalHome = realpathSync(home)
function guard(path: string) {
  const part = relative(home, path)
  if (part === '..' || part.startsWith('..' + sep) || isAbsolute(part)) throw new Error('Session resume requires ordinary private files')
  for (let probe = path;; probe = dirname(probe)) {
    try {
      const info = lstatSync(probe), part = relative(canonicalHome, realpathSync(probe))
      if (info.isSymbolicLink() || part === '..' || part.startsWith('..' + sep) || isAbsolute(part) || probe === path && !info.isFile()) throw new Error('Session resume requires ordinary private files')
      return
    } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT' || probe === dirname(probe)) throw error }
  }
}
const path = join(home, 'session.jsonl'), output = join(home, 'resume-result.json')
guard(path); guard(output)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home)
const analytics = await import('../../../src/services/analytics'), logging = await import('../../../src/utils/log'), debug = await import('../../../src/utils/debug')
const diagnostics: unknown[] = [], rawErrors: unknown[] = [], allocations: unknown[] = []
analytics.attachAnalyticsSink({ logEvent: (name, metadata) => { diagnostics.push({ kind: 'event', name, fields: metadata }) }, logEventAsync: async (name, metadata) => { diagnostics.push({ kind: 'event', name, fields: metadata }) } })
logging.attachErrorLogSink({ logError: error => { rawErrors.push({ name: error.name, message: error.message, stack: error.stack }) }, logMCPError: (serverName, error) => { rawErrors.push({ serverName, error: String(error) }) }, logMCPDebug: (serverName, message) => { rawErrors.push({ serverName, message }) }, getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: name => join(home, 'mcp-' + name) })
const originalDebug = debug.logForDebugging
spyOn(debug, 'logForDebugging').mockImplementation((name, fields) => { diagnostics.push({ kind: 'debug', name, fields }); originalDebug(name, fields) })
const recovery = await import('../../../src/utils/conversationRecovery')
let uuid = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => { const value = '00000000-0000-4000-8000-' + (++uuid).toString().padStart(12, '0'); allocations.push({ kind: 'uuid', value }); return value as ReturnType<typeof crypto.randomUUID> })
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1_700_000_000_000]) as [number]); if (!args.length) allocations.push({ kind: 'time', value: this.toISOString() }) }
  static now() { allocations.push({ kind: 'now', value: 1_700_000_000_000 }); return 1_700_000_000_000 }
} as DateConstructor
const observations: unknown[] = []
for (const step of input.steps) {
  uuid = 0; allocations.length = diagnostics.length = rawErrors.length = 0
  let history: Parameters<typeof recovery.deserializeMessagesWithInterruptDetection>[0] = [], result: unknown, failure: unknown
  try {
    if (step.operation === 'rewrite' || step.operation === 'append') {
      guard(path)
      await (step.operation === 'rewrite' ? fs.writeFile : fs.appendFile)(path, Buffer.from(step.bytes ?? '', 'hex'))
      result = { ok: true }
    } else if (step.operation === 'path') {
      guard(path)
      const loaded = await recovery.loadMessagesFromJsonlPath(path)
      history = loaded.messages
      result = { ...recovery.deserializeMessagesWithInterruptDetection(history), sessionId: loaded.sessionId }
    } else if (step.operation === 'reduce') {
      if (!Array.isArray(step.sources)) throw new Error('Resume sources required')
      history = step.sources.map(source => JSON.parse(source))
      result = recovery.deserializeMessagesWithInterruptDetection(history)
    } else throw new Error('Unknown session resume operation')
  } catch (error) { failure = { name: (error as Error).name, message: (error as Error).message }; rawErrors.push({ ...failure as object, stack: (error as Error).stack }) }
  const reduced = result as ReturnType<typeof recovery.deserializeMessagesWithInterruptDetection> | undefined
  observations.push({ resultSource: JSON.stringify(result) ?? null, historyAfterSource: JSON.stringify(history), references: reduced?.messages ? reduced.messages.map(message => history.indexOf(message)) : [], interruptionIndex: reduced?.turnInterruptionState?.kind === 'interrupted_prompt' ? reduced.messages.indexOf(reduced.turnInterruptionState.message) : null,
    permissionOwn: history.filter(message => message?.type === 'user').map(message => ({ uuid: message.uuid ?? null, own: Object.hasOwn(message, 'permissionMode'), value: message.permissionMode ?? null })), allocations: structuredClone(allocations), diagnostics: structuredClone(diagnostics), errors: rawErrors.filter((row: any) => row.stack).map((row: any) => ({ name: row.name, message: row.message })), failure: failure ?? null })
}
if (capabilities.blocked.length) throw new Error('Session resume blocked external capability: ' + capabilities.blocked.join(', '))
guard(output)
await fs.writeFile(output, JSON.stringify({ observations, sourceOnly: { capabilities, rawErrors, runtime: { platform: process.platform, arch: process.arch, bun: Bun.version } } }))
console.log(JSON.stringify({ result: 'resume-result.json' }))
