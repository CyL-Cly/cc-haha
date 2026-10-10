import fs from 'node:fs'
import * as crypto from 'node:crypto'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const fields = ['home', 'name', 'timezone', 'steps', 'memory', 'customSystemPrompt']
if (Object.keys(input).some(key => !fields.includes(key)) || !Array.isArray(input.steps) || process.env.NODE_ENV !== 'production' || process.env.USER_TYPE !== 'external' || process.env.CC_HAHA_USER_CONTEXT_FIXTURE !== '1' || input.timezone !== process.env.TZ || input.memory !== undefined && input.memory !== 'plain-delayed') throw new Error('User context requires declared original private input before imports')
const home = assertDisposableFixtureHome(input.home, 'User context')
for (const step of input.steps) if (!['read', 'clock', 'override', 'writeMode', 'resetSettings', 'clearUser', 'clearSessionDate', 'injection', 'schema', 'modeSources', 'flagSettings', 'mutateUser'].includes(step.kind)) throw new Error('Original context operation required before imports')
const project = join(home, 'project'), config = join(home, '.claude'), userPath = join(config, 'settings.json'), projectPath = join(project, '.claude/settings.json')
fs.mkdirSync(join(project, '.claude'), { recursive: true })
fs.mkdirSync(config, { recursive: true })
fs.writeFileSync(join(config, '.claude.json'), '{}')
fs.writeFileSync(userPath, input.memory ? JSON.stringify({ pluginConfigs: { 'agents-md@builtin': { options: { instructionFiles: 'claude-md' } } } }) : '{}')
if (input.memory) fs.writeFileSync(join(config, 'CLAUDE.md'), 'Private original probe instructions.\n')
const absent: string[] = [...absentManagedSettingsFixturePaths()]
for (const base of [home, fs.realpathSync(home)]) {
  for (let path = dirname(base); ; path = dirname(path)) {
    absent.push(join(path, '.git'))
    if (dirname(path) === path) break
  }
}
if (input.memory) {
  const directory = dirname(absent[0]!)
  absent.push(join(directory, 'CLAUDE.md'), join(directory, '.claude/rules'))
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
const NativeDate = Date
let clock = NativeDate.parse('2026-10-01T23:59:59.000Z'), serial = 0
const semanticAllocations: unknown[] = []
globalThis.Date = new Proxy(NativeDate, { construct(target, args, newTarget) { return Reflect.construct(target, args.length ? args : [clock], newTarget) }, get(target, key, receiver) { return key === 'now' ? () => clock : Reflect.get(target, key, receiver) } })
spyOn(crypto, 'randomUUID').mockImplementation(() => {
  const value = '00000000-0000-4000-8000-' + String(++serial).padStart(12, '0')
  semanticAllocations.push({ kind: 'uuid', value, millis: clock })
  return value as any
})
const bootstrap = await import('../../../src/bootstrap/state')
const configAPI = await import('../../../src/utils/config')
const settingsCache = await import('../../../src/utils/settings/settingsCache')
const settings = await import('../../../src/utils/settings/settings')
const common = await import('../../../src/constants/common')
const context = await import('../../../src/context')
const instruction = await import('../../../src/utils/instructionFiles')
const query = await import('../../../src/utils/queryContext')
const api = await import('../../../src/utils/api')
bootstrap.setOriginalCwd(project)
bootstrap.setCwdState(project)
bootstrap.setProjectRoot(project)
configAPI.enableConfigs()
let readSettled: Promise<void> | undefined, releaseRead: (() => void) | undefined
const io: any[] = []
if (input.memory) {
  bootstrap.setAllowedSettingSources(['userSettings'])
  bootstrap.setAdditionalDirectoriesForClaudeMd([project])
  process.env.CLAUDE_CODE_DISABLE_CLAUDE_MDS = '0'
  const fsOperations = await import('../../../src/utils/fsOperations'), original = fsOperations.getFsImplementation()
  let settle!: () => void
  readSettled = new Promise<void>(resolve => { settle = resolve })
  const hold = new Promise<void>(resolve => { releaseRead = resolve })
  fsOperations.setFsImplementation({ ...original, readFile(path: any, options: any) {
    const native = original.readFile(path, options)
    if (path !== join(config, 'CLAUDE.md')) return native
    io.push({ kind: 'read-start', millis: clock, path })
    return native.then(content => {
      io.push({ kind: 'native-read-settled', millis: clock, content })
      settle()
      return hold.then(() => {
        io.push({ kind: 'read-release', millis: clock })
        return content
      })
    })
  } })
}
const sourceOnly = { importAllocations: [...semanticAllocations] }
// This component's allocation domain starts after actual production imports.
// Retain import-only allocations above instead of feeding their offset to Go.
serial = 0
semanticAllocations.length = 0
let previousPromise: any, previousValue: any
const results: any[] = []
for (const step of input.steps) {
  const start = semanticAllocations.length
  let result: any = null
  if (step.kind === 'clock') clock = NativeDate.parse(step.value)
  else if (step.kind === 'override') {
    if (step.delete) delete process.env.CLAUDE_CODE_OVERRIDE_DATE
    else process.env.CLAUDE_CODE_OVERRIDE_DATE = step.raw === undefined ? step.value : JSON.parse(step.raw)
  }
  else if (step.kind === 'flagSettings') bootstrap.setFlagSettingsInline(step.value)
  else if (step.kind === 'mutateUser') {
    if (!previousValue) throw new Error('Mutation requires actual existing memo')
    previousValue.currentDate = step.value
  }
  else if (step.kind === 'writeMode') fs.writeFileSync(step.source === 'project' ? projectPath : userPath, JSON.stringify({ pluginConfigs: { 'agents-md@builtin': { options: { instructionFiles: step.value } } } }))
  else if (step.kind === 'resetSettings') settingsCache.resetSettingsCache()
  else if (step.kind === 'clearUser') context.getUserContext.cache.clear()
  else if (step.kind === 'clearSessionDate') common.getSessionStartDate.cache.clear()
  else if (step.kind === 'injection') context.setSystemPromptInjection(step.value)
  else if (step.kind === 'schema') {
    fs.writeFileSync(userPath, step.raw)
    settingsCache.resetSettingsCache()
    const parsed = settings.parseSettingsFile(userPath)
    result = { settingsJSON: JSON.stringify(parsed.settings), errorsJSON: JSON.stringify(parsed.errors) }
  }
  else if (step.kind === 'modeSources') result = { mode: instruction.getInstructionFilesModeFromSettings(...step.sources) }
  else {
    const sessionDate = common.getSessionStartDate()
    const promise = context.getUserContext()
    let pending: any = null
    if (input.memory && previousPromise === undefined) {
      await readSettled
      pending = { samePromise: context.getUserContext() === promise, cachedMemoryJSON: JSON.stringify(bootstrap.getCachedClaudeMdContent()) }
      clock = NativeDate.parse('2026-10-02T00:00:01.000Z')
      releaseRead!()
    }
    const parts = await query.fetchSystemPromptParts({ tools: [], mainLoopModel: 'fixture-model', additionalWorkingDirectories: [], mcpClients: [], customSystemPrompt: input.customSystemPrompt ?? 'fixture-system' })
    const value = await promise
    const prefix = api.prependUserContext([], parts.userContext)
    result = { sessionDateJSON: JSON.stringify(sessionDate), localDateJSON: JSON.stringify(common.getLocalISODate()), userContextJSON: JSON.stringify(value), prefixJSON: JSON.stringify(prefix), samePromise: previousPromise === promise, sameValue: previousValue === value, partsUsesSameValue: parts.userContext === value, cachedMemoryJSON: JSON.stringify(bootstrap.getCachedClaudeMdContent()), mode: instruction.getInstructionFilesMode(), cacheSize: context.getUserContext.cache.size, pending, io: [...io] }
    previousPromise = promise
    previousValue = value
  }
  results.push({ step, result, allocations: semanticAllocations.slice(start) })
}
if (process.env.HOME !== home || process.env.CLAUDE_CONFIG_DIR !== config) throw new Error('User context left private HOME')
process.stdout.write(JSON.stringify({ name: input.name, results, capabilities, sourceOnly }))
