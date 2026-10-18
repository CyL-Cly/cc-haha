import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual getBranch async alias return')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_RECORD_BRANCH_RETURN !== '1') throw new Error('Pinned owned getBranch return fixture required')
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
const git = await import('../../../src/utils/git')
const branch = await import('../../../src/utils/git/gitFilesystem')
const { spyOn } = await import('bun:test')
const value = await git.getBranch()
const original = branch.getCachedBranch
let cached: Promise<string>, returned: Promise<string>
const events: Array<{ phase: number, cached: string, returned: string }> = []
let finish: () => void
const observed = new Promise<void>(resolve => { finish = resolve })
const probe = (phase: number) => {
  events.push({ phase, cached: Bun.peek.status(cached), returned: Bun.peek.status(returned) })
  if (phase < 2) queueMicrotask(() => probe(phase + 1))
  else finish()
}
spyOn(branch, 'getCachedBranch').mockImplementation(() => {
  cached = original()
  void cached.then(() => probe(0))
  return cached
})
returned = git.getBranch()
const result = await returned
await observed
spyOn(branch, 'getCachedBranch').mockRestore()
branch.resetGitFileWatcher()
if (result !== value || caps.blocked.length) throw new Error('Original getBranch escaped owned primitives')
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ events, samePromise: cached === returned, value, result, blocked: caps.blocked }), () => resolve()))
process.exit(0)
