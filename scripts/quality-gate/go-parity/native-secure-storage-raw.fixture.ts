import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Secure storage raw Source')
if (process.env.CC_HAHA_SECURE_RAW_FIXTURE !== '1') throw new Error('Unowned secure raw fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
let now = 1700000000000
const originalNow = Date.now
Date.now = () => now
let primary: string | null = null
let failRead = false
let failWrite = false
const calls: string[] = []
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const exec = await import('../../../src/utils/execFileNoThrow')
const execa = await import('execa')
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable,
  execSyncWithDefaults_DEPRECATED(command: string) {
    if (command.startsWith('security find-generic-password ')) { calls.push('read'); if (failRead) throw new Error('private failure'); return primary?.trim() ?? '' }
    if (command.startsWith('security delete-generic-password ')) { calls.push('delete'); primary = null; return '' }
    throw new Error('Unowned security operation')
  },
}))
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...exec,
  async execFileNoThrow(command: string, args: string[]) {
    if (command !== 'security' || args[0] !== 'find-generic-password') throw new Error('Unowned async security operation')
    calls.push('read')
    return { code: failRead ? 1 : 0, stdout: primary ?? '' }
  },
}))
mock.module('execa', () => ({ ...execa,
  execaSync(command: string, args: string[], options: { input?: string }) {
    if (command !== 'security') throw new Error('Unowned security writer')
    calls.push('write')
    if (failWrite) return { exitCode: 1 }
    const payload = args[0] === '-i' ? options.input!.match(/ -X "([a-f0-9]*)"\n$/)![1] : args.at(-1)!
    primary = Buffer.from(payload, 'hex').toString('utf8')
    return { exitCode: 0 }
  },
}))
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { macOsKeychainStorage } = await import('../../../src/utils/secureStorage/macOsKeychainStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const { clearKeychainCache } = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
const file = join(home, '.claude', '.credentials.json')
mkdirSync(join(home, '.claude'), { recursive: true })
const output = []
try {
  for (const row of input) {
    clearKeychainCache()
    rmSync(file, { force: true })
    if (row.secondary !== undefined) writeFileSync(file, row.secondary, { mode: 0o600 })
    primary = row.primary ?? null
    failRead = false
    failWrite = false
    calls.length = 0
    const store = row.platform === 'darwin' ? createFallbackStorage(macOsKeychainStorage, plainTextStorage) : plainTextStorage
    const values = []
    for (const step of row.steps) {
      if (step.advance) now += step.advance
      if (step.failRead !== undefined) failRead = step.failRead
      if (step.failWrite !== undefined) failWrite = step.failWrite
      if (step.action === 'sync') values.push(structuredClone(store.read()))
      else if (step.action === 'async') values.push(structuredClone(await store.readAsync()))
      else if (step.action === 'update') values.push(store.update(step.value))
      else if (step.action === 'delete') values.push(store.delete())
      else if (step.action === 'clear') clearKeychainCache()
      else throw new Error('Unowned state action')
    }
    let disk = null
    try { disk = readFileSync(file, 'utf8') } catch {}
    output.push({ values, calls: [...calls], primary, disk })
  }
  process.stdout.write(JSON.stringify(output))
} finally { Date.now = originalNow }
