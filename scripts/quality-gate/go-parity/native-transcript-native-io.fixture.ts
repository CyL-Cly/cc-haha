import fs from 'node:fs'
import * as fsp from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Project native mkdir Promise reaction')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TRANSCRIPT_NATIVE_IO !== '1') throw new Error('Pinned owned native I/O fixture required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
Bun.plugin({ name: 'actual-project-native-io-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject }\n' }))
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-native-io', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000018')
process.env.USER_TYPE = 'ant'
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const project = (storage as any).fixtureGetProject()
const target = join(home, 'private-native-io', 'owned.jsonl')
const events: Array<{ phase: string, opens: number }> = []
let opens = 0
// The capability guard already wraps these exports with Bun spies. Preserve
// its existing implementation, rather than retaining the mutable spy object.
const originalOpen = (fsp.open as any).getMockImplementation() as typeof fsp.open
spyOn(fsp, 'open').mockImplementation(((...args: Parameters<typeof fsp.open>) => {
  if (args[0] === target) opens++
  return originalOpen(...args)
}) as typeof fsp.open)
const originalMkdir = (fsp.mkdir as any).getMockImplementation() as typeof fsp.mkdir
spyOn(fsp, 'mkdir').mockImplementation(((...args: Parameters<typeof fsp.mkdir>) => {
  if (args[0] !== dirname(target)) return originalMkdir(...args)
  // Only expose the actual private native mkdir completion. Resolving its
  // Promise never executes the awaiting original prepareTranscriptFile body
  // inline; that body's already-registered reaction precedes our observer.
  return new Promise((resolve, reject) => {
    originalMkdir(...args).then(value => {
      resolve(value)
      events.push({ phase: 'native-ready-before-await-body', opens })
      queueMicrotask(() => events.push({ phase: 'after-await-body-reaction', opens }))
    }, reject)
  })
}) as typeof fsp.mkdir)
await project.prepareTranscriptFile(target)
events.push({ phase: 'prepare-return', opens })
if (!fs.statSync(target).isFile() || caps.blocked.length) throw new Error('Original native I/O preparation escaped or did not create its target')
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ events, blocked: caps.blocked, physical: true }), error => error ? reject(error) : resolve()))
process.exit(0)
