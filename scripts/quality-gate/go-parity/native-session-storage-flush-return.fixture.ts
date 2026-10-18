import fs from 'node:fs'
import * as promises from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual session storage flush return')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SESSION_STORAGE_FLUSH_RETURN !== '1') throw new Error('Pinned owned flush return fixture required')
const absent = absentManagedSettingsFixturePaths()
for (let directory = home; ; directory = dirname(directory)) {
  const candidate = join(directory, '.git')
  if (fs.existsSync(candidate)) throw new Error('Disposable flush fixture inherits a repository')
  absent.push(candidate)
  if (dirname(directory) === directory) break
}
// This accessor changes the transformed module graph. It proves only the warm
// original Project/export return graph, never cold import or public CLI timing.
Bun.plugin({ name: 'actual-flush-return-project-accessor', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject }\n' }))
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-flush-return', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000029')
process.env.USER_TYPE = 'ant'
;(await import('../../../src/utils/settings/settings')).getInitialSettings()
process.env.USER_TYPE = 'external'
;(await import('../../../src/utils/config')).enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const { createUserMessage } = await import('../../../src/utils/messages')
const project = (storage as any).fixtureGetProject()
await storage.recordTranscript([createUserMessage({ content: 'actual flush return setup' })])
await storage.flushSessionStorage()

let enter!: () => void, release!: () => void, finishObservation!: () => void
const entered = new Promise<void>(resolve => { enter = resolve })
const released = new Promise<void>(resolve => { release = resolve })
const observed = new Promise<void>(resolve => { finishObservation = resolve })
// Capabilities already installed a Bun spy on this live export. Preserve its
// guarded implementation, not the mutable spy object changed below.
const originalOpen = (promises.open as any).getMockImplementation?.() as typeof promises.open
if (typeof originalOpen !== 'function') throw new Error('Actual guarded open implementation required')
// Preserve the original open Promise. Only hold completion after the real
// private native append has already succeeded; no synthetic filesystem result.
const openSpy = spyOn(promises, 'open').mockImplementation(((...args: any[]) => {
  const opened = (originalOpen as any)(...args)
  void opened.then((file: any) => {
    const append = file.appendFile.bind(file)
    file.appendFile = (...values: any[]) => {
      const writing = append(...values)
      if (!String(values[0]).includes('"uuid":"held-append"')) return writing
      return writing.then(() => { enter(); return released })
    }
  }, () => {})
  return opened
}) as typeof promises.open)
let drain: Promise<void> | undefined
const originalDrain = project.drainWriteQueue
project.drainWriteQueue = function (...args: any[]) {
  drain = originalDrain.apply(this, args)
  return drain
}
let completed = false
const observations: boolean[] = []
let sameExportPromise = false, sameDrainPromise = false
try {
  await project.appendEntry({ ...createUserMessage({ content: 'actual flush export held native append' }), uuid: 'held-append' })
  const originalExport = storage.flushSessionStorage
  const observeExport = () => {
    const returned = originalExport()
    sameExportPromise = Promise.resolve(returned) === returned
    void returned.then(() => { completed = true })
    return returned
  }
  const exported = observeExport()
  await entered
  if (!drain || Bun.peek.status(drain) !== 'pending') throw new Error('Actual pending drain was not captured')
  sameDrainPromise = Promise.resolve(drain) === drain
  // Register only after the original Project.flush has awaited this same D.
  // Observe its actual FIFO completion, then one following FIFO callback.
  const observation = drain.then(() => {
    observations.push(completed)
    queueMicrotask(() => { observations.push(completed); finishObservation() })
  })
  release()
  await Promise.all([exported, observation, observed])
} finally {
  release()
  project.drainWriteQueue = originalDrain
  openSpy.mockRestore()
}
const transcript = fs.readFileSync(join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl'), 'utf8')
if (!transcript.includes('"uuid":"held-append"') || !completed || caps.blocked.length) throw new Error('Actual private flush did not complete cleanly')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observations, completed, sameExportPromise, sameDrainPromise, persisted: true, blocked: caps.blocked, scope: 'warm transformed original export graph only; not cold authority' }), () => resolve()))
process.exit(0)
