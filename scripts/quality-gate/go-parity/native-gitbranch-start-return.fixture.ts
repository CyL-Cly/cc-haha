import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Git ensureStarted async return')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BRANCH_START_RETURN !== '1') throw new Error('Pinned owned Git start fixture required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
Bun.plugin({ name: 'original-git-start-return-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/git\/gitFilesystem\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { gitWatcher as fixtureGitWatcher }\n' }))
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
const branch = await import('../../../src/utils/git/gitFilesystem')
const watcher = (branch as any).fixtureGitWatcher
const start = watcher.start.bind(watcher)
let inner: Promise<void>, first: Promise<void>, second: Promise<void>
const events: Array<{ phase: number, inner: string, first: string, second: string }> = []
let finish: () => void
const observed = new Promise<void>(resolve => { finish = resolve })
const probe = (phase: number) => {
  events.push({ phase, inner: Bun.peek.status(inner), first: Bun.peek.status(first), second: Bun.peek.status(second) })
  if (phase < 2) queueMicrotask(() => probe(phase + 1))
  else finish()
}
// Synchronous observation returns the exact original start Promise. No async
// adapter, readiness substitute, filesystem delay or new business await.
watcher.start = () => {
  inner = start()
  void inner.then(() => probe(0))
  return inner
}
first = watcher.ensureStarted()
second = watcher.ensureStarted()
const identity = { firstIsInner: first === inner, secondIsInner: second === inner, firstIsSecond: first === second }
await Promise.all([first, second, observed])
const initialized = watcher.initialized
branch.resetGitFileWatcher()
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ identity, events, initialized, blocked: caps.blocked }), error => error ? reject(error) : resolve()))
process.exit(0)
