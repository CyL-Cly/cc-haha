import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual prelinked module import')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PRELINKED_MODULE_IMPORT !== '1') throw new Error('Pinned actual module fixture required')
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json']
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    const candidate = join(path, '.git')
    if (!fs.existsSync(candidate)) absent.push(candidate)
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
// These are absent external OS stores. No module body, import operation,
// Promise, queueMicrotask, plugin loader or handler is replaced.
const externalReads: string[] = []
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({
  startMdmRawRead: () => { externalReads.push('mdm:start') },
  getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }),
  fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }),
}))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({
  getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }),
  getLegacyApiKeyPrefetchPromise: () => undefined,
  startKeychainPrefetch: () => { externalReads.push('keychain:start') },
  clearLegacyApiKeyPrefetch: () => {},
  ensureKeychainPrefetchCompleted: async () => {},
}))
;(globalThis as any).MACRO = { VERSION: '999.0.0-prelinked-module', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.argv[1] = new URL('../../../src/entrypoints/cli.tsx', import.meta.url).pathname
await import('../../../src/main')
const target = new URL('../../../src/cli/handlers/plugins.ts', import.meta.url).href
const first = import(target)
const originalIdentity = Promise.resolve(first) === first
const namespace = await first
const promises = new Set<Promise<unknown>>([first])
const rows = []
for (let index = 0; index < 3; index++) {
  const pending = import(target)
  const identity = Promise.resolve(pending) === pending
  const distinctImportPromise = !promises.has(pending)
  promises.add(pending)
  let done = false, returned: unknown, failed: unknown
  pending.then(value => { returned = value; done = true }, error => { failed = error; done = true })
  let microtasks = 0
  const probe = () => {
    microtasks++
    if (!done && microtasks < 1000) queueMicrotask(probe)
  }
  queueMicrotask(probe)
  await pending
  if (!done || failed) throw new Error('Actual warm module import failed: ' + String(failed))
  rows.push({ microtasks, identity, distinctImportPromise, sameNamespace: returned === namespace })
}
if (caps.blocked.length) throw new Error('Module import escaped owned capabilities: ' + JSON.stringify(caps.blocked))
process.stdout.write(JSON.stringify({
  originalIdentity,
  exportedActualHandler: typeof namespace.marketplaceListHandler === 'function',
  namespaceOwnKeys: Object.keys(namespace),
  rows,
  externalReads,
  caps,
  scope: 'Whole original main/plugins module bodies; warmed module import protocol only, no handler invocation or cold I/O/HEAD timing claim',
}) + '\n')
