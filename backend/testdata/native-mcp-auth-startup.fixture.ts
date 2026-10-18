import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Whole MCP auth startup')
if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4' || process.platform !== 'darwin' || process.env.CC_HAHA_MCP_AUTH_STARTUP_SOURCE !== '1') throw new Error('Pinned private Darwin MCP startup required')
if (!['flag-bare', 'env-simple', 'forced-oauth', 'disabled-claudeai'].includes(input.profile) || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin)) throw new Error('Unknown private auth startup profile')
const config = join(home, '.claude')
fs.mkdirSync(config, { recursive: true })
fs.writeFileSync(join(config, 'settings.json'), '{}\n')
process.env.USER = 'owned-mcp-fixture'
if (input.profile === 'env-simple') process.env.CLAUDE_CODE_SIMPLE = '1'
if (input.profile === 'forced-oauth') process.env.CLAUDE_CODE_OAUTH_TOKEN = 'private-inference-only'
if (input.profile === 'disabled-claudeai') process.env.ENABLE_CLAUDEAI_MCP_SERVERS = 'false'
const fakeSecurity = join(home, 'private-empty-security.sh')
fs.writeFileSync(fakeSecurity, '#!/bin/sh\nexit 44\n', { mode: 0o700 })
const spawnPrimitive = Bun.spawn.bind(Bun)
const spawnSyncPrimitive = Bun.spawnSync.bind(Bun)
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
const absentMcpPaths: string[] = []
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  const mcp = join(path, '.mcp.json')
  try {
    fs.lstatSync(mcp)
    throw new Error('Actual ancestor MCP config is present; refuse fixture admission: ' + mcp)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
    absent.push(mcp)
    absentMcpPaths.push(mcp)
  }
  for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) if (!fs.existsSync(join(path, suffix))) absent.push(join(path, suffix))
  if (dirname(path) === path) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin, originalSecureStorage: true })
