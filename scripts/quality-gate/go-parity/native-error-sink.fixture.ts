import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { readdir, readFile, readlink, lstat, rm, mkdir } from 'fs/promises'
import { join } from 'path'
assertDisposableFixtureHome(process.env.HOME, 'Native error sink')
if (process.env.CC_HAHA_ERROR_LOG_FIXTURE !== '1') throw new Error('Unowned sink fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
await import('../../../preload')
const fixed = 1700000000000
const OriginalDate = Date
globalThis.Date = class extends OriginalDate { constructor(...args: any[]) { super(...(args.length ? args : [fixed]) as [any]) } static now() { return fixed } } as DateConstructor
for (const name of ['CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI', 'DISABLE_ERROR_REPORTING', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC', 'DISABLE_TELEMETRY', 'NODE_ENV', 'USER_TYPE', 'DEBUG', 'DEBUG_SDK']) delete process.env[name]
Object.assign(process.env, input.environment)
process.argv = ['bun', 'private-error-fixture', ...(input.arguments ?? [])]
const deferred: Array<() => void> = []
globalThis.setImmediate = (callback: () => void) => { deferred.push(callback); return {} as any }
const promises = await import('fs/promises')
const actualUnlink = promises.unlink, actualSymlink = promises.symlink
let latestStarted = false
let latestSettled!: () => void
const latestCompletion = new Promise<void>(resolve => { latestSettled = resolve })
mock.module('fs/promises', () => ({ ...promises,
  unlink: (path: any) => { if (String(path).endsWith('/latest')) latestStarted = true; return actualUnlink(path) },
  symlink: async (...args: any[]) => { try { return await actualSymlink(...args as [any, any]) } finally { if (String(args[1]).endsWith('/latest')) latestSettled() } },
}))
const state = await import('../../../src/bootstrap/state')
mock.module('../../../src/bootstrap/state', () => ({ ...state, getSessionId: () => 'private-error-session' }))
const fs = await import('../../../src/utils/fsOperations')
const actualFs = fs.getFsImplementation()
mock.module('../../../src/utils/fsOperations', () => ({ ...fs, getFsImplementation: () => ({ ...actualFs, cwd: () => input.cwd }) }))
let stderr = ''
const processUtils = await import('../../../src/utils/process')
mock.module('../../../src/utils/process', () => ({ ...processUtils, writeToStderr: (text: string) => { stderr += text } }))
const log = await import('../../../src/utils/log')
const debug = await import('../../../src/utils/debug')
const sink = await import('../../../src/utils/errorLogSink')
debug.setHasFormattedOutput(Boolean(input.formatted))
sink.initializeErrorLogSink()
if (input.failAfterInit) { const path = join(process.env.CLAUDE_CONFIG_DIR!, 'debug', 'private-error-session.txt'); await rm(path); await mkdir(path) }
const snapshot = async (): Promise<Record<string, string>> => {
  const result: Record<string, string> = {}
  const walk = async (dir: string) => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) await walk(path)
      else if ((await lstat(path)).isSymbolicLink()) result[path] = 'link:' + await readlink(path)
      else result[path] = await readFile(path, 'utf8')
    }
  }
  await walk(process.env.CLAUDE_CONFIG_DIR!)
  return result
}
for (let i = 0; i < input.count; i++) {
  const error = new Error(`${input.message}#${i}`)
  error.name = input.name ?? 'Error'
  error.stack = input.stack ? `${input.stack}#${i}` : ''
  log.logError(error)
}
if (latestStarted) await latestCompletion
const before = await snapshot()
sink._flushLogWritersForTesting()
await debug.flushDebugLogs()
for (const callback of deferred) callback()
await debug.flushDebugLogs()
// Observe the original async symlink's physical completion, without a sleep or output normalization.
if (latestStarted) await latestCompletion
const after = await snapshot()
sink._clearLogWritersForTesting()
process.stdout.write(JSON.stringify({ before, after, stderr, memory: log.getInMemoryErrors(), version: MACRO.VERSION }))
