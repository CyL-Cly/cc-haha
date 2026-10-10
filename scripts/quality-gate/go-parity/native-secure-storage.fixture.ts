import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { mkdirSync, readFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Secure storage Source comparison')
if (process.env.CC_HAHA_SECURE_STORAGE_FIXTURE !== '1') throw new Error('Missing isolated secure storage marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
let now = 1700000000000
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(value?: string | number) { super(value === undefined ? now : value) }
  static now() { return now }
} as DateConstructor
let primary: string | null = null
let failRead = false
let failWrite = false
const calls: unknown[] = []
const nativeExeca = await import('execa')
const nativePortable = await import('../../../src/utils/execFileNoThrowPortable')
// Only security(1) subprocess primitives are replaced. The original cache,
// fallback decisions, actual plaintext file writes and serialization execute.
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({
  ...nativePortable,
  execSyncWithDefaults_DEPRECATED(command: string) {
    if (command.startsWith('security find-generic-password ')) {
      calls.push('read')
      if (failRead) throw new Error('private keychain unavailable')
      return primary ?? ''
    }
    if (command.startsWith('security delete-generic-password ')) { calls.push('delete'); primary = null; return '' }
    throw new Error('Unexpected security primitive')
  },
}))
mock.module('execa', () => ({
  ...nativeExeca,
  execaSync(command: string, args: string[], options: { input?: string }) {
    if (command !== 'security') throw new Error('Unexpected native command')
    calls.push(args[0] === '-i' ? 'write-stdin' : 'write-argv')
    if (failWrite) return { exitCode: 1 }
    const payload = args[0] === '-i' ? options.input!.match(/ -X "([a-f0-9]*)"\n$/)![1] : args.at(-1)!
    primary = Buffer.from(payload, 'hex').toString('utf8')
    return { exitCode: 0 }
  },
}))
const { macOsKeychainStorage } = await import('../../../src/utils/secureStorage/macOsKeychainStorage')
const { clearKeychainCache } = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const store = createFallbackStorage(macOsKeychainStorage, plainTextStorage)
const path = join(home, '.claude', '.credentials.json')
mkdirSync(join(home, '.claude'), { recursive: true })
const output = []
for (const scenario of input) {
  clearKeychainCache()
  rmSync(path, { force: true })
  primary = scenario.primary
  failRead = false
  failWrite = false
  calls.length = 0
  const observations = []
  for (const step of scenario.steps) {
    if (step.advance) now += step.advance
    if ('failRead' in step) failRead = step.failRead
    if ('failWrite' in step) failWrite = step.failWrite
    if (step.action === 'clear') clearKeychainCache()
    else if (step.action === 'read') observations.push(store.read())
    else if (step.action === 'update') observations.push(store.update(step.value))
    else if (step.action === 'delete') observations.push(store.delete())
  }
  let disk: string | null = null
  try { disk = readFileSync(path, 'utf8') } catch {}
  output.push({ observations, calls: [...calls], primary, disk })
}
process.stdout.write(JSON.stringify(output))
