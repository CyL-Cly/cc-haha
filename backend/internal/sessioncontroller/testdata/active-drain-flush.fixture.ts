import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard.ts'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities.ts'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual active-drain independent flush')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_ACTIVE_DRAIN_FLUSH !== '1') throw new Error('Pinned private active-drain fixture required')
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
Bun.plugin({ name: 'actual-active-drain-project-accessor', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => {
    const original = fs.readFileSync(args.path)
    sourceSHA256 = createHash('sha256').update(original).digest('hex')
    return { loader: 'ts', contents: original.toString('utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject }\n' }
  })
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-active-drain', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../../preload.ts')
const bootstrap = await import('../../../../src/bootstrap/state.ts')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000')
process.env.USER_TYPE = 'ant'
;(await import('../../../../src/utils/settings/settings.ts')).getInitialSettings()
process.env.USER_TYPE = 'external'
;(await import('../../../../src/utils/config.ts')).enableConfigs()
const storage = await import('../../../../src/utils/sessionStorage.ts')
const { createUserMessage } = await import('../../../../src/utils/messages.ts')
const project = (storage as any).fixtureGetProject()
const path = join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl')
const lateEntry = { type: 'custom-title', customTitle: 'active-drain-late-producer', sessionId: bootstrap.getSessionId() }
const scheduled = new Map<object, (...args: any[]) => any>()
const originalTimeout = globalThis.setTimeout
const originalClear = globalThis.clearTimeout
// Existing Schedule-test adapter: capture only original 100ms callbacks. No
// timer callback body, native Promise, FS result, or Project method is wrapped.
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
let timerReturn: Promise<void> | undefined
let flushed: Promise<void> | undefined
let producer: Promise<void> | undefined
let late: Promise<void> | undefined
let observation: { firstPersisted: boolean, latePersisted: boolean, tracked: number, queued: number } | undefined
const registration: string[] = []
let sameActivePromise = false
let sameFlushPromise = false
try {
  await storage.recordTranscript([{ ...createUserMessage({ content: 'original native first' }), uuid: 'native-first' }])
  if (project.pendingWriteCount !== 0 || scheduled.size !== 1 || !project.transcriptFileIdentities.has(path)) throw new Error('Actual warmed identity and one scheduled first batch required')
  const [token, originalCallback] = [...scheduled.entries()][0]
  scheduled.delete(token)
  // All three admissions occur in this same synchronous JS turn. Native FS
  // completes later on its JS task; no readiness gate or extra job is inserted.
  timerReturn = Reflect.apply(originalCallback, undefined, [])
  const active: Promise<void> = project.activeDrain
  if (!active || Bun.peek.status(active) !== 'pending') throw new Error('Actual timer must own pending real native drain')
  sameActivePromise = Promise.resolve(active) === active
  registration.push('actual-timer-native-drain-started')
  flushed = project.flush()
  sameFlushPromise = Promise.resolve(flushed) === flushed
  registration.push('original-flush-registered-on-active')
  producer = active.then(() => {
    // Legitimate later producer, registered after flush on the same original D.
    // Metadata append takes actual cached-identity enqueue/trackWrite path.
    late = project.appendEntry(lateEntry)
  })
  registration.push('late-producer-registered-on-same-active')
  const consumed = flushed.then(() => {
    const disk = fs.readFileSync(path, 'utf8')
    observation = { firstPersisted: disk.includes('"uuid":"native-first"'), latePersisted: disk.includes('active-drain-late-producer'), tracked: project.pendingWriteCount, queued: [...project.writeQueues.values()].reduce((n: number, entries: any) => n + entries.length, 0) as number }
  })
  await consumed
} finally {
  // All work is physically joined before restoring adapters or exiting.
  await Promise.allSettled([timerReturn, flushed, producer].filter(Boolean))
  if (late) await late
  await project.flush()
  globalThis.setTimeout = originalTimeout
  globalThis.clearTimeout = originalClear
}
const finalDisk = fs.readFileSync(path, 'utf8')
if (!observation || !observation.firstPersisted || !observation.latePersisted || observation.tracked !== 0 || observation.queued !== 0 || !sameActivePromise || !sameFlushPromise || scheduled.size || !finalDisk.includes('active-drain-late-producer') || caps.blocked.length) throw new Error('Original active-drain flush boundary did not persist its later producer: ' + JSON.stringify({ observation, scheduled: scheduled.size, blocked: caps.blocked }))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observation, registration, sameActivePromise, sameFlushPromise, finalPersisted: true, blocked: caps.blocked, physical: true, sourceSHA256, scope: 'transformed warm original Project active-drain boundary only, not cold/P0' }), () => resolve()))
process.exit(0)
