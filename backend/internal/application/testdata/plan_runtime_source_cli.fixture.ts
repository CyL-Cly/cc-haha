import fs from 'node:fs'
import path from 'node:path'
import { mock, spyOn } from 'bun:test'
import { syncBuiltinESMExports } from 'node:module'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const cliArgs = process.argv.slice(2)
const homeInput = process.env.HOME!
const originalCwd = process.cwd()
process.chdir(homeInput)
const input = { home: homeInput, cwd: originalCwd, arguments: [...cliArgs, '--thinking', 'disabled', '--tools', 'EnterPlanMode,ExitPlanMode,Read,Write', '--no-computer-use', '--no-chrome', '--system-prompt', 'Use the actual tools for the private plan restart fixture.'] }
const sdkArgument = cliArgs.indexOf('--sdk-url')
if (sdkArgument < 0) throw new Error('Actual Source SDK launch required')
const sdkURL = new URL(cliArgs[sdkArgument + 1])
if (sdkURL.hostname !== '127.0.0.1' || !sdkURL.pathname.startsWith('/sdk/')) throw new Error('Unowned Source SDK endpoint')
const physicalWebSocket = globalThis.WebSocket
const home = assertDisposableFixtureHome(input.home, 'Actual public agent CLI')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SOURCE_PLAN_RESTART_FIXTURE !== '1') throw new Error('Missing public agent CLI guard')
if (input.cwd !== path.join(home, 'work')) throw new Error('Unowned agent CWD')
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', path.join(path.dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']
for (const suffix of ['.claude/skills', '.claude/agents', '.claude/rules', '.claude/workflows', 'CLAUDE.md']) absent.push(path.join('/Library/Application Support/ClaudeCode', suffix))
for (const start of [home, fs.realpathSync(home)]) {
  for (let directory = path.dirname(start); ; directory = path.dirname(directory)) {
    for (const suffix of ['.git', '.claude/agents', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.agents/commands', '.mcp.json', 'CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/CLAUDE.md', '.claude/AGENTS.md', '.claude/rules']) absent.push(path.join(directory, suffix))
    if (directory === path.dirname(directory)) break
  }
}
const physicalBunSpawn = Bun.spawn.bind(Bun)
const actualHttp = require('node:http')
const actualNet = require('node:net')
const physicalHttpRequest = actualHttp.request
const physicalCreateConnection = actualNet.createConnection
const physicalSocketConnect = actualNet.Socket.prototype.connect
const originalProcesses = require('node:child_process')
const physicalProcesses = Object.fromEntries(['spawn', 'spawnSync', 'execFile', 'exec'].map(name => [name, originalProcesses[name]]))
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: new URL(process.env.ANTHROPIC_BASE_URL!).origin, absentPaths: absent })
globalThis.WebSocket = class extends physicalWebSocket {
  constructor(url: string | URL, protocols?: string | string[], options?: any) {
    if (new URL(String(url)).href !== sdkURL.href) throw new Error('Unowned Source SDK socket')
    super(url, protocols, options)
  }
} as any
if(process.env.CC_HAHA_SOURCE_PLAN_STARTUP_HELD==='1') {
 // Hold only the physical fixture launcher before original CLI startup. The
 // parent closes this actual process while Source startSession awaits SDK.
 await new Promise<void>(resolve=>{const watcher=fs.watch(home,()=>{if(fs.existsSync(path.join(home,'release-original-startup'))){watcher.close();resolve()}})})
}
// Original HTTP hooks use node:http, while inference uses fetch. Grant only
// the owned private hook URL and its one loopback socket, preserving real I/O.
const hookURL = new URL('/agent-hook', process.env.ANTHROPIC_BASE_URL!)
const ownedSocket = (args: any[]) => {
  let value = args[0]
  if (Array.isArray(value)) value = value[0]
  if (typeof value !== 'object' || value === null) return String(args[0]) === hookURL.port && args[1] === '127.0.0.1'
  return String(value.port) === hookURL.port && (value.host ?? value.hostname) === '127.0.0.1' && !value.path
}
actualNet.createConnection = (...args: any[]) => {
  if (!ownedSocket(args)) throw new Error('Unowned hook socket')
  return physicalCreateConnection(...args)
}
actualNet.Socket.prototype.connect = function (...args: any[]) {
  if (!ownedSocket(args)) throw new Error('Unowned hook Socket.connect')
  return physicalSocketConnect.apply(this, args)
}
actualHttp.request = (...args: any[]) => {
  const value = args[0]
  const url = value instanceof URL || typeof value === 'string' ? new URL(value) : new URL(value.path ?? '/', `http://${value.hostname ?? value.host}:${value.port}`)
  if (url.href !== hookURL.href || url.username || url.password) throw new Error('Unowned HTTP hook request')
  return physicalHttpRequest(...args)
}
const subprocesses: any[] = []
const processes = require('node:child_process')
const guardedSpawn = processes.spawn
const privateRG = path.join(home, 'private-rg')
const expectedStartupHook = `/bin/cat >> '${path.join(home, 'actual-start-hooks.jsonl')}'; /usr/bin/printf '\\n' >> '${path.join(home, 'actual-start-hooks.jsonl')}'; /usr/bin/printf '%s' '{}'`
if (input.startHookCommand !== undefined && input.startHookCommand !== expectedStartupHook) throw new Error('Unowned startup hook command')
const privateCommand = (command: string, args: any[], options: any = {}) => {
  const cwd = path.resolve(options.cwd ?? process.cwd())
  if (!cwd.startsWith(home + '/')) return false
  if (input.startHookCommand && command === input.startHookCommand && args.length === 0 && options.shell === true) return true
  if (input.startHookCommand && ['/bin/sh', '/bin/bash'].includes(command) && args.length === 2 && args[0] === '-c' && args[1] === input.startHookCommand) return true
  if (command === privateRG) return args.includes('--version') && args.every(arg => ['--version', '--no-config'].includes(arg)) || typeof args.at(-1) === 'string' && path.resolve(cwd, args.at(-1)).startsWith(home + '/')
  if (command === 'codesign' && args.join(' ') === '-vv -d ' + privateRG) return true
  if (['/usr/bin/uname', 'uname', '/usr/bin/sw_vers', 'sw_vers'].includes(command) && args.every(arg => ['-a', '-r', '-s', '-productVersion'].includes(arg))) return true
  return (command === '/usr/bin/git' || command === 'git') && !options.shell && args.some(arg => ['rev-parse', 'status', 'log', 'diff', 'ls-files'].includes(arg)) || (command === '/usr/bin/git' || command === 'git') && args.join(' ') === 'config user.name'
}
for (const name of ['spawnSync', 'execFile']) {
  const denied = processes[name]
  processes[name] = (...args: any[]) => {
    subprocesses.push({ kind: name, command: args[0], args: args[1] })
    return privateCommand(args[0], args[1], args[2]) ? physicalProcesses[name](...args) : denied(...args)
  }
}
processes.spawn = (...args: any[]) => {
  subprocesses.push({ kind: 'spawn', command: args[0], args: args[1] })
  if (typeof args[0] === 'string' && args[0].startsWith('ps aux | grep -E "Visual Studio Code|Code Helper|Cursor Helper|Windsurf Helper|') && args[0].endsWith('" | grep -v grep') && Array.isArray(args[1]) && args[1].length === 0) {
    const child = new EventEmitter() as any
    child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.stdin = new PassThrough()
    child.stdio = [child.stdin, child.stdout, child.stderr]; child.kill = () => true
    queueMicrotask(() => { child.stdout.end(); child.stderr.end(); child.exitCode = 1; child.emit('exit', 1, null); child.emit('close', 1, null) })
    return child
  }
  if (privateCommand(args[0], args[1], args[2])) return physicalProcesses.spawn(...args)
  return guardedSpawn(...args)
}
Bun.spawn = ((args: any, options: any = {}) => {
  if (!Array.isArray(args)) { options = args; args = options.cmd }
  if (!Array.isArray(args)) throw new Error('Invalid native discovery argument shape')
  subprocesses.push({ kind: 'Bun.spawn', command: args[0], args: args.slice(1) })
  if (!privateCommand(args[0], args.slice(1), options)) throw new Error('Unowned native discovery command')
  return physicalBunSpawn(args, options)
}) as typeof Bun.spawn
const deniedExec = processes.exec
processes.exec = (...args: any[]) => {
  subprocesses.push({ kind: 'exec', command: args[0] })
  if (args[0] === 'ioreg -rd1 -c "IOPlatformExpertDevice"') {
    const callback = args.findLast(value => typeof value === 'function')
    queueMicrotask(() => callback?.(null, '"IOPlatformSerialNumber" = "private-agent-fixture-serial"\n', ''))
    return { kill() {} }
  }
  return deniedExec(...args)
}
const processExports = await import('node:child_process')
const plainProcessExports = await import('child_process')
for (const name of ['spawn', 'spawnSync', 'execFile', 'exec']) {
  const binding = processes[name]
  spyOn(processExports as any, name).mockImplementation(binding)
  spyOn(plainProcessExports as any, name).mockImplementation(binding)
}
syncBuiltinESMExports()
// Only absent external machine policy/keychain primitives are bound. The CLI
// action, agent discovery/selection, permission, Read, QueryEngine and Close run.
mock.module('../../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
;(globalThis as any).MACRO = { VERSION: 'source-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = path.join(home, 'managed')
const { getManagedFilePath } = await import('../../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== path.join(home, 'managed')) throw new Error('Unowned managed root')
process.env.USER_TYPE = 'external'
process.chdir(input.cwd)
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setCwdState(input.cwd); state.setOriginalCwd(input.cwd); state.setProjectRoot(input.cwd)
process.argv = [process.execPath, new URL('../../../../src/entrypoints/cli.tsx', import.meta.url).pathname, ...input.arguments]
let phase = 'before-main-import'
process.on('exit', code => fs.writeFileSync(path.join(home, 'original-plan-cli-' + process.pid + '-receipt.json'), JSON.stringify({ code, subprocesses, capabilities, agentType: state.getMainThreadAgentType(), phase, marks: performance.getEntriesByType('mark').map(mark => mark.name) })))
setTimeout(() => process.exit(94), 35000).unref()
try { const { main } = await import('../../../../src/main'); phase = 'main-called'; await main() }
catch (error) { console.error(error instanceof Error ? `${error.name}: ${error.message}` : String(error)); process.exit(1) }
