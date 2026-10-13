import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const home = assertDisposableFixtureHome(process.env.HOME, 'Actual auto-mode handlers')
if (process.env.CC_HAHA_AUTO_COMMAND_FIXTURE !== '1') throw new Error('Unowned auto-mode command fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
// The isolated machine has no managed policy or keychain credential. Preserve
// the handlers/settings/model parser and bind only those external OS stores.
mock.module('../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
await import('../../preload')
// Preserve the actual CLI module load order; settings-first has a Source TDZ.
await import('../../src/main')
const settings = await import('../../src/utils/settings/settings')
const side = await import('../../src/utils/sideQuery')
const handlers = await import('../../src/cli/handlers/autoMode')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const stdout = process.stdout.write.bind(process.stdout)
const stderr = process.stderr.write.bind(process.stderr)
const results = []
for (const row of rows) {
  let out = '', error = ''
  const calls: unknown[] = []
  const rawSettings = spyOn(settings, 'getSettingsForSource').mockImplementation(source => row.sources[source])
  const model = spyOn(side, 'sideQuery').mockImplementation(async options => {
    // This pure handler fixture stops at the original side-query producer.
    // Physical SDK and public CLI request tests exercise the actual consumer.
    calls.push(options)
    if (row.failure) throw new Error(row.failure)
    return row.response
  })
  process.stdout.write = ((chunk: unknown) => { out += String(chunk); return true }) as any
  process.stderr.write = ((chunk: unknown) => { error += String(chunk); return true }) as any
  process.exitCode = 0
  try {
    if (row.operation === 'defaults') handlers.autoModeDefaultsHandler()
    else if (row.operation === 'config') handlers.autoModeConfigHandler()
    else await handlers.autoModeCritiqueHandler({ model: row.model })
    results.push({ stdout: out, stderr: error, code: process.exitCode, calls })
  } finally {
    rawSettings.mockRestore()
    model.mockRestore()
    process.stdout.write = stdout
    process.stderr.write = stderr
    process.exitCode = 0
  }
}
stdout(JSON.stringify({ results, blocked: capabilities.blocked }))
