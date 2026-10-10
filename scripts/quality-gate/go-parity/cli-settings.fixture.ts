import fs, * as fsExports from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { syncBuiltinESMExports } from 'node:module'
import { mock, spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Object.keys(input).some(key => !['home','name','raw','rawBase64','encoding','operations','failWrites','fault','source','symlink'].includes(key))) throw new Error('CLI settings requires declared private fixture before production imports')
const home = assertDisposableFixtureHome(input.home, 'CLI settings private probe')
if (input.source !== undefined && !['userSettings','projectSettings'].includes(input.source) || input.fault !== undefined && !['temp','chmod','rename','cleanup'].includes(input.fault) || input.encoding !== undefined && input.encoding !== 'utf16le' || input.symlink !== undefined && !['relative','absolute'].includes(input.symlink) || !Array.isArray(input.operations)) throw new Error('CLI settings requires declared original input profile before production imports')

if (!feature('TRANSCRIPT_CLASSIFIER') || process.env.NODE_ENV !== 'production' || process.env.CC_HAHA_CLI_SETTINGS_FIXTURE !== '1' || process.env.USER_TYPE !== 'external' || process.env.CLAUDE_CODE_ENABLE_XAA !== '0') throw new Error('Probe requires explicit release/external/private inputs before production imports')
const root = resolve(import.meta.dir, '../../..')
const project = join(home, 'project'), settingsPath = input.source === 'userSettings' ? join(home,'.claude/settings.json') : join(project, '.claude/settings.json')
const actual = { ...fs }
const effects: any[] = []
const relevant = (path: unknown) => typeof path === 'string' && (path === settingsPath || path.startsWith(settingsPath + '.tmp.'))
const short = (path: string) => relative(home, path)
let now = 946684800000
const RealDate = Date
class ProbeDate extends RealDate { constructor(...args: any[]) { super(...(args.length ? args : [now]) as [any]) }; static now() { return now } }
globalThis.Date = ProbeDate as DateConstructor
Object.defineProperty(process, 'pid', { value: 12345 })
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude/.claude.json'), '{}')
fs.mkdirSync(dirname(settingsPath), { recursive: true })
if (input.rawBase64 !== undefined) fs.writeFileSync(settingsPath, Buffer.from(input.rawBase64,'base64'), { mode: 0o640 })
if (input.raw !== undefined) fs.writeFileSync(settingsPath, input.encoding === 'utf16le' ? Buffer.concat([Buffer.from([255, 254]), Buffer.from(input.raw, 'utf16le')]) : input.raw, { mode: 0o640 })
if (input.symlink) { const target = join(dirname(settingsPath),'actual.json'); fs.renameSync(settingsPath,target); fs.symlinkSync(input.symlink === 'absolute' ? target : 'actual.json',settingsPath) }
const absentGit: string[] = []
for (const base of [home, fs.realpathSync(home)]) for (let current = dirname(base); ; current = dirname(current)) { absentGit.push(join(current, '.git')); if (dirname(current) === current) break }
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), ...absentGit] })

const checkedOpen = fs.openSync, checkedWrite = fs.writeFileSync, checkedChmod = fs.chmodSync, checkedRename = fs.renameSync, checkedUnlink = fs.unlinkSync
const fds = new Set<number>()
const open = (...args: any[]) => { const fd = checkedOpen(...args as [any, any, any]); fds.add(fd); return fd }
const close = (fd: number) => { if (!fds.has(fd)) throw new Error('Probe refuses unowned fd'); fds.delete(fd); return actual.closeSync(fd) }
const write = (...args: any[]) => {
  if (typeof args[0] === 'number') { if (!fds.has(args[0])) throw new Error('Probe refuses unowned write fd'); return actual.writeFileSync(...args as [any, any, any]) }
  if (relevant(args[0])) {
    effects.push({ kind: 'writeFlush', path: short(args[0]), bytes: String(args[1]), options: args[2] })
    if (input.failWrites || input.fault === 'temp' && String(args[0]).includes('.tmp.')) { const error = new Error('private fixture write fault') as NodeJS.ErrnoException; error.code = 'EIO'; throw error }
  }
  return checkedWrite(...args as [any, any, any])
}
const chmod = (path: any, mode: any) => { if (relevant(path)) effects.push({ kind: 'chmod', path: short(path), mode }); if (input.fault === 'chmod') throw new Error('private fixture chmod fault'); return checkedChmod(path, mode) }
const rename = (source: any, target: any) => { if (relevant(source)) effects.push({ kind: 'rename', source: short(source), target: short(target) }); if (input.fault === 'rename') throw new Error('private fixture rename fault'); return checkedRename(source, target) }
const unlink = (path: any) => { if (relevant(path)) effects.push({ kind: 'unlink', path: short(path) }); if (input.fault === 'cleanup') throw new Error('private fixture cleanup fault'); return checkedUnlink(path) }
for (const [name, fn] of Object.entries({ openSync: open, closeSync: close, writeFileSync: write, chmodSync: chmod, renameSync: rename, unlinkSync: unlink })) { (fs as any)[name] = fn; spyOn(fsExports as any, name).mockImplementation(fn) }
syncBuiltinESMExports()
mock.module('fs', () => ({ ...fs, default: fs }))
mock.module('fs/promises', () => ({ ...fs.promises, default: fs.promises }))
await import(join(root, 'preload.ts'))
const internal = await import(join(root, 'src/utils/settings/internalWrites.ts'))
const cache = await import(join(root, 'src/utils/settings/settingsCache.ts'))
const mark = internal.markInternalWrite, reset = cache.resetSettingsCache
spyOn(internal, 'markInternalWrite').mockImplementation((path: string) => { effects.push({ kind: 'mark', path: short(path) }); mark(path) })
spyOn(cache, 'resetSettingsCache').mockImplementation(() => { effects.push({ kind: 'reset' }); reset() })
const types = await import(join(root, 'src/utils/settings/types.ts'))
const validation = await import(join(root, 'src/utils/settings/validation.ts'))
const settings = await import(join(root, 'src/utils/settings/settings.ts'))
const json = await import(join(root, 'src/utils/json.ts'))
const config = await import(join(root, 'src/utils/config.ts'))
const bootstrap = await import(join(root, 'src/bootstrap/state.ts'))
bootstrap.setOriginalCwd(project)
config.enableConfigs()
reset(); internal.clearInternalWrites(); effects.length = 0

