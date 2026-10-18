// Separate live-only Source process. There is no fake model or Harness binding.
import { mock } from 'bun:test'
import { lstatSync, realpathSync } from 'node:fs'
import { isAbsolute, relative, sep } from 'node:path'

const source = process.env.CC_HAHA_PAIRED_SOURCE_ROOT
const allocation = process.env.CC_HAHA_PAIRED_ALLOCATION
const home = process.env.HOME
if (process.env.CC_HAHA_PAIRED_ALLOW_LIVE !== '1' || !source || !allocation || !home || !isAbsolute(source) || !isAbsolute(allocation) || process.env.USERPROFILE !== home || process.env.CLAUDE_CONFIG_DIR !== home + '/.claude' || realpathSync(process.cwd()) !== realpathSync(home)) throw new Error('Missing isolated live Source boundary')
const below = relative(realpathSync(allocation), realpathSync(home))
if (!below || below === '..' || below.startsWith('..' + sep) || isAbsolute(below) || process.argv.includes('--bare') || process.argv.includes('--tools')) throw new Error('Invalid private HOME or reduced Harness')
// Source's Bash task-output consumer reads CLAUDE_CODE_TMPDIR, not TMPDIR.
// Reject a missing/foreign/aliased root before importing any Source module.
const temporary = home + '/tmp'
const temporaryStat = lstatSync(temporary)
if (process.env.CLAUDE_CODE_TMPDIR !== temporary || process.env.TMPDIR !== temporary || realpathSync(temporary) !== temporary || !temporaryStat.isDirectory() || temporaryStat.isSymbolicLink() || (temporaryStat.mode & 0o777) !== 0o700 || process.getuid && temporaryStat.uid !== process.getuid()) throw new Error('Missing isolated Source CLAUDE_CODE_TMPDIR boundary')
// An isolated test host has no OS MDM or keychain credentials. Bind those
// external observations only; use the original private plaintext store.
mock.module(source + '/src/utils/settings/mdm/rawRead.ts', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module(source + '/src/utils/secureStorage/keychainPrefetch.ts', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
const { plainTextStorage } = await import(source + '/src/utils/secureStorage/plainTextStorage.ts')
mock.module(source + '/src/utils/secureStorage/index.ts', () => ({ getSecureStorage: () => plainTextStorage }))
const fetchOriginal = globalThis.fetch
globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
  const url = new URL(input instanceof Request ? input.url : String(input))
  if (url.origin !== new URL(process.env.ANTHROPIC_BASE_URL!).origin) throw new Error('Unexpected live Source fetch origin: ' + url.origin)
  // Preserve the original fetch Promise; reject redirects before they could
  // escape the isolated loopback transport or carry a provider credential.
  return fetchOriginal(input, { ...init, redirect: 'error' })
}) as typeof fetch
await import(source + '/preload.ts')
process.argv[1] = source + '/src/entrypoints/cli.tsx'
const { main } = await import(source + '/src/main.tsx')
await main()
