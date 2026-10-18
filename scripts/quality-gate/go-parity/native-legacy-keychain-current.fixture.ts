import { mock } from 'bun:test'
import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const nativeSpawnSync = Bun.spawnSync.bind(Bun), nativeSpawn = Bun.spawn.bind(Bun)
const home = assertDisposableFixtureHome(input.home, 'Legacy Keychain actual current identity')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LEGACY_KEYCHAIN_CURRENT_SOURCE !== '1') throw new Error('Pinned private legacy Source required')
const cwd = fs.realpathSync(join(home, 'workspace'))
const executable = fs.realpathSync(join(home, 'bin', 'security'))
const statePath = join(home, 'state.json'), tracePath = join(home, 'trace.jsonl')
if (!fs.statSync(executable).isFile() || !(fs.statSync(executable).mode & 0o111)) throw new Error('Owned security executable required')
process.chdir(cwd)
const caps = await installClientFixtureCapabilities(home)
const execa = await import('execa')
type PrimitiveOptions = { input?: string; cwd?: string; env?: NodeJS.ProcessEnv; shell?: boolean }
const primitive = (command: string, argsOrOptions: string[] | PrimitiveOptions, finalOptions?: PrimitiveOptions) => {
  const args = Array.isArray(argsOrOptions) ? argsOrOptions : []
  const options = Array.isArray(argsOrOptions) ? finalOptions ?? {} : argsOrOptions
  const directory = options.cwd ?? process.cwd()
  const environment = { ...(options.env ?? process.env) }
  if (fs.realpathSync(directory) !== cwd || environment.HOME !== home || environment.OWNED_SECURITY_STATE !== statePath || environment.OWNED_SECURITY_TRACE !== tracePath || environment.PATH !== join(home, 'bin') + ':/usr/bin:/bin') throw new Error('Legacy primitive escaped its exact private process boundary')
  if (options.shell) {
    if (args.length || !/^security (find|delete)-generic-password -a \$USER (?:-w )?-s "Claude Code(?:-[a-f0-9]{8})?"$/.test(command)) throw new Error('Unowned legacy shell command')
    return { executable: '/bin/sh', args: ['-c', command], options, directory, environment }
  }
  if (command !== 'security' || args.length !== 1 || args[0] !== '-i' || !/^add-generic-password -U -a "[^"\n]*" -s "Claude Code(?:-[a-f0-9]{8})?" -X "[a-f0-9]*"\n$/.test(options.input ?? '')) throw new Error('Unowned legacy stdin writer')
  return { executable, args, options, directory, environment }
}
mock.module('execa', () => ({ ...execa,
  execaSync(command: string, argsOrOptions: string[] | PrimitiveOptions, options?: PrimitiveOptions) {
    const request = primitive(command, argsOrOptions, options)
    const result = nativeSpawnSync({ cmd: [request.executable, ...request.args], cwd: request.directory, env: request.environment, stdin: request.options.input === undefined ? 'ignore' : Buffer.from(request.options.input), stdout: 'pipe', stderr: 'pipe' })
    return { exitCode: result.exitCode, stdout: Buffer.from(result.stdout).toString('utf8'), stderr: Buffer.from(result.stderr).toString('utf8') }
  },
  execa(command: string, argsOrOptions: string[] | PrimitiveOptions, options?: PrimitiveOptions) {
    const request = primitive(command, argsOrOptions, options)
    const child = nativeSpawn({ cmd: [request.executable, ...request.args], cwd: request.directory, env: request.environment, stdin: request.options.input === undefined ? 'ignore' : Buffer.from(request.options.input), stdout: 'pipe', stderr: 'pipe' })
    return Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]).then(([exitCode, stdout, stderr]) => ({ exitCode, stdout, stderr }))
  },
}))
;(globalThis as any).MACRO = { VERSION: '999.0.0-legacy-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(cwd)
bootstrap.setCwdState(cwd)
bootstrap.setProjectRoot(cwd)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
const { getMacOsKeychainStorageServiceName, getUsername } = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
const auth = await import('../../../src/utils/auth')
const portable = await import('../../../src/utils/authPortable')
const rows = []
const fallback = 'owned-global-fallback'
config.enableConfigs()
for (const [index, profile] of input.rows.entries()) {
  const initial = join(home, 'initial-' + index)
  fs.mkdirSync(initial, { recursive: true })
  process.env.CLAUDE_CONFIG_DIR = initial
  process.env.USER = 'owned-old-account'
  const initialService = getMacOsKeychainStorageServiceName()
  if (profile.directory === null) delete process.env.CLAUDE_CONFIG_DIR
  else process.env.CLAUDE_CONFIG_DIR = profile.directory
  if (profile.user === null) delete process.env.USER
  else process.env.USER = profile.user
  const targetService = getMacOsKeychainStorageServiceName(), targetAccount = getUsername()
  const seeds = { ['owned-old-account\u0000' + initialService]: 'owned-old-key', [targetAccount + '\u0000' + targetService]: 'owned-target-key' }
  fs.writeFileSync(statePath, JSON.stringify(seeds))
  fs.writeFileSync(tracePath, '')
  // Global-file memoization is separately proved. Seed the one real Source
  // file before first access; no getGlobalConfig business mock is used here.
  if (index === 0) fs.writeFileSync(join(initial, '.claude.json'), JSON.stringify({ primaryApiKey: fallback, futureGlobal: { keep: 41 } }))
  const read = () => { auth.getApiKeyFromConfigOrMacOSKeychain.cache.clear(); return auth.getApiKeyFromConfigOrMacOSKeychain()?.key ?? null }
  process.env.CLAUDE_CONFIG_DIR = initial
  process.env.USER = 'owned-old-account'
  if (index === 0) config.getGlobalConfig()
  const values: unknown[] = [read()]
  if (profile.directory === null) delete process.env.CLAUDE_CONFIG_DIR
  else process.env.CLAUDE_CONFIG_DIR = profile.directory
  if (profile.user === null) delete process.env.USER
  else process.env.USER = profile.user
  values.push(read())
  const key = 'owned-saved-key-12345678901234567890'
  await auth.saveApiKey(key)
  values.push(read())
  let removed: string | null = null
  try { await portable.maybeRemoveApiKeyFromMacOSKeychainThrows() } catch (error) { removed = (error as Error).message }
  values.push(removed, read())
  rows.push({ name: profile.name, initial, target: profile.directory, user: profile.user, initialService, targetService, targetAccount, seeds, key, fallback: config.getGlobalConfig().primaryApiKey ?? null, values, trace: fs.readFileSync(tracePath, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)), final: JSON.parse(fs.readFileSync(statePath, 'utf8')) })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Legacy Source escaped private primitives ' + JSON.stringify(caps))
await new Promise<void>((accept, reject) => process.stdout.write(JSON.stringify({ rows, caps }), error => error ? reject(error) : accept()))
process.exit(0)
