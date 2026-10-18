import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original materialize async-return observer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MATERIALIZE_RETURN !== '1') throw new Error('Pinned owned materialize fixture required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  const candidate = join(path, '.git')
  if (!fs.existsSync(candidate)) absent.push(candidate)
  if (dirname(path) === path) break
}
Bun.plugin({ name: 'original-materialize-method-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject }\n' }))
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-materialize-return', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000020')
process.env.USER_TYPE = 'ant'
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const { createUserMessage } = await import('../../../src/utils/messages')
const project = (storage as any).fixtureGetProject()
const originalPrepare = project.prepareTranscriptFile, originalMaterialize = project.materializeSessionFile
let prepareCalls = 0, materialize: Promise<void>
const events: Array<{ phase: number, prepareCalls: number, materialize: string }> = []
let observed: () => void
const observation = new Promise<void>(resolve => { observed = resolve })
const probe = (index: number) => {
  events.push({ phase: index, prepareCalls, materialize: Bun.peek.status(materialize) })
  if (index < 3) queueMicrotask(() => probe(index + 1))
  else observed()
}
project.prepareTranscriptFile = function (...args: any[]) {
  prepareCalls++
  const promise = originalPrepare.apply(this, args)
  // Observe the original first prepare Promise before its materialize caller
  // registers await. No business wait or replacement filesystem is introduced.
  if (prepareCalls === 1) void promise.then(() => probe(0))
  return promise
}
project.materializeSessionFile = function (...args: any[]) {
  materialize = originalMaterialize.apply(this, args)
  return materialize
}
const message = createUserMessage({ content: 'actual materialize async-return prompt' })
if (input.buffered) project.pendingEntries.push({ type: 'queue-operation', uuid: 'actual-buffered-materialize', operation: 'enqueue', content: 'real buffered entry', sessionId: bootstrap.getSessionId() })
const recordUUID = await storage.recordTranscript([message])
await observation
await storage.flushSessionStorage()
project.prepareTranscriptFile = originalPrepare
project.materializeSessionFile = originalMaterialize
const text = fs.readFileSync(join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl'), 'utf8')
if (!text.includes('actual materialize async-return prompt') || recordUUID !== message.uuid || caps.blocked.length) throw new Error('Original materialize did not persist privately')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ events, prepareCalls, recordUUID, persisted: true, blocked: caps.blocked }), () => resolve()))
process.exit(0)
