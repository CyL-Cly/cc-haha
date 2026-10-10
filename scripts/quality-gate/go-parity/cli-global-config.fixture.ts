import fs, * as fsExports from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { syncBuiltinESMExports } from 'node:module'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import type { GlobalConfigProfile } from './cli-global-config-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as GlobalConfigProfile & { home: string }
const home = assertDisposableFixtureHome(input.home, 'cli global config')
const configDir = join(home, '.claude')
const path = input.legacy ? join(configDir, '.config.json') : join(configDir, `.claude${input.suffix ?? ''}.json`)
const backupDir = join(configDir, 'backups')
const checkSeed = (file: string) => { const path = relative(home, file); if (path === '..' || path.startsWith('../') || path.startsWith('..\\')) throw new Error('Global config fixture seed must stay in HOME') }
checkSeed(path)
for (const name of Object.keys(input.backups ?? {})) checkSeed(join(backupDir, name))
for (const name of Object.keys(input.backupBytes ?? {})) checkSeed(join(backupDir, name))
const real = { ...fs }
let now = 946684800000
const RealDate = Date
class FixtureDate extends RealDate {
  constructor(...args: any[]) { super(...(args.length ? args : [now]) as [any]) }
  static now() { return now }
}
globalThis.Date = FixtureDate as DateConstructor
Object.defineProperty(process, 'pid', { value: 12345 })
fs.mkdirSync(configDir, { recursive: true })
if (input.raw !== undefined || input.rawBytes !== undefined) {
  const target = input.symlink ? join(configDir, 'actual-config.json') : path
  fs.writeFileSync(target, input.rawBytes !== undefined ? Buffer.from(input.rawBytes) : input.raw!.replaceAll('<HOME>', home), { mode: input.mode ?? 0o600 })
  fs.utimesSync(target, new RealDate(now - 1000), new RealDate(now - 1000))
  if (input.symlink) fs.symlinkSync('actual-config.json', path)
}
for (const [name, raw] of Object.entries(input.backups ?? {})) {
  fs.mkdirSync(backupDir, { recursive: true })
  fs.writeFileSync(join(backupDir, name), raw, { mode: 0o600 })
}
for (const [name, raw] of Object.entries(input.backupBytes ?? {})) {
  fs.mkdirSync(backupDir, { recursive: true })
  fs.writeFileSync(join(backupDir, name), Buffer.from(raw), { mode: 0o600 })
}
if (input.lock) {
  fs.mkdirSync(path + '.lock')
  const stamp = input.lock === 'stale' ? now - 11000 : now
  fs.utimesSync(path + '.lock', new RealDate(stamp), new RealDate(stamp))
}

const ancestorGit: string[] = []
for (const allocationPath of [home, fs.realpathSync(home)]) {
  for (let directory = dirname(allocationPath); ; directory = dirname(directory)) {
    ancestorGit.push(join(directory, '.git'))
    if (dirname(directory) === directory) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), ...ancestorGit] })
