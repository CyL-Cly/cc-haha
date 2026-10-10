// Actual TS upgrade oracle with traced, fault-injected filesystem operations.
import { spyOn } from 'bun:test'
import * as fs from 'node:fs/promises'
import * as crypto from 'node:crypto'
import { join, relative, isAbsolute } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

type Step = { operation: string; config?: string; patch?: Record<string, unknown> }
type Fault = { operation: string; path: string; message: string; once?: boolean; used?: boolean }
type Request = { homeDir: string; steps: Step[]; faults?: Fault[] }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as Request
assertDisposableFixtureHome(input.homeDir, 'Migration')
if (!input.homeDir || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) {
  throw new Error('Migration fixture requires disposable HOME/config')
}
const migrations = await import('../../../src/server/services/persistentStorageMigrations')
const { SettingsService } = await import('../../../src/server/services/settingsService')
const { ManagedSettingsService } = await import('../../../src/server/services/managedSettingsService')
const terminalEnvironment = await import('../../../src/utils/terminalShellEnvironment')
const trace: Array<{ operation: string; paths: string[] }> = []
function before(operation: string, paths: string[]) {
  const names = paths.map(path => {
    const name = relative(input.homeDir, path).replace(/\\/g, '/')
    if (name === '..' || name.startsWith('../') || isAbsolute(name)) throw new Error('Fixture path escaped disposable HOME')
    return name
  })
  trace.push({ operation, paths: names })
  const fault = input.faults?.find(fault => fault.operation === operation && names[0]!.startsWith(fault.path) && (!fault.once || !fault.used))
  if (fault) { fault.used = true; throw new Error(fault.message) }
}
for (const [method, operation, count] of [
  ['readFile', 'read', 1], ['writeFile', 'write', 1], ['mkdir', 'mkdir', 1],
  ['rename', 'rename', 2], ['copyFile', 'copy', 2], ['unlink', 'unlink', 1], ['access', 'access', 1],
] as const) {
  const original = fs[method] as (...args: unknown[]) => Promise<unknown>
  spyOn(fs, method).mockImplementation((async (...args: unknown[]) => {
    before(operation, args.slice(0, count).map(String))
    return original(...args)
  }) as never)
}
spyOn(Date, 'now').mockReturnValue(1_700_000_000_000)
spyOn(crypto, 'randomBytes').mockImplementation(((size: number) => Buffer.from(Array.from({ length: size }, (_, index) => index % 3 + 1))) as never)
spyOn(terminalEnvironment, 'getProcessEnvWithTerminalShellEnvironment').mockResolvedValue({})
console.warn = () => {}
const service = new SettingsService()
const managed = new ManagedSettingsService()
const results: unknown[] = []
for (const step of input.steps) {
  if (step.config) {
    if (isAbsolute(step.config) || step.config === '..' || step.config.startsWith('../')) throw new Error('Config must be a fixture-relative path')
    process.env.CLAUDE_CONFIG_DIR = join(input.homeDir, step.config)
  }
  try {
    let value: unknown = null
    switch (step.operation) {
      case 'ensure': value = await migrations.ensurePersistentStorageUpgraded(); break
      case 'reset': migrations.resetPersistentStorageMigrationsForTests(); break
      case 'get-user': value = await service.getUserSettings(); break
      case 'managed-read': value = await managed.readSettings(); break
      case 'managed-update': value = await managed.updateSettings(current => ({ settings: { ...current, ...step.patch }, result: 'migration fixture update' })); break
      default: throw new Error(`Unknown fixture operation: ${step.operation}`)
    }
    results.push({ value })
  } catch (error) { results.push({ error: error instanceof Error ? error.message : String(error) }) }
}
console.log(JSON.stringify({ results, trace }))
