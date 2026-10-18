import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Git branch compute async return')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BRANCH_COMPUTE_RETURN !== '1') throw new Error('Pinned owned compute branch fixture required')
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
Bun.plugin({ name: 'actual-git-compute-return-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/git\/gitFilesystem\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { gitWatcher as fixtureGitWatcher, computeBranch as fixtureComputeBranch }\n' }))
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
const branch = await import('../../../src/utils/git/gitFilesystem')
const watcher = (branch as any).fixtureGitWatcher
const compute = (branch as any).fixtureComputeBranch
// This leaf consumes an initialized watcher with an empty branch cache. It is
// not a normal cold main profile or a module prewarm offered as a P0 fix.
await watcher.ensureStarted()
let computing: Promise<string>, returned: Promise<string>
const events: Array<{ phase: number, computed: string, returned: string, cached: boolean }> = []
let finish: () => void
const observed = new Promise<void>(resolve => { finish = resolve })
const probe = (phase: number) => {
  events.push({ phase, computed: Bun.peek.status(computing), returned: Bun.peek.status(returned), cached: watcher.cache.has('branch') })
  if (phase < 2) queueMicrotask(() => probe(phase + 1))
  else finish()
}
returned = watcher.get('branch', () => {
  computing = compute()
  void computing.then(() => probe(0))
  return computing
})
const value = await returned
await observed
branch.resetGitFileWatcher()
if (value !== 'HEAD' || caps.blocked.length) throw new Error('Actual compute escaped original private IO')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ events, value, blocked: caps.blocked }), () => resolve()))
process.exit(0)
