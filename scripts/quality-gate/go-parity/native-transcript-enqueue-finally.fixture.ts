import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Project enqueue finally await')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_ENQUEUE_FINALLY !== '1') throw new Error('Pinned owned enqueue fixture required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
// Observer accessors only: execute the unchanged full Project, memoized Set,
// enqueueWrite, trackWrite/finally, appendEntry and actual private filesystem.
Bun.plugin({ name: 'actual-project-enqueue-finally-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject, getSessionMessages as fixtureGetSessionMessages }\n' }))
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-enqueue-finally', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000019')
process.env.USER_TYPE = 'ant'
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const { createUserMessage } = await import('../../../src/utils/messages')
const project = (storage as any).fixtureGetProject()
await storage.recordTranscript([createUserMessage({ content: 'actual first local message' })])
await storage.flushSessionStorage()
const set = await (storage as any).fixtureGetSessionMessages(bootstrap.getSessionId())
const events: Array<{ kind: string, phase: string, tracked: number, added: boolean, status: string }> = []
for (const kind of ['user', 'queue-operation', 'last-prompt']) {
  const message = kind === 'user' ? createUserMessage({ content: 'actual enqueue user' }) : { type: kind, uuid: `actual-${kind}`, operation: 'enqueue', content: 'actual enqueue metadata', sessionId: bootstrap.getSessionId() }
  let append: Promise<void>
  // Register an observer on the same original pending-zero Promise used by
  // flush. decrementPendingWrites resolves it in trackWrite's finally, before
  // appendEntry's await reaction. This adds no business wait or fake I/O.
  const zero = new Promise<void>(resolve => project.flushResolvers.push(resolve))
  const observation = zero.then(() => {
    events.push({ kind, phase: 'tracked-zero-reaction', tracked: project.pendingWriteCount, added: set.has(message.uuid), status: Bun.peek.status(append) })
  })
  append = project.appendEntry(message)
  await append
  await observation
  events.push({ kind, phase: 'append-return', tracked: project.pendingWriteCount, added: set.has(message.uuid), status: Bun.peek.status(append) })
  await storage.flushSessionStorage()
}
const recordEvents: Array<{ phase: string, tracked: number, memo: string, record: string, insertion: string, track: string }> = []
let recording: Promise<unknown>, insertion: Promise<unknown>, track: Promise<unknown>
const originalInsert = project.insertMessageChain, originalTrack = project.trackWrite
project.trackWrite = function (...args: any[]) {
  const outer = this.pendingWriteCount === 0
  const result = originalTrack.apply(this, args)
  if (outer) track = result
  return result
}
project.insertMessageChain = function (...args: any[]) {
  insertion = originalInsert.apply(this, args)
  return insertion
}
const recordMessage = createUserMessage({ content: 'actual record finally prompt' })
let finishObservation: () => void
const observedRecord = new Promise<void>(resolve => { finishObservation = resolve })
const probe = (index: number) => {
  recordEvents.push({ phase: index === 0 ? 'tracked-zero-reaction' : `following-reaction-${index}`, tracked: project.pendingWriteCount, memo: project.currentSessionLastPrompt, record: Bun.peek.status(recording), insertion: Bun.peek.status(insertion), track: Bun.peek.status(track) })
  if (index < 2) queueMicrotask(() => probe(index + 1))
  else finishObservation()
}
const recordZero = new Promise<void>(resolve => project.flushResolvers.push(resolve))
void recordZero.then(() => probe(0))
recording = storage.recordTranscript([recordMessage])
const recordUUID = await recording
await observedRecord
project.insertMessageChain = originalInsert
project.trackWrite = originalTrack
await storage.flushSessionStorage()
const text = fs.readFileSync(join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl'), 'utf8')
if (!text.includes('actual enqueue user') || !text.includes('actual-queue-operation') || !text.includes('actual-last-prompt') || !text.includes('actual record finally prompt') || recordUUID !== recordMessage.uuid) throw new Error('Original enqueue did not persist actual entries')
if (caps.blocked.length) throw new Error('Original enqueue escaped fixture: ' + caps.blocked.join(', '))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ events, recordEvents, recordUUID, persisted: true, blocked: caps.blocked }), () => resolve()))
process.exit(0)
