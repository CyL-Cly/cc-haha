import { mock, spyOn } from 'bun:test'
import fs from 'node:fs'
import * as fsExports from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'
import { dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

// This fixture runs real auth/client code. Replace only the external credential
// store and deny process/network capabilities before importing that code.
export function absentManagedSettingsFixturePaths(): string[] {
  const directory = process.platform === 'darwin' ? '/Library/Application Support/ClaudeCode'
    : process.platform === 'win32' ? 'C:\\Program Files\\ClaudeCode' : '/etc/claude-code'
  return [join(directory, 'managed-settings.json'), join(directory, 'managed-settings.d')]
}

export async function installClientFixtureCapabilities(home: string, options: { loopbackFetchOrigin?: string; absentPaths?: string[] } = {}) {
  const root = resolve(fileURLToPath(new URL('../../../', import.meta.url)))
  const canonicalHome = fs.realpathSync(home), canonicalRoot = fs.realpathSync(root)
  const blocked: string[] = [], secureReads: string[] = [], absentReads: Array<{ operation: string; path: string }> = []
  const absent = new Set((options.absentPaths ?? []).map(path => resolve(path)))
  // Numeric descriptors are capabilities only when this guard opened their
  // path. Closing removes provenance, including when the OS reuses that fd.
  const descriptors = new Map<number, { writable: boolean }>()
  const realpath = fs.realpathSync, lstat = fs.lstatSync
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
  for (const name of ['readFile', 'writeFile', 'mkdir', 'rm', 'rename', 'copyFile', 'open', 'appendFile', 'readdir', 'stat', 'lstat', 'access', 'realpath', 'unlink', 'chmod']) {
    const original = (fs.promises as any)[name]
    const write = ['writeFile', 'mkdir', 'rm', 'rename', 'copyFile', 'appendFile', 'unlink', 'chmod'].includes(name)
    ;(fs.promises as any)[name] = async (...args: any[]) => {
      const writing = name === 'open' ? !['r', 0].includes(args[1]) : write
      isAbsentRead(args[0], name, writing)
      if (!inside(args[0], writing) || ['rename', 'copyFile'].includes(name) && !inside(args[1], true)) return deny('filesystem:' + name + ':' + String(args[0]))
      return original(...args)
    }
  }
  const processes = require('node:child_process')
  const processExports = await import('node:child_process')
  for (const name of ['exec', 'execFile', 'spawn', 'execSync', 'execFileSync', 'spawnSync', 'fork']) {
    const unavailable = () => deny('subprocess:' + name)
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
  for (const name of ['spawn', 'spawnSync', 'connect', 'listen', 'serve', 'udpSocket', 'dlopen']) (Bun as any)[name] = () => deny('native:' + name)
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
