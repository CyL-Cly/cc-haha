import { mock, spyOn } from 'bun:test'
import fs from 'node:fs'
import * as fsExports from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'
import { basename, dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

// This fixture runs real auth/client code. Replace only the external credential
// store and deny process/network capabilities before importing that code.
export function absentManagedSettingsFixturePaths(): string[] {
  const directory = process.platform === 'darwin' ? '/Library/Application Support/ClaudeCode'
    : process.platform === 'win32' ? 'C:\\Program Files\\ClaudeCode' : '/etc/claude-code'
  return [join(directory, 'managed-settings.json'), join(directory, 'managed-settings.d')]
}

export type ApprovedFixtureSpawn = { command: string; args: readonly string[]; options: import('node:child_process').SpawnOptions }

export async function installClientFixtureCapabilities(home: string, options: {
  loopbackFetchOrigin?: string
  absentPaths?: string[]
  readonlyExternalFiles?: readonly string[]
  privateExecutableFiles?: readonly string[]
  approvedSpawn?: (request: ApprovedFixtureSpawn) => boolean
} = {}) {
  const root = resolve(fileURLToPath(new URL('../../../', import.meta.url)))
  const canonicalHome = fs.realpathSync(home), canonicalRoot = fs.realpathSync(root)
  const blocked: string[] = [], secureReads: string[] = [], absentReads: Array<{ operation: string; path: string }> = []
  const absent = new Set((options.absentPaths ?? []).map(path => resolve(path)))
  // Numeric descriptors are capabilities only when this guard opened their
  // path. Closing removes provenance, including when the OS reuses that fd.
  const descriptors = new Map<number, { writable: boolean }>()
  let approvedNativeSpawnDepth = 0
  const ownedPowerShellPids = new Set<number>()
  const realpath = fs.realpathSync, lstat = fs.lstatSync
  // Native PowerShell differential tests can read exactly the installed binary.
  // Neither its parent directory nor writable access follows this capability.
  const external = new Map<string, string>()
  const privateExecutables = new Map<string, string>()
  for (const value of options.privateExecutableFiles ?? []) {
    const path = resolve(value), canonical = realpath(path)
    if (!canonical.startsWith(canonicalHome + sep) || !fs.statSync(canonical).isFile() || (fs.statSync(canonical).mode & 0o111) === 0) throw new Error('Fixture executable capability requires an exact executable inside its disposable home')
    external.set(path, canonical)
    external.set(canonical, canonical)
    privateExecutables.set(basename(path), canonical)
  }
  for (const value of options.readonlyExternalFiles ?? []) {
    const path = resolve(value), canonical = realpath(path)
    if (!['pwsh', 'pwsh.exe', 'powershell', 'powershell.exe'].includes(basename(canonical).toLowerCase()) || !fs.statSync(canonical).isFile()) throw new Error('Fixture external file capability requires an exact PowerShell executable')
    external.set(path, canonical)
    external.set(canonical, canonical)
  }
  const fetchOriginal = globalThis.fetch.bind(globalThis)
  if (options.loopbackFetchOrigin) {
    const origin = new URL(options.loopbackFetchOrigin)
    if (!['http:', 'https:'].includes(origin.protocol) || origin.hostname !== '127.0.0.1' || !origin.port || origin.port === '0' || origin.origin !== options.loopbackFetchOrigin) throw new Error('Fixture fetch capability requires an explicit HTTP(S) loopback origin')
  }
  const deny = (operation: string): never => {
    blocked.push(operation)
    throw new Error(`Client options fixture blocked ${operation}`)
  }
  const isAbsentRead = (value: unknown, operation: string, write: boolean) => {
    if (write || typeof value !== 'string' || !absent.has(resolve(value))) return false
    const path = resolve(value)
    absentReads.push({ operation, path })
    if (operation === 'existsSync') return true
    const error = new Error(`ENOENT: no such file or directory, ${operation} '${path}'`) as NodeJS.ErrnoException
    error.code = 'ENOENT'
    error.path = path
    error.syscall = operation
    throw error
  }
  const inside = (value: unknown, write = false) => {
    if (typeof value === 'number') return value >= 0 && value <= 2 || descriptors.has(value) && (!write || descriptors.get(value)!.writable)
    if (value instanceof URL) value = fileURLToPath(value)
    if (Buffer.isBuffer(value)) value = value.toString()
    if (typeof value !== 'string') return false
    const path = resolve(value)
    if (!write && external.has(path)) {
      try { return realpath(path) === external.get(path) } catch { return false }
    }
    const under = (candidate: string, directory: string) => candidate === directory || candidate.startsWith(directory + sep)
    if (!(under(path, home) || under(path, canonicalHome) || !write && (under(path, root) || under(path, canonicalRoot)))) return false
    // Resolve the nearest existing ancestor for new writes, and the leaf for
    // reads. A dangling symlink is denied instead of falling back to its parent.
    let candidate = path
    for (;;) {
      try {
        lstat(candidate)
      } catch (error) {
        if (!['ENOENT', 'ENOTDIR'].includes((error as NodeJS.ErrnoException).code ?? '') || dirname(candidate) === candidate) return false
        candidate = dirname(candidate)
        continue
      }
      try {
        const canonical = realpath(candidate)
        return under(canonical, canonicalHome) || !write && under(canonical, canonicalRoot)
      } catch { return false }
    }
  }
  for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'realpathSync', 'readlinkSync', 'accessSync', 'openSync', 'existsSync', 'writeFileSync', 'appendFileSync', 'mkdirSync', 'rmSync', 'unlinkSync', 'renameSync', 'copyFileSync', 'chmodSync']) {
    const original = (fs as any)[name]
    const write = ['writeFileSync', 'appendFileSync', 'mkdirSync', 'rmSync', 'unlinkSync', 'renameSync', 'copyFileSync', 'chmodSync'].includes(name)
    const wrapper: any = (...args: any[]) => {
      const writing = name === 'openSync' ? ![undefined, 'r', 0].includes(args[1]) : write
      if (isAbsentRead(args[0], name, writing)) return false
      if (!inside(args[0], writing) || ['renameSync', 'copyFileSync'].includes(name) && !inside(args[1], true)) return deny('filesystem:' + name + ':' + String(args[0]))
      const result = original(...args)
      if (name === 'openSync') descriptors.set(result, { writable: writing })
      return result
    }
    if (original.native) wrapper.native = wrapper
    ;(fs as any)[name] = wrapper
    spyOn(fsExports as any, name).mockImplementation(wrapper)
  }
  for (const name of ['readSync', 'readvSync', 'writeSync', 'writevSync', 'fstatSync', 'fsyncSync', 'fdatasyncSync', 'ftruncateSync', 'fchmodSync', 'closeSync']) {
    const original = (fs as any)[name]
    if (!original) continue
    const writing = ['writeSync', 'writevSync', 'fsyncSync', 'fdatasyncSync', 'ftruncateSync', 'fchmodSync'].includes(name)
    const wrapper: any = (...args: any[]) => {
      if (!inside(args[0], writing)) return deny('filesystem:' + name + ':' + String(args[0]))
      try { return original(...args) } finally { if (name === 'closeSync') descriptors.delete(args[0]) }
    }
    ;(fs as any)[name] = wrapper
    spyOn(fsExports as any, name).mockImplementation(wrapper)
  }
  const promiseExports = await import('node:fs/promises')
  for (const name of ['readFile', 'writeFile', 'mkdir', 'rm', 'rename', 'copyFile', 'open', 'appendFile', 'readdir', 'stat', 'lstat', 'access', 'realpath', 'unlink', 'chmod']) {
    const original = (fs.promises as any)[name]
    const write = ['writeFile', 'mkdir', 'rm', 'rename', 'copyFile', 'appendFile', 'unlink', 'chmod'].includes(name)
    const wrapper = async (...args: any[]) => {
      const writing = name === 'open' ? !['r', 0].includes(args[1]) : write
      isAbsentRead(args[0], name, writing)
      if (!inside(args[0], writing) || ['rename', 'copyFile'].includes(name) && !inside(args[1], true)) return deny('filesystem:' + name + ':' + String(args[0]))
      const result = await original(...args)
      if (name === 'open') {
        descriptors.set(result.fd, { writable: writing })
        const close = result.close.bind(result), fd = result.fd
        result.close = async () => { try { return await close() } finally { descriptors.delete(fd) } }
      }
      return result
    }
    ;(fs.promises as any)[name] = wrapper
    spyOn(promiseExports as any, name).mockImplementation(wrapper)
  }
  const processes = require('node:child_process')
  const processExports = await import('node:child_process')
  for (const name of ['exec', 'execFile', 'spawn', 'execSync', 'execFileSync', 'spawnSync', 'fork']) {
    const original = processes[name]
    const unavailable = (...args: any[]) => {
      if (name !== 'spawn' || !options.approvedSpawn) return deny('subprocess:' + name)
      let [command, argv, spawnOptions] = args
      const pgrep = (command === '/usr/bin/pgrep' || command === 'pgrep' && process.env.PATH === '/usr/bin:/bin') && Array.isArray(argv) && argv.length === 2 && argv[0] === '-P' && /^\d+$/.test(argv[1]) && ownedPowerShellPids.has(Number(argv[1]))
      if (pgrep) argv = ['-P', String(argv[1])]
      if (pgrep && spawnOptions === undefined) {
        // tree-kill's Source call inherits this private fixture environment.
        // Bind that same environment explicitly at the native boundary.
        command = '/usr/bin/pgrep'
        spawnOptions = { cwd: process.cwd(), env: { ...process.env }, stdio: ['pipe', 'pipe', 'pipe'], detached: false }
        args = [command, argv, spawnOptions]
      }
      const env = spawnOptions?.env, cwd = spawnOptions?.cwd
      // Approved native calls must still use the disposable process boundary.
      // Inherited env, shell:true and unknown/untracked output fds are denied.
      const privatePath = (value: unknown) => typeof value === 'string' && inside(value, true) && (resolve(value).startsWith(resolve(home) + sep) || resolve(value).startsWith(canonicalHome + sep))
      const privateHome = (value: unknown) => { try { return typeof value === 'string' && realpath(resolve(value)) === canonicalHome } catch { return false } }
      const privateExecutable = typeof command === 'string' && basename(command) === command && privateExecutables.has(command) && inside(privateExecutables.get(command)) && typeof env?.PATH === 'string' && resolve(env.PATH.split(process.platform === 'win32' ? ';' : ':')[0], command) === privateExecutables.get(command)
      const executable = typeof command === 'string' && (external.has(resolve(command)) && inside(command) || privateExecutable)
      const guardedPowerShell = command === '/bin/sh' && Array.isArray(argv) && argv[0] === '-c' && argv[2] === 'cc-haha-shell-lifetime' && argv[3] === String(process.pid) && typeof argv[4] === 'string' && external.has(resolve(argv[4])) && inside(argv[4])
      const stdio = spawnOptions?.stdio
      const trackedStdio = Array.isArray(stdio) && stdio.length === 3 && stdio.every((value: any, index: number) => value === 'pipe' || value === 'ignore' || typeof value === 'number' && descriptors.has(value) && (index === 0 || descriptors.get(value)!.writable))
      if ((!executable && !guardedPowerShell && !pgrep) || !Array.isArray(argv) || !argv.every((value: any) => typeof value === 'string') || !spawnOptions || spawnOptions.shell || spawnOptions.uid !== undefined || spawnOptions.gid !== undefined) return deny('subprocess:spawn:executable')
      if (!privatePath(cwd) && !(typeof cwd === 'string' && privateHome(cwd)) || !env || !privateHome(env.HOME) || !privateHome(env.USERPROFILE) || !privatePath(env.CLAUDE_CONFIG_DIR) || ['TMPDIR', 'TMP', 'TEMP', 'CLAUDE_CODE_TMPDIR'].some(key => env[key] !== undefined && !privatePath(env[key]))) return deny('subprocess:spawn:private-boundary')
      if (!trackedStdio) return deny('subprocess:spawn:descriptor')
      if (!options.approvedSpawn({ command, args: argv, options: spawnOptions })) return deny('subprocess:spawn:approval')
      approvedNativeSpawnDepth++
      try {
        const child = original(...args)
        if (!pgrep) {
          if (typeof child.pid === 'number') ownedPowerShellPids.add(child.pid)
          child.once('spawn', () => { if (typeof child.pid === 'number') ownedPowerShellPids.add(child.pid) })
          child.once('close', () => { if (typeof child.pid === 'number') ownedPowerShellPids.delete(child.pid) })
        }
        return child
      } finally { approvedNativeSpawnDepth-- }
    }
    processes[name] = unavailable
    spyOn(processExports as any, name).mockImplementation(unavailable)
  }
  for (const namespace of ['node:net', 'node:tls', 'node:http', 'node:https', 'node:dgram']) {
    const module = require(namespace)
    for (const name of ['connect', 'createConnection', 'request', 'get', 'createServer', 'createSocket']) if (module[name]) module[name] = () => deny(`network:${namespace}:${name}`)
    if (module.Socket) module.Socket.prototype.connect = () => deny(`network:${namespace}:socket-connect`)
  }
  globalThis.fetch = ((input: any, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input))
    if (!options.loopbackFetchOrigin || url.origin !== options.loopbackFetchOrigin || url.username || url.password) return deny('network:fetch')
    // The trusted in-process fixture server never redirects. Refuse redirect
    // following so its origin grant cannot turn into a public-network request.
    return fetchOriginal(input, { ...init, redirect: 'error' })
  }) as typeof fetch
  globalThis.WebSocket = class { constructor() { deny('network:websocket') } } as any
  for (const name of ['spawn', 'spawnSync', 'connect', 'listen', 'serve', 'udpSocket', 'dlopen']) {
    const original = typeof (Bun as any)[name] === 'function' ? (Bun as any)[name].bind(Bun) : undefined
    ;(Bun as any)[name] = (...args: any[]) => name === 'spawn' && approvedNativeSpawnDepth > 0 ? original(...args) : deny('native:' + name)
  }
  const bunFile = Bun.file.bind(Bun), bunWrite = Bun.write.bind(Bun)
  Bun.file = ((value: any, ...args: any[]) => inside(value) ? (bunFile as any)(value, ...args) : deny('filesystem:Bun.file')) as typeof Bun.file
  Bun.write = ((value: any, ...args: any[]) => inside(value, true) ? (bunWrite as any)(value, ...args) : deny('filesystem:Bun.write')) as typeof Bun.write
  syncBuiltinESMExports()
  mock.module(resolve(root, 'src/utils/secureStorage/index.ts'), () => ({ getSecureStorage: () => ({
    name: 'fixture-external-empty-credentials',
    read: () => { secureReads.push('read'); return null },
    readAsync: async () => { secureReads.push('readAsync'); return null },
    update: () => deny('secure-storage:update'), delete: () => deny('secure-storage:delete'),
  }) }))
  return { blocked, secureReads, absentReads }
}
