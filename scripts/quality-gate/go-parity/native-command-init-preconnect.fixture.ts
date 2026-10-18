import fs from 'node:fs'
import { dirname, join, resolve, relative } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

// Exercise the original exported main/Commander preAction and real command
// handler. Only the absent external managed/keychain OS stores are substituted.
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual ordinary command init HEAD')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_COMMAND_INIT_HEAD_SOURCE !== '1') throw new Error('Pinned ordinary command init fixture required')
if (typeof input.origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin)) throw new Error('Explicit private loopback origin required')
const argumentsByProfile: Record<string, string[]> = {
  agents: ['agents'],
  bare: ['--bare', 'agents'],
  plugin: ['plugin', 'marketplace', 'list', '--json'],
  help: ['--help'],
  agentHelp: ['agents', '--help'],
  version: ['--version'],
  parseError: ['agents', '--unknown-private-option'],
}
const args = argumentsByProfile[input.profile]
if (!args) throw new Error('Unknown ordinary command init profile')
const fetchReturnFIFO = input.fetchReturnFIFO
if (fetchReturnFIFO !== undefined && (typeof fetchReturnFIFO !== 'string' || resolve(fetchReturnFIFO) !== fetchReturnFIFO || relative(home, fetchReturnFIFO).startsWith('..') || !fs.lstatSync(fetchReturnFIFO).isFIFO())) throw new Error('Private real fetch-return FIFO required')
const extraEnvironment = input.environment ?? {}
if (!extraEnvironment || typeof extraEnvironment !== 'object' || Array.isArray(extraEnvironment) || Object.values(extraEnvironment).some(value => typeof value !== 'string')) throw new Error('Scalar environment input required')
const project = join(home, 'workspace'), config = join(home, '.claude')
const knownMarketplacesFile = join(config, 'plugins', 'known_marketplaces.json')
if (input.knownReadFIFO === true && !fs.lstatSync(knownMarketplacesFile).isFIFO()) throw new Error('Actual private known-marketplace FIFO required')
const protectedFiles = [
  { path: join(config, 'settings.json'), content: JSON.stringify({ env: { ANTHROPIC_BASE_URL: input.origin + '/safe', PROBE_COMMAND_INIT_SAFE: 'user', ...extraEnvironment }, futurePrivate: { keep: true } }) },
  { path: join(project, '.claude', 'settings.json'), content: JSON.stringify({ env: { ANTHROPIC_BASE_URL: input.origin + '/project', PROBE_COMMAND_INIT_PROJECT: 'project' }, futureProject: { keep: true } }) },
  { path: join(config, 'agents', 'head-reader.md'), content: '---\nname: head-reader\ndescription: Private ordinary command reader\ntools: Read\n---\nRead only private fixture files.\n' },
]
if (input.knownContent !== undefined) {
  if (typeof input.knownContent !== 'string') throw new Error('Actual known-marketplace file content required')
  protectedFiles.push({ path: knownMarketplacesFile, content: input.knownContent })
}
for (const file of protectedFiles) {
  fs.mkdirSync(dirname(file.path), { recursive: true })
  fs.writeFileSync(file.path, file.content)
}
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/Library/Application Support/ClaudeCode/.claude/agents', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const start of [home, fs.realpathSync(home)]) {
  for (let directory = dirname(start); ; directory = dirname(directory)) {
    for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.agents/commands']) absent.push(join(directory, suffix))
    if (directory === dirname(directory)) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
const calls: Array<Record<string, unknown>> = [], timeouts: number[] = []
const timeoutSignals = new WeakMap<AbortSignal, number>()
const fetchPrimitive = globalThis.fetch
globalThis.fetch = ((...parameters: Parameters<typeof fetch>) => {
  const [target, options] = parameters
  calls.push({ url: String(target), method: options?.method ?? 'GET', signal: options?.signal instanceof AbortSignal, timeout: options?.signal ? timeoutSignals.get(options.signal) : undefined, safe: process.env.PROBE_COMMAND_INIT_SAFE, project: process.env.PROBE_COMMAND_INIT_PROJECT, certificates: process.env.NODE_EXTRA_CA_CERTS })
  const pending = fetchPrimitive(...parameters)
  if (input.exitImmediatelyAfterFetch === true) process.exit(0)
  if (fetchReturnFIFO !== undefined) {
    fs.writeFileSync(join(home, 'ordinary-command-fetch-return.json'), JSON.stringify({ calls, timeouts }))
    // Hold only the real external fetch primitive's return. Its physical I/O
    // remains live; no Source init/handler or Promise implementation changes.
    fs.readFileSync(fetchReturnFIFO)
  }
  return pending
}) as typeof fetch
const originalTimeout = AbortSignal.timeout
spyOn(AbortSignal, 'timeout').mockImplementation(delay => {
  timeouts.push(delay)
  const signal = originalTimeout.call(AbortSignal, delay)
  timeoutSignals.set(signal, delay)
  return signal
})
;(globalThis as any).MACRO = { VERSION: '999.0.0-command-init-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(project)
state.setOriginalCwd(project)
state.setProjectRoot(project)
process.argv = [process.execPath, new URL('../../../src/entrypoints/cli.tsx', import.meta.url).pathname, ...args]
process.on('exit', code => {
  const protectedUnchanged = protectedFiles.every(file => fs.readFileSync(file.path, 'utf8') === file.content)
  fs.writeFileSync(join(home, 'ordinary-command-init-source-receipt.json'), JSON.stringify({ code, calls, timeouts, safe: process.env.PROBE_COMMAND_INIT_SAFE, project: process.env.PROBE_COMMAND_INIT_PROJECT, protectedUnchanged, caps }))
})
const { main } = await import('../../../src/main')
await main()
