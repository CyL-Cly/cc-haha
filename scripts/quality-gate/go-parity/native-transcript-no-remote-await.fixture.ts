import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Project no-remote append await')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_NO_REMOTE_APPEND !== '1') throw new Error('Pinned owned append fixture required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
// Export only an observer accessor. The full original module, Project class,
// appendEntry, persistToRemote, filesystem and Promise bodies remain unchanged.
Bun.plugin({ name: 'actual-project-no-remote-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => ({ loader: 'ts', contents: fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '') + '\nexport { getProject as fixtureGetProject }\n' }))
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-no-remote', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000017')
process.env.USER_TYPE = 'ant'
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const { createUserMessage } = await import('../../../src/utils/messages')
const project = (storage as any).fixtureGetProject()
const first = createUserMessage({ content: 'actual first local message' })
await storage.recordTranscript([first])
await storage.flushSessionStorage()
const events: Array<{ phase: string, status: string }> = []
const original = project.persistToRemote
let append: Promise<void>
project.persistToRemote = function (...args: any[]) {
  const result = original.apply(this, args)
  events.push({ phase: 'remote-return', status: Bun.peek.status(result) })
  queueMicrotask(() => events.push({ phase: 'before-remote-await-reaction', status: Bun.peek.status(append) }))
  return result
}
const next = createUserMessage({ content: 'actual second local message' })
append = project.appendEntry(next)
await append
events.push({ phase: 'append-return', status: Bun.peek.status(append) })
project.persistToRemote = original
await storage.flushSessionStorage()
const text = fs.readFileSync(join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl'), 'utf8')
if (!text.includes(next.uuid) || !text.includes('actual second local message')) throw new Error('Original append did not persist actual local message')
if (caps.blocked.length) throw new Error('Original append escaped fixture: ' + caps.blocked.join(', '))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ events, persisted: true, blocked: caps.blocked }), () => resolve()))
process.exit(0)
