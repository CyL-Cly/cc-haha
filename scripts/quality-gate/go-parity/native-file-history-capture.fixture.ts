import { join } from 'node:path'
import { mkdir, writeFile, readFile, readdir, stat, symlink, link, rename } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native file history capture')
if (process.env.CC_HAHA_FILE_HISTORY_CAPTURE_FIXTURE !== '1') throw new Error('Unowned file history fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
process.env.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = '1'
const state = await import('../../../src/bootstrap/state')
state.setIsInteractive(false)
const history = await import('../../../src/utils/fileHistory')
const storage = await import('../../../src/utils/sessionStorage')
const log = await import('../../../src/utils/log')
const errors: string[] = []
log.attachErrorLogSink({ logError: e => errors.push(e.message), logMCPError: () => {}, logMCPDebug: () => {}, getErrorsPath: () => join(home, 'errors'), getMCPLogsPath: () => join(home, 'mcp') })
const results = []
for (const [index, row] of input.cases.entries()) {
  const work = join(home, 'work-' + index)
  await mkdir(work, { recursive: true, mode: 0o700 })
  state.setOriginalCwd(work); state.setCwdState(work); state.regenerateSessionId()
  const sessionId = state.getSessionId()
  const path = join(work, 'input.txt')
  if (!row.missing) await writeFile(path, row.before, { mode: row.mode ?? 0o640 })
  // Keep the protected target inside the disposable home. The Source lstat
  // still sees and rejects a symlink, without creating an unresolvable cycle
  // that the independent filesystem guard correctly refuses to follow.
  if (row.symlink) { await rename(path, join(work, 'alias')); await symlink(join(work, 'alias'), path) }
  if (row.hardlink) await link(path, join(work, 'alias'))
  let current: any = { snapshots: [{ schemaVersion: 2, messageId: 'original-user', trackedFileBackups: {}, timestamp: new Date() }], trackedFiles: new Set(), snapshotSequence: 1 }
  const update = (fn: any) => { current = fn(current) }
  const offset = errors.length
  await history.fileHistoryTrackEdit(update, path, 'parent-assistant' as any)
  const before = current.snapshots[0].trackedFileBackups['input.txt']
  if (row.after !== undefined && !row.symlink && !row.hardlink) {
    await writeFile(path, row.after)
    await history.fileHistoryCompleteSnapshot(update, 'original-user' as any)
  }
  await storage.flushSessionStorage()
  const after = current.snapshots[0].completedFileBackups?.['input.txt']
  const disk: any[] = []
  try {
    const dir = join(home, '.claude', 'file-history', sessionId)
    for (const name of await readdir(dir)) { const p = join(dir, name); disk.push({ name, bytes: (await readFile(p)).toString('hex'), mode: (await stat(p)).mode & 0o777 }) }
  } catch (e) { if ((e as any).code !== 'ENOENT') throw e }
  results.push({ sessionId, path, work, before, after, disk, errors: errors.slice(offset) })
}
if (capabilities.blocked.length) throw new Error('File history accessed blocked capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, capabilities }))
