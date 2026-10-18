import { mock } from 'bun:test'
import fs from 'node:fs'
import { join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const home = assertDisposableFixtureHome(process.env.HOME, 'Actual configuration directory Keychain consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_KEYCHAIN_SOURCE !== '1') throw new Error('Pinned private Keychain Source required')
const caps = await installClientFixtureCapabilities(home)
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const nativeExec = await import('../../../src/utils/execFileNoThrow')
const execa = await import('execa')
let now = 1700000000000
const originalNow = Date.now
Date.now = () => now
let primary = new Map<string, string>()
let failWrite = false
let qualifyAccounts = false
const entryKey = (account: string, service: string) => qualifyAccounts ? account + '\u0000' + service : service
const calls: unknown[] = []
const syncIdentity = (command: string, operation: string) => {
  const suffix = operation === 'read' ? '-w ' : ''
  const match = command.match(new RegExp(`^security ${operation === 'read' ? 'find' : 'delete'}-generic-password -a "([^"]*)" ${suffix}-s "([^"]*)"$`))
  if (!match) throw new Error('Unowned security command ' + command)
  const [, account, service] = match
  calls.push({ operation, account, service, mode: 'sync' })
  return entryKey(account, service)
}
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable,
  execSyncWithDefaults_DEPRECATED(command: string) {
    if (command.startsWith('security find-generic-password ')) return primary.get(syncIdentity(command, 'read')) ?? ''
    if (command.startsWith('security delete-generic-password ')) {
      if (!primary.delete(syncIdentity(command, 'delete'))) throw new Error('Private entry absent')
      return ''
    }
    throw new Error('Unowned security primitive')
  },
}))
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...nativeExec,
  async execFileNoThrow(command: string, args: string[]) {
    if (command !== 'security' || args.length !== 6 || args[0] !== 'find-generic-password' || args[1] !== '-a' || args[3] !== '-w' || args[4] !== '-s') throw new Error('Unowned async security operation')
    calls.push({ operation: 'read', account: args[2], service: args[5], mode: 'async' })
    return { code: 0, stdout: primary.get(entryKey(args[2], args[5])) ?? '' }
  },
}))
mock.module('execa', () => ({ ...execa,
  execaSync(command: string, args: string[], options: { input?: string }) {
    if (command !== 'security' || args.length !== 1 || args[0] !== '-i') throw new Error('Unowned security writer')
    const match = options.input?.match(/^add-generic-password -U -a "([^"]*)" -s "([^"]*)" -X "([a-f0-9]*)"\n$/)
    if (!match) throw new Error('Unowned security writer input')
    const [, account, service, payload] = match
    calls.push({ operation: 'update', account, service, mode: 'stdin' })
    if (failWrite) return { exitCode: 1 }
    primary.set(entryKey(account, service), Buffer.from(payload, 'hex').toString('utf8'))
    return { exitCode: 0 }
  },
}))
const { macOsKeychainStorage } = await import('../../../src/utils/secureStorage/macOsKeychainStorage')
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const { clearKeychainCache, getMacOsKeychainStorageServiceName, getUsername, CREDENTIALS_SERVICE_SUFFIX } = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
const rows = []
try {
  for (const kind of ['absolute', 'normalized', 'relative', 'empty', 'absent']) {
    for (const mode of ['seeded-success', 'absent-success', 'seeded-fallback']) {
      const index = rows.length
      const initial = join(home, `owned-keychain-${index}`, 'initial')
      const target = kind === 'absolute' ? join(home, `owned-keychain-${index}`, 'next')
        : kind === 'normalized' ? join(home, `owned-keychain-${index}`, 'next-e\u0301')
        : kind === 'relative' ? `owned-keychain-relative-${index}`
        : kind === 'empty' ? '' : undefined
      const physicalTarget = resolve((target ?? join(home, '.claude')).normalize('NFC'))
      fs.mkdirSync(initial, { recursive: true })
      fs.mkdirSync(physicalTarget, { recursive: true })
      const oldPlain = JSON.stringify({ value: 'old-plaintext', future: { keep: 41 } })
      const newPlain = JSON.stringify({ value: 'new-plaintext', future: { keep: 73 } })
      fs.writeFileSync(join(initial, '.credentials.json'), oldPlain, { mode: 0o600 })
      fs.writeFileSync(join(physicalTarget, '.credentials.json'), newPlain, { mode: 0o600 })
      process.env.USER = 'owned-keychain-user'
      process.env.CLAUDE_CONFIG_DIR = initial
      const initialService = getMacOsKeychainStorageServiceName(CREDENTIALS_SERVICE_SUFFIX)
      if (target === undefined) delete process.env.CLAUDE_CONFIG_DIR
      else process.env.CLAUDE_CONFIG_DIR = target
      const targetService = getMacOsKeychainStorageServiceName(CREDENTIALS_SERVICE_SUFFIX)
      primary = new Map([[initialService, JSON.stringify({ value: 'old-primary', future: { keep: 29 } })]])
      if (mode !== 'absent-success') primary.set(targetService, JSON.stringify({ value: 'new-primary', future: { keep: 17 } }))
      const seeds = [...primary.entries()]
      failWrite = mode === 'seeded-fallback'
      clearKeychainCache()
      calls.length = 0
      now = 1700000000000
      process.env.CLAUDE_CONFIG_DIR = initial
      const store = createFallbackStorage(macOsKeychainStorage, plainTextStorage)
      const values: unknown[] = [structuredClone(store.read())]
      if (target === undefined) delete process.env.CLAUDE_CONFIG_DIR
      else process.env.CLAUDE_CONFIG_DIR = target
      values.push(structuredClone(store.read()), structuredClone(await store.readAsync()))
      now += 31000
      values.push(structuredClone(await store.readAsync()))
      clearKeychainCache()
      values.push(structuredClone(store.read()))
      const update = { value: 'fresh-write', future: { keep: 99 }, unrelated: [true, 'owned'] }
      values.push(store.update(update), structuredClone(await store.readAsync()))
      values.push(store.delete(), structuredClone(await store.readAsync()))
      let initialDisk: string | null = null, targetDisk: string | null = null
      try { initialDisk = fs.readFileSync(join(initial, '.credentials.json'), 'utf8') } catch {}
      try { targetDisk = fs.readFileSync(join(physicalTarget, '.credentials.json'), 'utf8') } catch {}
      rows.push({ kind, mode, initial, target: target ?? null, physicalTarget, initialService, targetService, oldPlain, newPlain, seeds, update, values, calls: [...calls], primary: [...primary.entries()], initialDisk, targetDisk })
    }
  }
  // USER, like the service directory, is read at the real primitive call.
  // Neither changing it nor reading the new account clears the shared cache.
  qualifyAccounts = true
  failWrite = false
  for (const userKind of ['plain', 'unicode', 'empty', 'absent']) {
    for (const mode of ['seeded-success', 'absent-success']) {
      const initial = join(home, `owned-keychain-user-${rows.length}`)
      fs.mkdirSync(initial, { recursive: true })
      process.env.CLAUDE_CONFIG_DIR = initial
      process.env.USER = 'owned-keychain-user'
      const initialService = getMacOsKeychainStorageServiceName(CREDENTIALS_SERVICE_SUFFIX)
      const targetUser = userKind === 'plain' ? 'owned-next-user'
        : userKind === 'unicode' ? 'owned-用户-é' : userKind === 'empty' ? '' : undefined
      if (targetUser === undefined) delete process.env.USER
      else process.env.USER = targetUser
      const targetAccount = getUsername()
      const oldPlain = JSON.stringify({ value: 'user-fallback', future: { keep: 73 } })
      fs.writeFileSync(join(initial, '.credentials.json'), oldPlain, { mode: 0o600 })
      primary = new Map([[entryKey('owned-keychain-user', initialService), JSON.stringify({ value: 'old-account', future: { keep: 29 } })]])
      if (mode === 'seeded-success') primary.set(entryKey(targetAccount, initialService), JSON.stringify({ value: 'new-account', future: { keep: 17 } }))
      const seeds = [...primary.entries()]
      clearKeychainCache()
      calls.length = 0
      now = 1700000000000
      process.env.USER = 'owned-keychain-user'
      const store = createFallbackStorage(macOsKeychainStorage, plainTextStorage)
      const values: unknown[] = [structuredClone(store.read())]
      if (targetUser === undefined) delete process.env.USER
      else process.env.USER = targetUser
      values.push(structuredClone(store.read()), structuredClone(await store.readAsync()))
      now += 31000
      values.push(structuredClone(await store.readAsync()))
      clearKeychainCache()
      values.push(structuredClone(store.read()))
      const update = { value: 'fresh-user-write', future: { keep: 99 }, unrelated: [true, 'owned'] }
      values.push(store.update(update), structuredClone(await store.readAsync()))
      values.push(store.delete(), structuredClone(await store.readAsync()))
      let initialDisk: string | null = null
      try { initialDisk = fs.readFileSync(join(initial, '.credentials.json'), 'utf8') } catch {}
      rows.push({ kind: 'user-' + userKind, mode, username: true, targetUser: targetUser ?? null, targetAccount, initial, target: initial, physicalTarget: initial, initialService, targetService: initialService, oldPlain, newPlain: oldPlain, seeds, update, values, calls: [...calls], primary: [...primary.entries()], initialDisk, targetDisk: initialDisk })
    }
  }
  if (caps.blocked.length || caps.secureReads.length) throw new Error('Keychain Source escaped private primitive boundary ' + JSON.stringify(caps))
  await new Promise<void>((accept, reject) => process.stdout.write(JSON.stringify({ rows, caps }), error => error ? reject(error) : accept()))
} finally {
  Date.now = originalNow
}
process.exit(0)