function view(value: any) {
  const fields: any[] = []
  const visit = (current: any, path: string[]) => {
    fields.push({ path, kind: current === undefined ? 'undefined' : current === null ? 'null' : Array.isArray(current) ? 'array' : typeof current, ...(typeof current === 'number' ? { number: String(current), negativeZero: Object.is(current, -0) } : {}), ...(current && typeof current === 'object' ? { keys: Object.keys(current) } : {}) })
    if (current && typeof current === 'object') for (const key of Object.keys(current)) visit(current[key], [...path, key])
  }
  visit(value, [])
  return { source: JSON.stringify(value) ?? null, fields }
}
const results: any[] = []
let last: any, previous: any, sourceValue: any
for (const operation of input.operations) {
  let observation: any = null
  switch (operation.kind) {
    case 'schema': {
      const raw = JSON.parse(operation.raw)
      const filtered = validation.filterInvalidPermissionRules(raw, settingsPath)
      const parsed = types.SettingsSchema().safeParse(filtered.data)
      observation = parsed.success ? { success: true, value: view(parsed.data), warnings: filtered.warnings } : { success: false, warnings: filtered.warnings, issues: parsed.error.issues, formatted: validation.formatZodError(parsed.error, settingsPath) }
      break
    }
    case 'getSource': { const previousSource = sourceValue; sourceValue = settings.getSettingsForSource(input.source ?? 'projectSettings'); observation = { value: view(sourceValue), ...(previousSource ? { sameSource: previousSource === sourceValue } : {}) }; break }
    case 'mutateSource': { let target = sourceValue; for (const key of operation.path.slice(0, -1)) target = target[key]; target[operation.path.at(-1)] = JSON.parse(operation.raw); break }
    case 'parse': previous = last; last = settings.parseSettingsFile(settingsPath); observation = { value: view(last.settings), errors: last.errors, ...(previous ? { sameSettings: previous.settings === last.settings, sameErrors: previous.errors === last.errors } : {}) }; break
    case 'mutateErrors': last.errors.push(JSON.parse(operation.raw)); break
    case 'mutate': { let target = last.settings; for (const key of operation.path.slice(0, -1)) target = target[key]; target[operation.path.at(-1)] = JSON.parse(operation.raw); break }
    case 'warmContent': last = json.safeParseJSON(input.raw, false); observation = view(last); break
    case 'contentAgain': observation = view(json.safeParseJSON(input.raw, false)); break
    case 'overwrite': actual.writeFileSync(settingsPath, operation.raw); break
    case 'update': {
      const patch = JSON.parse(operation.patch)
      for (const path of operation.undefinedPaths ?? []) { let object = patch; for (const key of path.slice(0, -1)) object = object[key]; object[path.at(-1)] = undefined }
      const result = settings.updateSettingsForSource(input.source ?? 'projectSettings', patch, project)
      observation = { patch: view(patch), error: result.error ? { name: result.error.name, message: result.error.message } : null }
      break
    }
    case 'reset': reset(); break
    case 'consumeMark': observation = internal.consumeInternalWrite(settingsPath, 5000); break
    default: throw new Error('Unknown private probe operation')
  }
  results.push({ operation, observation, effects: effects.slice(), file: actual.existsSync(settingsPath) ? { text: actual.readFileSync(settingsPath, 'utf8'), rawBase64: actual.readFileSync(settingsPath).toString('base64'), mode: actual.statSync(settingsPath).mode & 0o777 } : null, cachedParsePresent: cache.getCachedParsedFile(settingsPath) !== undefined })
}
process.stdout.write(JSON.stringify({ name: input.name, results, capabilities }))
