import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard.ts'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities.ts'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual explicit-flush error ownership')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_EXPLICIT_FLUSH_ERROR !== '1') throw new Error('Pinned private explicit-flush fixture required')
const absent = absentManagedSettingsFixturePaths()
for (let directory = home; ; directory = dirname(directory)) {
  const candidate = join(directory, '.git')
  if (fs.existsSync(candidate)) throw new Error('Private fixture inherits repository')
  absent.push(candidate)
  if (dirname(directory) === directory) break
}
let sourceSHA256 = ''
// Accessor only: full original Project bodies and Promise returns are retained.
// This transformed warm same-area boundary is not cold SDK/P0 authority.
Bun.plugin({ name: 'actual-explicit-flush-project-accessor', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => {
    const original = fs.readFileSync(args.path)
    sourceSHA256 = createHash('sha256').update(original).digest('hex')
    return { loader: 'ts', contents: original.toString('utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject }\n' }
  })
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-explicit-flush', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload.ts')
const bootstrap = await import('../../../src/bootstrap/state.ts')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000')
process.env.USER_TYPE = 'ant'
;(await import('../../../src/utils/settings/settings.ts')).getInitialSettings()
process.env.USER_TYPE = 'external'
;(await import('../../../src/utils/config.ts')).enableConfigs()
const storage = await import('../../../src/utils/sessionStorage.ts')
const { createUserMessage } = await import('../../../src/utils/messages.ts')
const project = (storage as any).fixtureGetProject()
const path = join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl')
const backup = path + '.explicit-flush-backup'
const scheduled = new Map<object, (...args: any[]) => any>()
const originalTimeout = globalThis.setTimeout
const originalClear = globalThis.clearTimeout
// Existing Schedule-test adapter: the original 100ms callback is captured and
// never invoked. No Project body, native FS result or Promise return is wrapped.
globalThis.setTimeout = ((callback: any, milliseconds?: number, ...args: any[]) => {
  if (milliseconds !== 100 || typeof callback !== 'function' || args.length) return (originalTimeout as any)(callback, milliseconds, ...args)
  const token = {}
  scheduled.set(token, callback)
  return token
}) as any
globalThis.clearTimeout = ((token: any) => {
  if (scheduled.delete(token)) return
  return (originalClear as any)(token)
}) as any
type Outcome = { status: 'fulfilled' | 'rejected', code: string | null }
let first: Promise<void> | undefined
let second: Promise<void> | undefined
let settled: PromiseSettledResult<void>[] | undefined
let renamed = false
let beforeDisk = ''
let sameFirstPromise = false
let sameSecondPromise = false
let distinctReturns = false
let activeAfterAdmissions = false
const registration: string[] = []
let restored = false
try {
  await storage.recordTranscript([{ ...createUserMessage({ content: 'original native first' }), uuid: 'native-first' }])
  const queued = [...project.writeQueues.values()].reduce((n: number, entries: any) => n + entries.length, 0) as number
  if (project.pendingWriteCount !== 0 || scheduled.size !== 1 || queued === 0 || project.activeDrain !== null || !project.transcriptFileIdentities.has(path)) throw new Error('Actual cached identity, queued batch, uninvoked timer and no active timer drain required')
  if (fs.existsSync(backup)) throw new Error('Refusing existing private backup')
  beforeDisk = fs.readFileSync(path, 'utf8')
  // Actual private filesystem rotation produces native EISDIR on the original
  // write-only append open. The cached original inode and queued batch remain.
  fs.renameSync(path, backup)
  renamed = true
  fs.mkdirSync(path, { mode: 0o700 })
  if (!fs.lstatSync(path).isDirectory()) throw new Error('Actual directory target required')
  // Both original calls run in one synchronous JS turn. F1 splices the batch
  // before its native open awaits; F2 starts its own empty drain. No native
  // completion callback can execute between these two admissions.
  first = project.flush()
  registration.push('first-original-explicit-flush')
  second = project.flush()
  registration.push('second-original-explicit-flush')
  sameFirstPromise = Promise.resolve(first) === first
  sameSecondPromise = Promise.resolve(second) === second
  distinctReturns = first !== second
  activeAfterAdmissions = project.activeDrain !== null
  // Attach immutable result observers before this turn yields, avoiding any
  // unhandled rejection and retaining each original Promise's own outcome.
  settled = await Promise.allSettled([first, second])
} finally {
  try {
    await Promise.allSettled([first, second].filter(Boolean))
    if (renamed) {
      if (fs.existsSync(path)) {
        if (!fs.lstatSync(path).isDirectory() || fs.readdirSync(path).length !== 0) throw new Error('Private target changed before restore')
        fs.rmSync(path, { recursive: true })
      }
      fs.renameSync(backup, path)
      renamed = false
      restored = fs.readFileSync(path, 'utf8') === beforeDisk
    }
    await project.flush()
  } finally {
    globalThis.setTimeout = originalTimeout
    globalThis.clearTimeout = originalClear
  }
}
const outcomes: Outcome[] = (settled ?? []).map(result => result.status === 'fulfilled'
  ? { status: 'fulfilled', code: null }
  : { status: 'rejected', code: typeof result.reason?.code === 'string' ? result.reason.code : null })
const expected: Outcome[] = [{ status: 'rejected', code: 'EISDIR' }, { status: 'fulfilled', code: null }]
const finalQueued = [...project.writeQueues.values()].reduce((n: number, entries: any) => n + entries.length, 0) as number
if (JSON.stringify(outcomes) !== JSON.stringify(expected) || !sameFirstPromise || !sameSecondPromise || !distinctReturns || activeAfterAdmissions || !restored || scheduled.size || project.pendingWriteCount || finalQueued || project.writeQueues.size || project.activeDrain !== null || fs.existsSync(backup) || caps.blocked.length || fs.readFileSync(path, 'utf8') !== beforeDisk) throw new Error('Original explicit-flush error ownership proof failed: ' + JSON.stringify({ outcomes, sameFirstPromise, sameSecondPromise, distinctReturns, activeAfterAdmissions, restored, scheduled: scheduled.size, blocked: caps.blocked }))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ outcomes, registration, sameFirstPromise, sameSecondPromise, distinctReturns, activeAfterAdmissions, restored, finalTracked: project.pendingWriteCount, finalQueued, finalQueuePaths: project.writeQueues.size, scheduled: scheduled.size, blocked: caps.blocked, physical: true, sourceSHA256, scope: 'transformed warm original Project explicit flush error ownership only, not cold/P0' }), () => resolve()))
process.exit(0)
