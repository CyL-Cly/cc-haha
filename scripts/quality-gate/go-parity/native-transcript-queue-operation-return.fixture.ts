import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Project buffered queue operation return')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_QUEUE_OPERATION_RETURN !== '1') throw new Error('Pinned owned queue operation fixture required')
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
const queueOp = { type: 'queue-operation', operation: 'enqueue', timestamp: '2026-10-14T00:00:00.000Z', sessionId: bootstrap.getSessionId(), content: 'actual buffered queue operation' }
const events: Array<{ phase: number, tracked: number, buffered: number, recording: string }> = []
const recording = storage.recordQueueOperation(queueOp as any)
let observed: () => void
const complete = new Promise<void>(resolve => { observed = resolve })
const probe = (phase: number) => {
  events.push({ phase, tracked: project.pendingWriteCount, buffered: project.pendingEntries.length, recording: Bun.peek.status(recording) })
  if (phase < 4) queueMicrotask(() => probe(phase + 1))
  else observed()
}
queueMicrotask(() => probe(0))
await complete
await recording
await storage.recordTranscript([createUserMessage({ content: 'actual queue materialization message' })])
await storage.flushSessionStorage()
const text = fs.readFileSync(join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl'), 'utf8')
if (!text.includes('actual buffered queue operation') || !text.includes('actual queue materialization message')) throw new Error('Original buffered queue did not persist')
if (caps.blocked.length) throw new Error('Original queue escaped fixture: ' + caps.blocked.join(', '))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ events, persisted: true, blocked: caps.blocked }), () => resolve()))
process.exit(0)
