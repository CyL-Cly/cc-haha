import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual original observation Promise identity')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_ORIGINAL_OBSERVER_IDENTITY !== '1') throw new Error('Pinned owned original identity fixture required')
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
const identities: unknown[] = []
for (const profile of ['sync', 'async']) {
  let originalPromise: Promise<unknown>
  const sync = (...args: Parameters<typeof storage.recordTranscript>) => {
    originalPromise = storage.recordTranscript(...args)
    return originalPromise
  }
  const asynchronous = async (...args: Parameters<typeof storage.recordTranscript>) => {
    originalPromise = storage.recordTranscript(...args)
    return originalPromise
  }
  const observedPromise = (profile === 'sync' ? sync : asynchronous)([createUserMessage({ content: 'actual original identity ' + profile })])
  const statuses: string[] = []
  await originalPromise.then(() => {
    statuses.push(Bun.peek.status(observedPromise))
    queueMicrotask(() => statuses.push(Bun.peek.status(observedPromise)))
  })
  await observedPromise
  await storage.flushSessionStorage()
  identities.push({ profile, samePromise: observedPromise === originalPromise, statuses })
}
const text = fs.readFileSync(join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl'), 'utf8')
if (!text.includes('actual original identity sync') || !text.includes('actual original identity async') || caps.blocked.length) throw new Error('Actual original identity proof escaped private file')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ identities, persisted: true, blocked: caps.blocked }), () => resolve()))
process.exit(0)
