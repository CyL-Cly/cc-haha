import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard.ts'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities.ts'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual same-path Map single visit')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MAP_SINGLE_VISIT !== '1') throw new Error('Pinned private active-drain fixture required')
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
    return { loader: 'ts', contents: original.toString('utf8') + '\nexport { getProject as fixtureGetProject }\n' }
  })
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-map-single-visit', BUILD_TIME: '', PACKAGE_URL: '' }
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
const lateEntry = { type: 'custom-title', customTitle: 'map-single-visit-late', sessionId: bootstrap.getSessionId() }
const scheduled = new Map<object, (...args: any[]) => any>()
const originalTimeout = globalThis.setTimeout
const originalClear = globalThis.clearTimeout
// Identical100ms capture adapter to the already-qualified active-drain fixture.
// Only the first original callback is manually entered; the late timer never runs.
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
let drain: Promise<void> | undefined
let late: Promise<void> | undefined
let observation: any
let admission: any
let cleanupError: unknown
const registration: string[] = []
try {
  await storage.recordTranscript([{ ...createUserMessage({ content: 'original native first' }), uuid: 'native-first' }])
  const queue = project.writeQueues.get(path)
  const identity = project.transcriptFileIdentities.get(path)
  if (!queue || queue.length !== 1 || !identity || project.pendingWriteCount !== 0 || scheduled.size !== 1 || project.writeQueues.size !== 1) throw new Error('Real warmed first-only queue and identity required')
  const [token, callback] = [...scheduled.entries()][0]
  scheduled.delete(token)
  // One synchronous JS turn: original timer enters original drain, then cached
  // appendEntry pushes onto the same already-spliced queue before any reaction.
  timerReturn = Reflect.apply(callback, undefined, [])
  drain = project.activeDrain
  if (!(drain instanceof Promise) || Bun.peek.status(drain) !== 'pending' || project.writeQueues.get(path) !== queue || queue.length !== 0 || project.flushTimer !== null) throw new Error('Original firstD did not splice the existing Map entry')
  registration.push('original-timer-firstD-returned-pending')
  late = project.appendEntry(lateEntry)
  if (project.writeQueues.get(path) !== queue || queue.length !== 1 || project.transcriptFileIdentities.get(path) !== identity || project.writeQueues.size !== 1 || project.pendingWriteCount !== 1 || scheduled.size !== 1 || !project.flushTimer) throw new Error('Cached late append was not synchronous on the same Map entry')
  registration.push('same-turn-cached-late-append-enqueued')
  admission = { cachedIdentitySame: true, sameQueueObject: true, samePathOnly: true, firstDNativePending: true, queueBeforeLate: 0, queueAfterLate: 1, scheduledLateTimer: true }
  // Await the exact original D. No replacement, readiness gate, sleep or tick.
  await drain
  const disk = fs.readFileSync(path, 'utf8')
  observation = { firstPersisted: disk.includes('"uuid":"native-first"'), latePersisted: disk.includes('map-single-visit-late'), tracked: project.pendingWriteCount, queued: [...project.writeQueues.values()].reduce((n: number, entries: any) => n + entries.length, 0), pathPresent: project.writeQueues.has(path), active: project.activeDrain !== null, timerPending: project.flushTimer !== null }
  admission.sameQueueObjectAtConsumer = project.writeQueues.get(path) === queue
} finally {
  // Join every original promise and the real final flush even on an unexpected
  // proof failure; no final business assertion precedes physical completion.
  try { if (drain) await drain } catch (error) { cleanupError ??= error }
  try { if (timerReturn) await timerReturn } catch (error) { cleanupError ??= error }
  try { if (late) await late } catch (error) { cleanupError ??= error }
  try { await project.flush() } catch (error) { cleanupError ??= error }
  globalThis.setTimeout = originalTimeout
  globalThis.clearTimeout = originalClear
}
const finalDisk = fs.readFileSync(path, 'utf8')
const expected = { firstPersisted: true, latePersisted: false, tracked: 0, queued: 1, pathPresent: true, active: false, timerPending: true }
if (cleanupError || JSON.stringify(observation) !== JSON.stringify(expected) || !admission?.sameQueueObjectAtConsumer || scheduled.size || project.writeQueues.size || project.pendingWriteCount || !finalDisk.includes('map-single-visit-late') || caps.blocked.length) throw new Error('Invalid actual Source single-visit proof: ' + JSON.stringify({ observation, admission, scheduled: scheduled.size, blocked: caps.blocked, cleanupError: String(cleanupError ?? '') }))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observation, admission, registration, sourceSHA256, physical: true, finalPersisted: true, finalQueued: 0, finalTracked: 0, finalTimerCount: scheduled.size, blocked: caps.blocked, scope: 'transformed warm original Project original-timer firstD single-existing-Map-entry boundary only; no delete/reinsert profile; not public cold/P0' }), () => resolve()))
process.exit(0)
