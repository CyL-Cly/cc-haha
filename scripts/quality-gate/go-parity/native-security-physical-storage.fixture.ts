import { mock } from 'bun:test'
import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const nativeSync = Bun.spawnSync.bind(Bun), nativeAsync = Bun.spawn.bind(Bun)
const home = assertDisposableFixtureHome(input.home, 'Physical credentials storage')
const canonicalHome = fs.realpathSync(home)
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PHYSICAL_STORAGE_SOURCE !== '1') throw new Error('Pinned private credentials Source required')
const cwd = fs.realpathSync(join(home, 'workspace'))
const bins = ['a', 'b'].map(name => fs.realpathSync(join(home, 'bin-' + name)))
const allowedPaths = [...bins, '../bin-a', '../bin-b', ...bins.map(bin => ':' + bin)]
const statePath = join(home, 'state.json'), tracePath = join(home, 'trace.jsonl')
for (const bin of bins) {
  const executable = fs.realpathSync(join(bin, 'security'))
  if (!executable.startsWith(canonicalHome + '/') || !fs.statSync(executable).isFile() || !(fs.statSync(executable).mode & 0o111)) throw new Error('Exact private security executable required')
}
const allowedAccounts = new Set(['unknown', 'owned-plain-user', 'owned spaced user', 'owned-glob-*', '$OWNED_ACCOUNT', 'owned-nonzero-user', 'owned-path-user'])
process.chdir(cwd)
const caps = await installClientFixtureCapabilities(home)
const execa = await import('execa')
type Options = { input?: string; cwd?: string; env?: NodeJS.ProcessEnv; shell?: boolean }
const primitive = (command: string, argsOrOptions: string[] | Options, finalOptions?: Options) => {
  const args = Array.isArray(argsOrOptions) ? argsOrOptions : []
  const options = Array.isArray(argsOrOptions) ? finalOptions ?? {} : argsOrOptions
  const environment = { ...(options.env ?? process.env) }, directory = options.cwd ?? process.cwd()
  if (fs.realpathSync(directory) !== cwd || environment.HOME !== home || environment.OWNED_SECURITY_STATE !== statePath || environment.OWNED_SECURITY_TRACE !== tracePath || !allowedPaths.includes(environment.PATH ?? '')) throw new Error('Credentials primitive escaped private context')
  if (options.shell) {
    const match = command.match(/^security (find|delete)-generic-password -a "([^"\n]*)" (?:-w )?-s "Claude Code-credentials(?:-[a-f0-9]{8})?"$/)
    if (args.length || !match || !allowedAccounts.has(match[2])) throw new Error('Unowned credentials shell command')
    return { cmd: ['/bin/sh', '-c', command], options, environment, directory }
  }
  if (command !== 'security' || !(args.length === 1 && args[0] === '-i' || args.length === 6 && args[0] === 'find-generic-password' || args.length === 8 && args[0] === 'add-generic-password')) throw new Error('Unowned direct credentials operation')
  if (args[0] === '-i' && !/^add-generic-password -U -a "[^"\n]*" -s "Claude Code-credentials(?:-[a-f0-9]{8})?" -X "[a-f0-9]*"\n$/.test(options.input ?? '')) throw new Error('Unowned credentials stdin')
  return { cmd: [command, ...args], options, environment, directory }
}
mock.module('execa', () => ({ ...execa,
  execaSync(command: string, argsOrOptions: string[] | Options, options?: Options) {
    const request = primitive(command, argsOrOptions, options)
    const result = nativeSync({ cmd: request.cmd, cwd: request.directory, env: request.environment, stdin: request.options.input === undefined ? 'ignore' : Buffer.from(request.options.input), stdout: 'pipe', stderr: 'pipe' })
    return { exitCode: result.exitCode, failed: result.exitCode !== 0, stdout: Buffer.from(result.stdout).toString('utf8'), stderr: Buffer.from(result.stderr).toString('utf8') }
  },
  execa(command: string, argsOrOptions: string[] | Options, options?: Options) {
    const request = primitive(command, argsOrOptions, options)
    const child = nativeAsync({ cmd: request.cmd, cwd: request.directory, env: request.environment, stdin: request.options.input === undefined ? 'ignore' : Buffer.from(request.options.input), stdout: 'pipe', stderr: 'pipe' })
    return Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]).then(([exitCode, stdout, stderr]) => ({ exitCode, failed: exitCode !== 0, stdout, stderr }))
  },
}))
;(globalThis as any).MACRO = { VERSION: '999.0.0-physical-storage', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(cwd)
bootstrap.setCwdState(cwd)
bootstrap.setProjectRoot(cwd)
bootstrap.setIsInteractive(false)
const { macOsKeychainStorage } = await import('../../../src/utils/secureStorage/macOsKeychainStorage')
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const helpers = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
let now = 1700000000000
Date.now = () => now
const rows = []
const store = createFallbackStorage(macOsKeychainStorage, plainTextStorage)
for (const profile of input.rows) {
  if (profile.user === null) delete process.env.USER
  else process.env.USER = profile.user
  process.env.OWNED_ACCOUNT = 'owned-expanded-account'
  process.env.OWNED_FIND_EXIT = String(profile.findExit)
  const paths = profile.pathKind === 'relative' ? ['../bin-a', '../bin-b'] : profile.pathKind === 'leading-empty' ? bins.map(bin => ':' + bin) : bins
  process.env.PATH = paths[0]
  const service = helpers.getMacOsKeychainStorageServiceName(helpers.CREDENTIALS_SERVICE_SUFFIX), account = helpers.getUsername()
  const seeds = { [account + '\u0000' + service]: JSON.stringify({ value: 'literal-account', future: { keep: 41 } }), ['owned-expanded-account\u0000' + service]: JSON.stringify({ value: 'expanded-account', future: { keep: 73 } }) }
  fs.writeFileSync(statePath, JSON.stringify(seeds))
  fs.writeFileSync(tracePath, '')
  helpers.clearKeychainCache()
  now += 31000
  const values: unknown[] = [store.read()]
  process.env.PATH = paths[1]
  helpers.clearKeychainCache()
  values.push(await store.readAsync())
  helpers.clearKeychainCache()
  const update = { value: 'updated', future: { keep: 99 } }
  values.push(store.update(update), store.read())
  values.push(store.delete())
  helpers.clearKeychainCache()
  values.push(store.read(), store.delete())
  rows.push({ name: profile.name, pathKind: profile.pathKind, user: profile.user, findExit: profile.findExit, service, account, seeds, values, trace: fs.readFileSync(tracePath, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)), final: JSON.parse(fs.readFileSync(statePath, 'utf8')) })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Physical credentials Source escaped private boundary ' + JSON.stringify(caps))
await new Promise<void>((accept, reject) => process.stdout.write(JSON.stringify({ rows, caps }), error => error ? reject(error) : accept()))
process.exit(0)
