import { AsyncLocalStorage } from 'node:async_hooks'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original writer-state inline diagnostic')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_RECORD_STATE_OBSERVER !== '1') throw new Error('Pinned private writer-state diagnostic required')
if (!['serial', 'concurrent'].includes(input.profile) || typeof input.seed !== 'string') throw new Error('Finite original record-state profile required')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}

// The full original Project and recordTranscript bodies are unchanged. Adding
// private observation exports transforms module loading, so this is explicitly
// a warm diagnostic, never a replacement for the untouched cold SDK oracle.
let sessionStorageSHA256 = ''
Bun.plugin({ name: 'original-writer-state-observation-exports', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/sessionStorage\.ts$/ }, args => {
    const original = fs.readFileSync(args.path)
    sessionStorageSHA256 = createHash('sha256').update(original).digest('hex')
    return {
      loader: 'ts',
      contents: original.toString('utf8').replace(/\/\/# sourceMappingURL=.*$/gm, '')
        + '\nexport { getProject as fixtureGetProject, getSessionMessages as fixtureGetSessionMessages }\n',
    }
  })
} })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: 'fixture-writer-state', BUILD_TIME: '', PACKAGE_URL: '' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000')
process.env.USER_TYPE = 'ant'
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const storage = await import('../../../src/utils/sessionStorage')
const project = (storage as any).fixtureGetProject()
const path = join(storage.getProjectDir(home), bootstrap.getSessionId() + '.jsonl')
fs.mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
fs.writeFileSync(path, input.seed, { mode: 0o600 })
project.ensureCurrentSessionFile()
const set: Set<string> = await (storage as any).fixtureGetSessionMessages(bootstrap.getSessionId())
if (set.size !== 2 || !set.has('old-user') || !set.has('old-assistant')) throw new Error('Actual seed UUID cache did not load')

type Context = { kind: string, record: string[], selected: string[] }
type Observation = {
  phase: string
  record: string[]
  selected: string[]
  uuid: string | null
  set: string[]
  setCount: number
  tracked: number
  queued: number
  memo: string | null
}
const contexts = new AsyncLocalStorage<Context>()
const events: Observation[] = []
const identities: Array<{ method: string, same: boolean }> = []
let active = true
let tracked: number = project.pendingWriteCount
let memo: string | undefined = project.currentSessionLastPrompt
const snapshot = (phase: string, record: string[] = [], selected: string[] = [], uuid: string | null = null) => {
  if (!active) return
  let queued = 0
  for (const entries of project.writeQueues.values()) queued += entries.length
  events.push({ phase, record: [...record], selected: [...selected], uuid, set: [...set], setCount: set.size, tracked, queued, memo: memo ?? null })
}
const trackedDescriptor = Object.getOwnPropertyDescriptor(project, 'pendingWriteCount')
const memoDescriptor = Object.getOwnPropertyDescriptor(project, 'currentSessionLastPrompt')
if (!trackedDescriptor?.configurable || memoDescriptor && !memoDescriptor.configurable) throw new Error('Original data field descriptors changed')
Object.defineProperty(project, 'pendingWriteCount', {
  configurable: true,
  enumerable: trackedDescriptor.enumerable,
  get() {
    // This is the original Project.flush read after its actual await drain.
    // ALS labels an existing continuation; it adds no then/await/reaction.
    if (contexts.getStore()?.kind === 'flush') snapshot('flush-after-drain')
    return tracked
  },
  set(value: number) {
    const previous = tracked
    tracked = value
    if (value === previous + 1) snapshot('tracked-add')
    else if (value === previous - 1) snapshot('tracked-remove')
    else if (active) throw new Error('Unexpected original tracked counter mutation')
  },
})
Object.defineProperty(project, 'currentSessionLastPrompt', {
  configurable: true,
  enumerable: memoDescriptor?.enumerable ?? true,
  get() { return memo },
  set(value: string | undefined) {
    memo = value
    const context = contexts.getStore()
    snapshot('memo', context?.record, context?.selected)
  },
})
const addDescriptor = Object.getOwnPropertyDescriptor(set, 'add')
const originalAdd = set.add
set.add = function (value: string) {
  const result = originalAdd.call(this, value)
  snapshot('set-add', [], [], value)
  return result
}

// Every wrapper returns exactly the original callee's Promise. The observer
// never registers .then/.finally, adds a queueMicrotask, holds native readiness,
// or supplies a replacement I/O result.
const invokeOriginal = (method: string, original: (...args: any[]) => any, receiver: any, args: any[], context: Context) => {
  let direct!: Promise<unknown>
  const returned = contexts.run(context, () => {
    direct = Reflect.apply(original, receiver, args)
    return direct
  })
  identities.push({ method, same: returned === direct && Promise.resolve(returned) === returned })
  return returned
}
const originals = new Map<string, (...args: any[]) => any>()
for (const method of ['insertMessageChain', 'appendEntry', 'flush']) {
  const original = project[method]
  if (typeof original !== 'function') throw new Error('Missing original Project method: ' + method)
  originals.set(method, original)
  project[method] = function (...args: any[]) {
    const parent = contexts.getStore()
    const context = { kind: method === 'flush' ? 'flush' : method, record: parent?.record ?? [], selected: parent?.selected ?? [] }
    if (method === 'insertMessageChain') {
      context.selected = args[0].map((message: { uuid: string }) => message.uuid)
      snapshot('selection', context.record, context.selected)
    }
    return invokeOriginal(method, original, this, args, context)
  }
}
const originalRecord = storage.recordTranscript
const record = (messages: any[]) => {
  const ids = messages.map(message => message.uuid)
  snapshot('record', ids)
  return invokeOriginal('recordTranscript', originalRecord, storage, [messages], { kind: 'record', record: ids, selected: [] })
}
const old = input.seed.trim().split('\n').map((line: string) => JSON.parse(line))
const later = { type: 'user', uuid: 'later-id', message: { role: 'user', content: 'later original' } }
const next = { type: 'user', uuid: 'next-id', message: { role: 'user', content: 'now original\nnext original' } }
const first = record([...old, later])
let second: Promise<unknown>
if (input.profile === 'concurrent') second = record([...old, later, next])
const firstResult = await first
if (input.profile === 'serial') {
  await project.flush()
  second = record([...old, later, next])
}
const secondResult = await second!
await project.flush()
const finalMemo = memo ?? null
active = false
for (const [method, original] of originals) project[method] = original
Object.defineProperty(project, 'pendingWriteCount', { ...trackedDescriptor, value: tracked })
Object.defineProperty(project, 'currentSessionLastPrompt', { ...memoDescriptor, value: memo })
if (addDescriptor) Object.defineProperty(set, 'add', addDescriptor)
else delete (set as any).add
storage.reAppendSessionMetadata()
await storage.flushSessionStorage()
const transcript = fs.readFileSync(path, 'utf8')
if (!transcript.includes('"uuid":"later-id"') || !transcript.includes('"uuid":"next-id"') || caps.blocked.length) throw new Error('Original writer escaped private files or omitted actual rows: ' + caps.blocked.join(', '))
if (identities.some(value => !value.same)) throw new Error('An observation wrapper replaced the original Promise')
const result = {
  profile: input.profile, events, results: [firstResult, secondResult], memo: finalMemo, transcript,
  identities, blocked: caps.blocked, diagnostic: 'transformed-warm-not-cold-authority',
  sourceIdentity: { sessionStorageSHA256, mode: 'full-original-plus-observer-exports' },
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result), error => error ? reject(error) : resolve()))
process.exit(0)
