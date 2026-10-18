import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { join } from 'node:path'
import { mkdir, writeFile, readFile, readdir, stat, chmod, unlink, link, utimes } from 'node:fs/promises'
import { spyOn } from 'bun:test'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native file history state')
if (process.env.CC_HAHA_FILE_HISTORY_STATE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned file history state fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
process.env.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = '1'
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setIsInteractive(false)
const storage = await import('../../../src/utils/sessionStorage')
const log = await import('../../../src/utils/log')
const records: any[] = [], errors: string[] = []
let recordFailure = false
// Only the external persistence sink is intercepted. All state transitions,
// safe backup IO, stat/content reuse and callbacks are actual Source functions.
spyOn(storage, 'recordFileHistorySnapshot').mockImplementation(async (id: any, snapshot: any, update: boolean) => {
 records.push({ id, snapshot: JSON.parse(JSON.stringify(snapshot)), update })
 if (recordFailure) throw new Error('fixture record failed')
})
log.attachErrorLogSink({ logError: e => errors.push(e.message), logMCPError: () => {}, logMCPDebug: () => {}, getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: () => join(home, 'mcp') })
const history = await import('../../../src/utils/fileHistory')
const fsPromises = await import('node:fs/promises')
const { createSubagentContext } = await import('../../../src/utils/forkedAgent')
const OriginalDate = Date
let now = 1700000000000
class FixtureDate extends OriginalDate {
 constructor(...args: any[]) { if (args.length) super(...args as [any]); else super(now) }
 static now() { return now }
}
globalThis.Date = FixtureDate as any
const clone = (value: any) => JSON.parse(JSON.stringify(value))
const serialize = (value: any) => ({ ...value, trackedFiles: Array.from(value.trackedFiles) })
const results: any[] = []
for (const [i, row] of input.cases.entries()) {
 const work = join(home, 'work-' + i), config = join(home, '.claude')
 await mkdir(work, { recursive: true })
 bootstrap.setOriginalCwd(work); bootstrap.setCwdState(work); bootstrap.regenerateSessionId()
 const sessionId = bootstrap.getSessionId()
 let current: any = { snapshots: [], trackedFiles: new Set(), snapshotSequence: 0, ...clone(row.initial ?? {}) }
 current.trackedFiles = new Set(row.initial?.trackedFiles ?? [])
 const update = (fn: any) => { current = fn(current) }
 const steps: any[] = []
 const paths = (name: string) => join(work, name)
 for (const action of row.actions) {
  now = action.now ?? 1700000000000
  const recordOffset = records.length, errorOffset = errors.length
  let thrown: any
  try {
   if (action.op === 'write') { await mkdir(join(paths(action.path), '..'), { recursive: true }); await writeFile(paths(action.path), Buffer.from(action.hex, 'hex'), { mode: action.mode ?? 0o640 }) }
   else if (action.op === 'chmod') await chmod(paths(action.path), action.mode)
   else if (action.op === 'mtime') await utimes(paths(action.path), action.time / 1000, action.time / 1000)
   else if (action.op === 'delete') await unlink(paths(action.path))
   else if (action.op === 'hardlink') await link(paths(action.path), paths(action.alias))
   else if (action.op === 'unlink-alias') await unlink(paths(action.alias))
   else if (action.op === 'record-failure') recordFailure = action.enabled
   else if (action.op === 'enabled') { if (action.enabled) delete process.env.CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING; else process.env.CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING = '1' }
   else if (action.op === 'make') await history.fileHistoryMakeSnapshot(update, action.id)
   else if (action.op === 'many-makes') for (let n = 0; n < action.count; n++) await history.fileHistoryMakeSnapshot(update, 'bulk-' + n)
   else if (action.op === 'track') await history.fileHistoryTrackEdit(update, action.absolute === false ? action.path : paths(action.path), action.id)
   else if (action.op === 'complete') await history.fileHistoryCompleteSnapshot(update, action.id)
   else if (action.op === 'race-make-track') {
    let release: () => void = () => {}, started: () => void = () => {}
    const gate = new Promise<void>(resolve => { release = resolve })
    const observed = new Promise<void>(resolve => { started = resolve })
    // The capability guard already installed a Bun spy. Capture its concrete
    // implementation, not the mutable mock function that spyOn will replace.
    const originalStat = (fsPromises.lstat as any).getMockImplementation?.()
    if (typeof originalStat !== 'function') throw new Error('Guarded lstat implementation missing')
    const spy = spyOn(fsPromises, 'lstat').mockImplementation(async (path: any, ...args: any[]) => {
     if (path === paths(action.slowPath)) { started(); await gate }
     return (originalStat as any)(path, ...args)
    })
    try {
     const making = history.fileHistoryMakeSnapshot(update, action.id)
     await observed
     await history.fileHistoryTrackEdit(update, paths(action.newPath), action.parentId)
     release()
     await making
    } finally { release(); spy.mockImplementation(originalStat) }
   }
   else if (action.op === 'agent-noop') {
    const parent: any = { abortController: new AbortController(), readFileState: (await import('../../../src/utils/fileStateCache')).createFileStateCacheWithSizeLimit(100), getAppState: () => ({ fileHistory: current, toolPermissionContext: {} }), setAppState: () => {}, updateFileHistoryState: update }
    const child = createSubagentContext(parent, { shareSetAppState: action.foreground, getAppState: parent.getAppState })
    await history.fileHistoryMakeSnapshot(child.updateFileHistoryState, action.id)
    await history.fileHistoryTrackEdit(child.updateFileHistoryState, paths(action.path), action.id)
    await history.fileHistoryCompleteSnapshot(child.updateFileHistoryState, action.id)
   }
   else throw new Error('Unknown fixture action ' + action.op)
  } catch (error: any) { thrown = { name: error.name, message: error.message } }
  await Promise.resolve(); await Promise.resolve()
  const disk: any[] = []
  try { for (const name of (await readdir(join(config, 'file-history', sessionId))).sort()) { const path = join(config, 'file-history', sessionId, name); const info = await stat(path); disk.push({ name, hex: (await readFile(path)).toString('hex'), mode: info.mode & 0o777 }) } }
  catch (error: any) { if (error.code !== 'ENOENT') throw error }
  steps.push({ action, state: clone(serialize(current)), records: records.slice(recordOffset), errors: errors.slice(errorOffset), thrown, disk })
 }
 results.push({ work, sessionId, steps })
 delete process.env.CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING
 recordFailure = false
}
globalThis.Date = OriginalDate
if (capabilities.blocked.length) throw new Error('Blocked state fixture capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, capabilities: { blocked: capabilities.blocked } }) + '\n')
