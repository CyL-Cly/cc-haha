import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual UUID load async returns')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_UUID_LOAD_RETURN !== '1') throw new Error('Pinned owned UUID fixture required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  const candidate = join(path, '.git')
  if (!fs.existsSync(candidate)) absent.push(candidate)
  if (dirname(path) === path) break
}
let transcript: Promise<any>, session: Promise<any>, cached: Promise<Set<string>>
const events: Array<{ phase: number, transcript: string, session: string, cached: string }> = []
let finish: () => void
const observed = new Promise<void>(resolve => { finish = resolve })
const probe = (phase: number) => {
  events.push({ phase, transcript: Bun.peek.status(transcript), session: Bun.peek.status(session), cached: Bun.peek.status(cached) })
  if (phase < 3) queueMicrotask(() => probe(phase + 1))
  else finish()
}
;(globalThis as any).fixtureObserveUUIDPromise = (kind: string, actual: Promise<any>) => {
  if (kind === 'transcript') {
    transcript = actual
    void actual.then(() => probe(0))
  } else session = actual
  return actual
}
Bun.plugin({ name: 'original-uuid-load-return-observer', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => {
    let contents = fs.readFileSync(args.path, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '')
    const definition = 'async function loadSessionFile(sessionId: UUID)'
    const returned = 'return loadTranscriptFile(sessionFile)'
    if (contents.split(definition).length !== 2 || contents.split(returned).length !== 2) throw new Error('Original loadSessionFile callsite changed')
    // Only add synchronous observers which return the same original Promise.
    // The full original async body, lexical caller and actual I/O remain intact.
    contents = contents.replace(definition, 'async function fixtureOriginalLoadSessionFile(sessionId: UUID)')
      .replace(returned, 'return (globalThis as any).fixtureObserveUUIDPromise("transcript", loadTranscriptFile(sessionFile))')
    contents += '\nfunction loadSessionFile(sessionId: UUID) { return (globalThis as any).fixtureObserveUUIDPromise("session", fixtureOriginalLoadSessionFile(sessionId)) }\nexport { getSessionMessages as fixtureGetSessionMessages }\n'
    return { loader: 'ts', contents }
  })
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
const sid = '00000000-0000-4000-8000-000000000028'
bootstrap.switchSession(sid)
process.env.USER_TYPE = 'ant'
;(await import('../../../src/utils/settings/settings')).getInitialSettings()
process.env.USER_TYPE = 'external'
;(await import('../../../src/utils/config')).enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const path = join(storage.getProjectDir(home), sid + '.jsonl')
fs.mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
fs.writeFileSync(path, input.content, { mode: 0o600 })
cached = (storage as any).fixtureGetSessionMessages(sid)
const coalesced = (storage as any).fixtureGetSessionMessages(sid) === cached
const keys = [...await cached]
await observed
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ events, keys, coalesced, blocked: caps.blocked }), error => error ? reject(error) : resolve()))
process.exit(0)