mock.module('../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead() {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
// Retain the complete original keychainPrefetch module. Only security(1)'s
// external execFile primitive is mapped to an actual private empty process.
// No token getter, prefetch Promise, migration, sink or handler is replaced.
const prefetched: Array<{ command: string, args: string[], timeout: number, code?: number, signal?: string | null, stdout?: string, stderr?: string }> = []
const processes = require('node:child_process')
const processExports = await import('node:child_process')
// v3 established these exact original read callers. Retain the existing deny
// implementation for every other sync command; no real security(1) is invoked.
// Capture that implementation before changing its spy, rather than retaining
// the mutable mock wrapper and accidentally recursing through the new spy.
const syncGuard = processes.spawnSync as any
const deniedSpawnSync = typeof syncGuard.getMockImplementation === 'function' ? syncGuard.getMockImplementation() : syncGuard
if (typeof deniedSpawnSync !== 'function') throw new Error('Missing existing synchronous subprocess deny guard')
const syncCalls: Array<{ command: unknown, args: unknown, options: unknown, stack: string | undefined, kind?: 'legacy' | 'oauth', admitted?: boolean, physical?: unknown, result?: unknown, error?: string }> = []
const sourceAuthPath = fileURLToPath(new URL('../../src/utils/auth.ts', import.meta.url))
const sourceReadPath = fileURLToPath(new URL('../../src/utils/secureStorage/macOsKeychainStorage.ts', import.meta.url))
const sourceExecPath = fileURLToPath(new URL('../../src/utils/execFileNoThrowPortable.ts', import.meta.url))
const serviceSuffix = createHash('sha256').update(config).digest('hex').slice(0, 8)
const privateSpawnSync = (...operands: any[]) => {
  const [command, args, options] = operands
  const env = options?.env
  const stack = new Error('Observed Source spawnSync').stack
  const record: typeof syncCalls[number] = { command, args: Array.isArray(args) ? [...args] : args, options: options && {
    cwd: options.cwd, shell: options.shell, timeout: options.timeout, encoding: options.encoding,
    argv0: options.argv0, stdio: options.stdio, windowsHide: options.windowsHide, maxBuffer: options.maxBuffer,
    environmentKeys: env && Object.keys(env).sort(),
    privateEnvironment: env && Object.fromEntries(['HOME', 'USERPROFILE', 'CLAUDE_CONFIG_DIR', 'USER', 'PATH'].map(key => [key, env[key]])),
  }, stack }
  syncCalls.push(record)
  const legacy = command === `security find-generic-password -a $USER -w -s "Claude Code-${serviceSuffix}"` && stack?.includes(sourceAuthPath + ':1078:')
  const oauth = command === `security find-generic-password -a "owned-mcp-fixture" -w -s "Claude Code-credentials-${serviceSuffix}"` && stack?.includes(sourceReadPath + ':39:') && stack?.includes(sourceAuthPath + ':1319:')
  const privateEnv = env && env.HOME === home && env.USERPROFILE === home && env.CLAUDE_CONFIG_DIR === config && env.USER === 'owned-mcp-fixture' && env.PATH === '/usr/bin:/bin' && env.TMPDIR === home && Object.keys(env).length === Object.keys(process.env).length && Object.keys(env).every(key => env[key] === process.env[key])
  const privateOptions = Array.isArray(args) && args.length === 0 && options?.cwd === home && options.shell === true && options.timeout === 600_000 && options.encoding === 'buffer' && options.maxBuffer === 1_000_000 && JSON.stringify(options.stdio) === '["ignore","pipe","pipe"]' && options.windowsHide === true && options.killSignal === 'SIGTERM' && options.input === undefined && options.argv0 === undefined && options.uid === undefined && options.gid === undefined && !options.detached
  if ((!legacy && !oauth) || !stack?.includes(sourceExecPath + ':72:') || !privateEnv || !privateOptions) return deniedSpawnSync(...operands)
  const executable = fs.lstatSync(fakeSecurity)
  if (!executable.isFile() || executable.isSymbolicLink() || fs.realpathSync(fakeSecurity) !== fakeSecurity || fs.readFileSync(fakeSecurity, 'utf8') !== '#!/bin/sh\nexit 44\n') return deniedSpawnSync(...operands)
  record.kind = legacy ? 'legacy' : 'oauth'
  record.admitted = true
  record.physical = { cmd: [fakeSecurity], cwd: home, timeout: options.timeout, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' }
  try {
    const child = spawnSyncPrimitive([fakeSecurity], { cwd: home, env: { ...env }, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe', timeout: options.timeout, killSignal: options.killSignal, maxBuffer: options.maxBuffer })
    const stdout = Buffer.from(child.stdout), stderr = Buffer.from(child.stderr)
    const result = { pid: child.pid, status: child.exitCode, signal: child.signalCode ?? null, stdout, stderr, output: [null, stdout, stderr] }
    record.result = { pid: result.pid, status: result.status, signal: result.signal, stdout: { isBuffer: Buffer.isBuffer(stdout), bytes: stdout.length, utf8: stdout.toString('utf8') }, stderr: { isBuffer: Buffer.isBuffer(stderr), bytes: stderr.length, utf8: stderr.toString('utf8') }, outputNullStdin: result.output[0] === null }
    return result // Node spawnSync shape, populated only from this real child.
  } catch (error) {
    record.error = String(error)
    throw error
  }
}
processes.spawnSync = privateSpawnSync
spyOn(processExports, 'spawnSync').mockImplementation(privateSpawnSync as any)
const privateExecFile = (command: string, args: string[], options: any, callback: any) => {
  if (command !== 'security' || args.length !== 6 || args[0] !== 'find-generic-password' || args[1] !== '-a' || args[2] !== 'owned-mcp-fixture' || args[3] !== '-w' || args[4] !== '-s' || !args[5]?.startsWith('Claude Code') || options?.timeout !== 10_000 || typeof callback !== 'function') throw new Error('Unexpected external Keychain primitive')
  const record: typeof prefetched[number] = { command, args: [...args], timeout: options.timeout }
  prefetched.push(record)
  const child = spawnPrimitive([fakeSecurity], { cwd: home, env: { HOME: home, USERPROFILE: home, TMPDIR: home, PATH: '/usr/bin:/bin' }, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGTERM'), options.timeout)
  void Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]).then(([code, stdout, stderr]) => {
    clearTimeout(timer)
    Object.assign(record, { code, signal: child.signalCode, stdout, stderr })
    callback(code === 0 ? null : Object.assign(new Error('Private absent Keychain receipt'), { code, killed: child.signalCode !== null }), stdout, stderr)
  })
  return child
}
processes.execFile = privateExecFile
spyOn(processExports, 'execFile').mockImplementation(privateExecFile as any)
;(globalThis as any).MACRO = { VERSION: '999.0.0-mcp-auth-startup', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
process.argv = [process.execPath, fileURLToPath(new URL('../../src/entrypoints/cli.tsx', import.meta.url)), ...(input.profile === 'flag-bare' ? ['--bare'] : []), 'mcp', 'list']
const state = await import('../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home)
const { main } = await import('../../src/main')
const auth = await import('../../src/utils/auth')
const claudeAI = await import('../../src/services/mcp/claudeai')
const storage = await import('../../src/utils/secureStorage/index')
const { macOsKeychainStorage } = await import('../../src/utils/secureStorage/macOsKeychainStorage')
const { keychainCacheState } = await import('../../src/utils/secureStorage/macOsKeychainHelpers')
const { getLegacyApiKeyPrefetchResult } = await import('../../src/utils/secureStorage/keychainPrefetch')
// These observers preserve the actual getter result and readAsync Promise.
// They count logical accesses separately from security(1) process receipts.
let storageGetters = 0
const originalStorage = storage.getSecureStorage
spyOn(storage, 'getSecureStorage').mockImplementation(() => {
  storageGetters++
  return originalStorage()
})
const logicalReads: Array<{ method: 'read' | 'readAsync', cachedAt: number, generation: number, cacheDataNull: boolean }> = []
const beforeLogicalRead = (method: 'read' | 'readAsync') => {
  logicalReads.push({ method, cachedAt: keychainCacheState.cache.cachedAt, generation: keychainCacheState.generation, cacheDataNull: keychainCacheState.cache.data === null })
}
const originalRead = macOsKeychainStorage.read
spyOn(macOsKeychainStorage, 'read').mockImplementation(() => {
  beforeLogicalRead('read')
  return originalRead()
})
const originalReadAsync = macOsKeychainStorage.readAsync
spyOn(macOsKeychainStorage, 'readAsync').mockImplementation(() => {
  beforeLogicalRead('readAsync')
  return originalReadAsync()
})
const originalCatalog = claudeAI.fetchClaudeAIMcpConfigsIfEligible
let catalogBefore: { physical: number, logical: number, getter: number } | undefined
spyOn(claudeAI, 'fetchClaudeAIMcpConfigsIfEligible').mockImplementation(() => {
  catalogBefore = { physical: prefetched.length + syncCalls.filter(call => call.admitted).length, logical: logicalReads.length, getter: storageGetters }
  return originalCatalog() // same original Promise, including memoized result
})
process.on('exit', code => {
  fs.writeFileSync(join(home, 'mcp-auth-startup-source.json'), JSON.stringify({ code, profile: input.profile, prefetched, logicalReads, storageGetters, syncCalls, absentMcpPaths,
    legacyMemoEntries: auth.getApiKeyFromConfigOrMacOSKeychain.cache?.size, legacyPrefetch: getLegacyApiKeyPrefetchResult(),
    keychainCache: { cachedAt: keychainCacheState.cache.cachedAt, generation: keychainCacheState.generation, dataNull: keychainCacheState.cache.data === null },
    catalogBefore, catalogAfter: { physical: prefetched.length + syncCalls.filter(call => call.admitted).length, logical: logicalReads.length, getter: storageGetters }, caps,
    scope: 'v4 whole original main/preAction/initSinks/runMigrations/mcpListHandler/gracefulShutdown; original SecureStorage/fallback/cache and two-prefetch producer. Exact async security execFile and the two actual v3-proven synchronous Keychain read callers are mapped to owned empty subprocesses. All other synchronous primitives remain refused. Logical getter/read accesses are not physical counts. Counts do not assert cold timing equivalence.' }))
})
await main()