const effects: any[] = [], results: any[] = []
const realSetTimeout = globalThis.setTimeout, realClearTimeout = globalThis.clearTimeout
const leaseTimers: Array<{ callback: () => void; delay: number; cancelled: boolean; unref: () => void }> = []
const leaseProfile = input.steps.some(step => step.operation.startsWith('lease'))
if (leaseProfile) {
  globalThis.setTimeout = ((callback: any, delay: any, ...args: any[]) => {
    if (String(callback).includes('lock.updateTimeout')) { const timer = { callback, delay, cancelled: false, unref() {} }; leaseTimers.push(timer); return timer }
    return realSetTimeout(callback, delay, ...args)
  }) as any
  globalThis.clearTimeout = ((timer: any) => { if (leaseTimers.includes(timer)) timer.cancelled = true; else realClearTimeout(timer) }) as any
}
const faultCounts = new Map<number, number>()
const fault = (operation: string, paths: string[]) => {
  for (const [index, rule] of (input.faults ?? []).entries()) {
    if (rule.operation !== operation || rule.match && !paths.some(path => path.includes(rule.match!))) continue
    const count = (faultCounts.get(index) ?? 0) + 1
    faultCounts.set(index, count)
    if (count === rule.occurrence || rule.repeat && count > rule.occurrence) {
      const error = new Error(`fixture fault: ${operation}`) as NodeJS.ErrnoException
      error.code = 'EIO'
      throw error
    }
  }
}
const relevant = (value: string) => value === path || value === path + '.lock' || value.startsWith(backupDir) || value.startsWith(path + '.tmp.') || value.startsWith(join(configDir, 'actual-config.json'))
const short = (value: string) => relative(home, value).replaceAll('\\', '/')
// The external capability adapter has checked every opened path before this
// owned-fd grant. Source's real fsync writer uses numeric descriptors; no
// arbitrary descriptor or external path can bypass the adapter.
const owned = new Map<number, string>()
const checkedOpen = fs.openSync, checkedWrite = fs.writeFileSync
const open: any = (...args: any[]) => {
  const fd = checkedOpen(...args as [any, any, any])
  owned.set(fd, String(args[0]))
  if (relevant(String(args[0])) && args[1] !== 'r') effects.push({ kind: 'openWrite', path: short(String(args[0])), mode: args[2] ?? null })
  return fd
}
const write: any = (...args: any[]) => {
  if (typeof args[0] !== 'number') {
    if (relevant(String(args[0]))) effects.push({ kind: 'writeFlush', path: short(String(args[0])), bytes: String(args[1]), mode: args[2]?.mode ?? null })
    if (relevant(String(args[0]))) fault('write', [String(args[0])])
    return checkedWrite(...args as [any, any, any])
  }
  const file = owned.get(args[0])
  if (!file) throw new Error('Fixture refuses unowned file descriptor')
  if (relevant(file)) effects.push({ kind: 'write', path: short(file), bytes: String(args[1]) })
  return real.writeFileSync(...args as [any, any, any])
}
const flush = (fd: number) => {
  const file = owned.get(fd)
  if (!file) throw new Error('Fixture refuses unowned fsync descriptor')
  if (relevant(file)) effects.push({ kind: 'flush', path: short(file) })
  return real.fsyncSync(fd)
}
const close = (fd: number) => {
  const file = owned.get(fd)
  if (file && relevant(file)) effects.push({ kind: 'closeWrite', path: short(file) })
  owned.delete(fd)
  return real.closeSync(fd)
}
for (const [name, implementation] of Object.entries({ openSync: open, writeFileSync: write, fsyncSync: flush, closeSync: close })) {
  ;(fs as any)[name] = implementation
  spyOn(fsExports as any, name).mockImplementation(implementation)
}
syncBuiltinESMExports()
let watcher: ((stats: fs.Stats) => void) | undefined
fs.watchFile = ((file: string, options: any, callback: any) => { watcher = callback; return {} }) as any
fs.unwatchFile = (() => {}) as any
const checkedChmod = fs.chmodSync
fs.chmodSync = (file, mode) => { if (relevant(String(file))) fault('chmod', [String(file)]); return checkedChmod(file, mode) }
// Bun has distinct bare/node builtin ESM bindings. Route Source's bare fs
// imports through the same path-checked real capabilities, including owned
// descriptors; this does not replace any config/parser/writer policy.
mock.module('fs', () => ({ ...fs, default: fs }))
mock.module('fs/promises', () => ({ ...fs.promises, default: fs.promises }))
await import('../../../preload')
const oauth = await import('../../../src/constants/oauth')
spyOn(oauth, 'fileSuffixForOauthConfig').mockImplementation(() => input.suffix ?? '')
const operations = await import('../../../src/utils/fsOperations')
const original = operations.getFsImplementation()
const wrapped = { ...original }
const watchReads: Array<Promise<unknown>> = []
type ReadGate = { started: Promise<void>; release: Promise<void>; onStart: () => void; onRelease: () => void }
let nextReadGate: ReadGate | undefined, pendingReadGate: ReadGate | undefined
wrapped.readFile = (...args: Parameters<typeof original.readFile>) => {
  const nativeRead = original.readFile(...args)
  const gate = nextReadGate
  nextReadGate = undefined
  const pending = gate ? nativeRead.then(async raw => { gate.onStart(); await gate.release; return raw }) : nativeRead
  watchReads.push(pending)
  return pending
}
for (const name of ['mkdirSync', 'copyFileSync', 'renameSync', 'unlinkSync', 'readlinkSync', 'readFileSync', 'statSync'] as const) {
  ;(wrapped as any)[name] = (...args: any[]) => {
    if (relevant(String(args[0]))) fault(({ mkdirSync: 'mkdir', copyFileSync: 'copy', renameSync: 'rename', unlinkSync: 'remove', readlinkSync: 'readlink', readFileSync: 'read', statSync: 'stat' })[name], args.map(String))
    // Preserve real I/O; only successful mutation records enter this trace.
    const result = (original as any)[name](...args)
    if (relevant(String(args[0])) && !['readlinkSync', 'readFileSync', 'statSync'].includes(name)) effects.push({ kind: name, paths: args.slice(0, name === 'copyFileSync' || name === 'renameSync' ? 2 : 1).map((value: string) => short(value)) })
    return result
  }
}
operations.setFsImplementation(wrapped)
const config = await import('../../../src/utils/config')
const env = await import('../../../src/utils/env')
const locks = await import('../../../src/utils/lockfile')
let releaseLease: (() => void) | undefined
const leaseState = () => ({ exists: real.existsSync(path + '.lock'), mtime: real.existsSync(path + '.lock') ? real.statSync(path + '.lock').mtime.getTime() : null })
const snapshots = () => {
  const files: Record<string, any> = {}
  const visit = (directory: string) => {
    for (const entry of real.readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const file = join(directory, entry.name)
      if (entry.isDirectory()) { if (file === backupDir || file === path + '.lock') visit(file); continue }
      if (!relevant(file)) continue
      const stat = real.lstatSync(file)
      files[short(file)] = entry.isSymbolicLink() ? { symlink: real.readlinkSync(file) } : { bytes: real.readFileSync(file, 'utf8'), rawBase64: real.readFileSync(file).toString('base64'), mode: stat.mode & 0o777 }
    }
  }
  visit(configDir)
  return files
}
for (const step of input.steps) {
  let value: unknown, failure: unknown = null, calls = 0
  try {
    const updater = (current: any) => {
      calls++
      effects.push({ kind: 'updater', current: JSON.stringify(current) })
      if (step.updater === 'throw' || step.updater === 'throwOnce' && calls === 1) throw new Error('fixture updater failure')
      if (step.updater === 'same') return current
      return { ...current, ...JSON.parse(step.patch ?? '{}') }
    }
    switch (step.operation) {
      case 'enable': config.enableConfigs(); break
      case 'get': value = config.getGlobalConfig(); break
      case 'firstStart': config.recordFirstStartTime(); break
      case 'save': config.saveGlobalConfig(updater); break
      case 'advance': now += step.milliseconds ?? 0; break
      case 'externalWrite': real.writeFileSync(path, step.raw ?? ''); real.utimesSync(path, new RealDate(now + 2000), new RealDate(now + 2000)); break
      case 'remove': real.unlinkSync(path); break
      case 'poll': {
        const started = watchReads.length
        watcher?.(real.statSync(path))
        // Drain the actual asynchronous filesystem operation. Source's own
        // then/catch reaction is registered before this allSettled observer;
        // setImmediate alone does not guarantee I/O has finished.
        await Promise.allSettled(watchReads.slice(started))
        break
      }
      case 'pollPending': {
        let onStart!: () => void, onRelease!: () => void
        pendingReadGate = { started: new Promise(resolve => { onStart = resolve }), release: new Promise(resolve => { onRelease = resolve }), onStart: () => onStart(), onRelease: () => onRelease() }
        nextReadGate = pendingReadGate
        watcher?.(real.statSync(path))
        await pendingReadGate.started // Native I/O finished; Source reaction is still pending.
        break
      }
      case 'releasePoll': pendingReadGate!.onRelease(); await Promise.allSettled(watchReads); break
      case 'project': value = config.getCurrentProjectConfig(home); break
      case 'saveProject': config.saveCurrentProjectConfig(updater, home); break
      case 'leaseAcquire': releaseLease = locks.lockSync(path, { lockfilePath: path + '.lock', onCompromised(error) { effects.push({ kind: 'compromised', message: error.message }) } }); value = leaseState(); break
      case 'leaseTick': { const timer = leaseTimers.find(timer => !timer.cancelled); if (!timer) throw new Error('Missing actual Source lease timer'); timer.cancelled = true; timer.callback(); value = leaseState(); break }
      case 'leaseRelease': releaseLease!(); value = leaseState(); break
      case 'leaseSteal': real.utimesSync(path + '.lock', new RealDate(now + 2000), new RealDate(now + 2000)); break
      case 'leaseRemove': real.rmdirSync(path + '.lock'); break
    }
  } catch (error: any) { failure = { name: error.name, message: error.message, ...(error.filePath ? { filePath: short(error.filePath), defaults: JSON.stringify(error.defaultConfig) } : {}) } }
  results.push({ operation: step.operation, value: value === undefined ? null : JSON.stringify(value), calls, error: failure, files: snapshots() })
}
process.stdout.write(JSON.stringify({ path: short(env.getGlobalClaudeFile()), results, effects, capabilities }) + '\n')
process.exit(0)
