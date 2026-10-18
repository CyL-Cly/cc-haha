import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard.ts'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities.ts'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual initial multi-target drain')
const profile = input.profile as string
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_INITIAL_MULTI_TARGET !== '1' || !['initial', 'delete-reinsert', 'error-interrupt', 'independent-drains'].includes(profile)) throw new Error('Pinned private initial multi-target profile required')
const absent = absentManagedSettingsFixturePaths()
for (let directory = home; ; directory = dirname(directory)) {
  const candidate = join(directory, '.git')
  if (fs.existsSync(candidate)) throw new Error('Private fixture inherits repository')
  absent.push(candidate)
  if (dirname(directory) === directory) break
}
let sourceSHA256 = ''
// Exactly the existing same-path fixture's accessor: every original Project
// method and returned Promise remains unchanged. This is a warm same-area test.
Bun.plugin({ name: 'actual-multi-target-project-accessor', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => {
    const original = fs.readFileSync(args.path)
    sourceSHA256 = createHash('sha256').update(original).digest('hex')
    return { loader: 'ts', contents: original.toString('utf8') + '\nexport { getProject as fixtureGetProject }\n' }
  })
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-initial-multi-target', BUILD_TIME: '', PACKAGE_URL: '' }
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
const project = (storage as any).fixtureGetProject()
const targets = ['A', 'B', 'C'] as const
const paths = Object.fromEntries(targets.map(target => [target, join(home, 'targets', target + '.jsonl')])) as Record<string, string>
const entry = (label: string) => ({ type: 'custom-title', customTitle: label, sessionId: bootstrap.getSessionId() })
const scheduled = new Map<object, (...args: any[]) => any>()
const originalTimeout = globalThis.setTimeout, originalClear = globalThis.clearTimeout
// Existing 100ms capture adapter; no captured callback is triggered here.
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
type Outcome = { status: string, code: string }
const jobs: Array<{ original: Promise<void>, outcome: Promise<Outcome> }> = []
const rawErrors: string[] = []
const startFlush = () => {
  const original = project.flush() as Promise<void>
  // Attach from creation; an actual expected EISDIR is never unhandled.
  const outcome = original.then(() => ({ status: 'fulfilled', code: '' }), error => {
    rawErrors.push(String(error))
    return { status: 'rejected', code: String(error?.code ?? 'UNKNOWN') }
  })
  jobs.push({ original, outcome })
  return original
}
const labels = (text: string) => text.split('\n').filter(Boolean).map(line => JSON.parse(line).customTitle as string)
const queueSnapshot = () => [...project.writeQueues].map(([path, queue]: [string, any[]]) => ({ target: targets.find(target => paths[target] === path), labels: queue.map(item => item.entry.customTitle) }))
const diskSnapshot = () => Object.fromEntries(targets.map(target => [target, fs.statSync(paths[target]).isFile() ? labels(fs.readFileSync(paths[target], 'utf8')) : []]))
const kinds = () => Object.fromEntries(targets.map(target => [target, fs.statSync(paths[target]).isFile() ? 'file' : 'directory']))
const appendBodies: string[] = []
let appendPrototype: any, appendDescriptor: PropertyDescriptor | undefined
let saved: string | undefined, damaged: string | undefined
let swapped = false
let observation: any, admission: any, cleanupError: unknown
let late: Promise<void> | undefined
try {
  for (const target of targets) {
    await project.prepareTranscriptFile(paths[target])
    await project.enqueueWrite(paths[target], entry(target))
  }
  if (project.pendingWriteCount !== 0 || project.writeQueues.size !== 3 || scheduled.size !== 1) throw new Error('Original warm three-target queue required')
  const probe = await fs.promises.open(paths.A, 'a')
  appendPrototype = Object.getPrototypeOf(probe)
  appendDescriptor = Object.getOwnPropertyDescriptor(appendPrototype, 'appendFile')
  await probe.close()
  if (!appendDescriptor || typeof appendDescriptor.value !== 'function') throw new Error('Real FileHandle appendFile prototype required')
  const originalAppend = appendDescriptor.value
  Object.defineProperty(appendPrototype, 'appendFile', { ...appendDescriptor, value: function (this: any, ...args: any[]) {
    if (typeof args[0] !== 'string') throw new Error('Original Project must pass immutable string content')
    appendBodies.push(args[0])
    // Exact native method Promise, no async wrapper, await, then, delay or gate.
    return Reflect.apply(originalAppend, this, args)
  } })
  if (profile === 'error-interrupt' || profile === 'independent-drains') {
    damaged = profile === 'error-interrupt' ? paths.B : paths.A
    saved = damaged + '.saved'
    fs.renameSync(damaged, saved)
    swapped = true
    fs.mkdirSync(damaged, { mode: 0o700 })
  }
  // One legal synchronous JS turn. F1 has entered the original native open
  // before delete/reinsert or F2. No native readiness is manufactured.
  const initialTargets = project.writeQueues.size
  const first = startFlush()
  if (profile === 'delete-reinsert') {
    if (!project.writeQueues.delete(paths.B)) throw new Error('Unvisited original B Map entry required')
    late = project.enqueueWrite(paths.B, entry('B2'))
  }
  if (profile === 'independent-drains') startFlush()
  admission = { initialTargets, pending: jobs.map(job => Bun.peek.status(job.original) === 'pending'), distinct: jobs.length === 1 || jobs[0].original !== jobs[1].original, queues: queueSnapshot(), timerPending: project.flushTimer !== null, active: project.activeDrain !== null }
  if (Bun.peek.status(first) !== 'pending') throw new Error('Original first flush must await real native IO')
  const outcomes = await Promise.all(jobs.map(job => job.outcome))
  if (late) await late
  observation = { outcomes, writes: appendBodies.flatMap(labels), files: diskSnapshot(), kinds: kinds(), queues: queueSnapshot(), tracked: project.pendingWriteCount, timerPending: project.flushTimer !== null, active: project.activeDrain !== null }
} finally {
  try { await Promise.all(jobs.map(job => job.outcome)); if (late) await late } catch (error) { cleanupError ??= error }
  try { if (swapped && damaged && saved) { fs.rmSync(damaged, { recursive: true }); fs.renameSync(saved, damaged) } } catch (error) { cleanupError ??= error }
  try { await project.flush() } catch (error) { cleanupError ??= error }
  if (appendPrototype && appendDescriptor) Object.defineProperty(appendPrototype, 'appendFile', appendDescriptor)
  globalThis.setTimeout = originalTimeout
  globalThis.clearTimeout = originalClear
}
const final = { writes: appendBodies.flatMap(labels), files: diskSnapshot(), kinds: kinds(), queues: queueSnapshot(), tracked: project.pendingWriteCount, timerPending: project.flushTimer !== null, active: project.activeDrain !== null }
if (cleanupError || scheduled.size || project.writeQueues.size || project.pendingWriteCount || caps.blocked.length) throw new Error('Actual multi-target physical cleanup failed: ' + JSON.stringify({ cleanupError: String(cleanupError ?? ''), final, scheduled: scheduled.size, blocked: caps.blocked }))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ profile, sourceSHA256, admission, observation, final, rawErrors, blocked: caps.blocked, physical: true, scope: 'warm original Project multi-target Map and explicit flush boundary; direct Map delete is controlled input; native append observer returns exact original Promise; not cold P0 or live acceptance' }), () => resolve()))
process.exit(0)
