import { spawn, type ChildProcess } from 'node:child_process'
import { accessSync, constants, realpathSync, statSync } from 'node:fs'
import { request as httpRequest } from 'node:http'
import { homedir } from 'node:os'
import { basename, dirname, isAbsolute, relative, resolve, sep } from 'node:path'
import { Readable } from 'node:stream'
import type { QualityGateSandbox } from './sandbox'

/** An explicit executable and argument vector, never a shell command or fallback. */
export type BackendLaunchTarget = {
  kind: 'typescript' | 'go'
  command: string
  args?: readonly string[]
  cwd?: string
  env?: Readonly<Record<string, string>>
}

export const BACKEND_TARGET_ENV = 'QUALITY_GATE_BACKEND_TARGET_JSON'

const PROTECTED_ENV_NAMES = new Set([
  'HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA',
  'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
  'CODEX_HOME', 'GH_CONFIG_DIR', 'CLAUDE_CONFIG_DIR', 'TEMP', 'TMP', 'TMPDIR',
  'BUN_OPTIONS', 'CC_HAHA_DISABLE_TERMINAL_SHELL_ENV', 'CLAUDE_CLI_PATH',
  'CC_HAHA_ELECTRON_SMOKE_STATE_ROOT', 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG',
  'SERVER_HOST', 'SERVER_PORT', BACKEND_TARGET_ENV,
])
const SANDBOX_PATH_NAMES = [
  'HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA',
  'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
  'CODEX_HOME', 'GH_CONFIG_DIR', 'CLAUDE_CONFIG_DIR', 'TEMP', 'TMP', 'TMPDIR',
]

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function protectedEnv(name: string) {
  return PROTECTED_ENV_NAMES.has(name.toUpperCase()) || name.toUpperCase().startsWith('MOCK_')
}

/** Only this selector is read from the ambient environment; it is never inherited. */
export function resolveBackendLaunchTarget(
  rootDir: string,
  target?: BackendLaunchTarget,
  source: NodeJS.ProcessEnv = process.env,
): BackendLaunchTarget {
  let selected: unknown = target
  if (selected === undefined && source[BACKEND_TARGET_ENV] !== undefined) {
    try {
      selected = JSON.parse(source[BACKEND_TARGET_ENV]!)
    } catch {
      throw new Error(`${BACKEND_TARGET_ENV} must contain a valid backend target JSON object`)
    }
  }
  if (selected === undefined) {
    selected = {
      kind: 'typescript',
      command: process.execPath,
      args: ['--no-env-file', 'run', resolve(rootDir, 'src/server/index.ts')],
    }
  }
  if (!object(selected) || Object.keys(selected).some(key => !['kind', 'command', 'args', 'cwd', 'env'].includes(key))) {
    throw new Error('Backend target must be an object containing kind, command, and optional args/cwd/env')
  }
  if (selected.kind !== 'typescript' && selected.kind !== 'go') throw new Error('Backend target kind must be typescript or go')
  if (typeof selected.command !== 'string' || !isAbsolute(selected.command) || selected.command.includes('\0')) {
    throw new Error('Backend target command must be an absolute executable path')
  }
  if (selected.args !== undefined && (!Array.isArray(selected.args) || selected.args.some(arg => typeof arg !== 'string' || arg.includes('\0')))) {
    throw new Error('Backend target args must be an array of strings')
  }
  const args = [...((selected.args as string[] | undefined) ?? [])]
  if (args.some(arg => /^--(?:host|port)(?:=|$)/.test(arg))) {
    throw new Error('Backend host and port are owned by the sandbox runner')
  }
  if (selected.cwd !== undefined && (typeof selected.cwd !== 'string' || selected.cwd.includes('\0'))) {
    throw new Error('Backend target cwd must be a directory path')
  }
  const cwd = resolve(rootDir, (selected.cwd as string | undefined) ?? '.')
  if (!statSync(cwd).isDirectory()) throw new Error(`Backend target cwd is not a directory: ${cwd}`)
  if (!statSync(selected.command).isFile()) throw new Error(`Backend target command is not a file: ${selected.command}`)
  accessSync(selected.command, constants.X_OK)
  const env: Record<string, string> = {}
  if (selected.env !== undefined) {
    if (!object(selected.env)) throw new Error('Backend target env must be an object of strings')
    const names = new Set<string>()
    for (const [name, value] of Object.entries(selected.env)) {
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || typeof value !== 'string' || value.includes('\0')) {
        throw new Error('Backend target env must contain valid names and string values')
      }
      if (protectedEnv(name)) throw new Error(`Backend target cannot override sandbox variable ${name}`)
      if (names.has(name.toUpperCase())) throw new Error(`Backend target env repeats variable ${name}`)
      names.add(name.toUpperCase())
      env[name] = value
    }
  }
  return { kind: selected.kind, command: selected.command, args, cwd, env }
}

function isWithin(root: string, candidate: string) {
  const path = relative(root, candidate)
  return path !== '..' && !path.startsWith(`..${sep}`) && !isAbsolute(path)
}

function canonicalPath(path: string): string {
  try {
    return realpathSync(path)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
    return resolve(canonicalPath(dirname(path)), basename(path))
  }
}

/** Validate the launch boundary without reading any real user configuration. */
function validateSandbox(sandbox: QualityGateSandbox, env: Readonly<Record<string, string>>) {
  const home = realpathSync(sandbox.home)
  if (home === realpathSync(homedir())) throw new Error('Backend sandbox cannot use the real user home')
  if (!isWithin(home, realpathSync(sandbox.configDir)) || home === realpathSync(sandbox.configDir)) {
    throw new Error('Backend config must be inside the sandbox home')
  }
  for (const name of SANDBOX_PATH_NAMES) {
    const value = env[name]
    if (!value || value !== sandbox.env[name] || !isAbsolute(value) || !isWithin(home, canonicalPath(value))) {
      throw new Error(`Backend sandbox variable ${name} escaped its isolated environment`)
    }
  }
  if (env.HOME !== sandbox.home || env.CLAUDE_CONFIG_DIR !== sandbox.configDir) {
    throw new Error('Backend home/config must match the guarded sandbox')
  }
  for (const [name, value] of Object.entries(env)) {
    if (protectedEnv(name) && value !== sandbox.env[name]) throw new Error(`Backend launch cannot change sandbox variable ${name}`)
  }
}

export type BackendServer = {
  target: BackendLaunchTarget
  stdout: ReadableStream<Uint8Array>
  stderr: ReadableStream<Uint8Array>
  exited: Promise<number>
  waitUntilHealthy(timeoutMs?: number): Promise<void>
  stop(graceMs?: number): Promise<void>
}

function waitForExit(exited: Promise<number>, timeoutMs: number): Promise<boolean> {
  return new Promise(resolveWait => {
    const timer = setTimeout(() => resolveWait(false), timeoutMs)
    void exited.then(() => { clearTimeout(timer); resolveWait(true) }, () => { clearTimeout(timer); resolveWait(true) })
  })
}

function healthStatus(url: string, signal: AbortSignal): Promise<number> {
  // node:http uses a direct loopback socket, independent of ambient HTTP proxies.
  return new Promise((resolveStatus, reject) => {
    const removeAbort = () => signal.removeEventListener('abort', aborted)
    const request = httpRequest(url, { signal }, response => {
      removeAbort()
      resolveStatus(response.statusCode ?? 0)
      response.destroy()
    })
    const aborted = () => {
      request.destroy()
      removeAbort()
      reject(new Error('Backend health request aborted'))
    }
    request.once('error', error => { removeAbort(); reject(error) })
    if (signal.aborted) aborted()
    else {
      signal.addEventListener('abort', aborted, { once: true })
      request.end()
    }
  })
}

/** Spawn only the selected backend, with the already guarded lane environment. */
export function spawnBackendServer(options: {
  rootDir: string
  port: number
  sandbox: QualityGateSandbox
  env?: Readonly<Record<string, string>>
  target?: BackendLaunchTarget
}): BackendServer {
  if (!Number.isInteger(options.port) || options.port < 1 || options.port > 65535) throw new Error('Invalid backend port')
  const target = resolveBackendLaunchTarget(options.rootDir, options.target)
  const baseEnv = options.env ?? options.sandbox.env
  validateSandbox(options.sandbox, baseEnv)
  const childEnv: Record<string, string> = { ...baseEnv, ...target.env, SERVER_HOST: '127.0.0.1', SERVER_PORT: String(options.port) }
  if (target.kind === 'go') {
    // Even a loopback provider can probe cached OS accounts during model setup.
    // Reuse the native credential-I/O seam without changing tools or auth files.
    const credentialRoot = realpathSync(options.sandbox.home)
    for (const name of Object.keys(childEnv)) {
      if (name.toUpperCase() === 'CC_HAHA_ELECTRON_SMOKE_STATE_ROOT' || name.toUpperCase() === 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG') {
        delete childEnv[name]
      }
    }
    childEnv.CC_HAHA_ELECTRON_SMOKE_STATE_ROOT = credentialRoot
    childEnv.CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG = resolve(credentialRoot, 'native-credential-smoke.jsonl')
  }
  const child = spawn(target.command, [...(target.args ?? []), '--host', '127.0.0.1', '--port', String(options.port)], {
    cwd: target.cwd,
    env: childEnv,
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    detached: process.platform !== 'win32',
  })
  let finished = false
  const exited = new Promise<number>((resolveExit, reject) => {
    child.once('error', error => { finished = true; reject(error) })
    child.once('exit', code => { finished = true; resolveExit(code ?? -1) })
  })
  void exited.catch(() => {})
  let stopping: Promise<void> | undefined
  return {
    target,
    stdout: Readable.toWeb(child.stdout!) as ReadableStream<Uint8Array>,
    stderr: Readable.toWeb(child.stderr!) as ReadableStream<Uint8Array>,
    exited,
    async waitUntilHealthy(timeoutMs = 60_000) {
      const abort = new AbortController()
      const url = `http://127.0.0.1:${options.port}/health`
      let timer: ReturnType<typeof setTimeout>
      let lastError = ''
      const timeout = new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          abort.abort()
          reject(new Error(`Timed out waiting for ${target.kind} backend health at ${url}: ${lastError}`))
        }, timeoutMs)
      })
      const poll = async () => {
        while (!abort.signal.aborted && !finished) {
          try {
            const status = await healthStatus(url, abort.signal)
            if (status >= 200 && status < 300 && !finished) return
            lastError = `HTTP ${status}`
          } catch (error) {
            lastError = error instanceof Error ? error.message : String(error)
          }
          await Bun.sleep(50)
        }
        throw new Error(`Timed out waiting for ${target.kind} backend health at ${url}: ${lastError}`)
      }
      try {
        await Promise.race([
          poll(),
          timeout,
          exited.then(code => { throw new Error(`${target.kind} backend exited before health readiness (exit ${code})`) }),
        ])
      } finally {
        clearTimeout(timer!)
        abort.abort()
      }
    },
    stop(graceMs = 6_000) {
      stopping ??= (async () => {
        if (!finished) {
          if (process.platform === 'win32') await signalBackend(child, 'SIGTERM', childEnv)
          else child.kill('SIGTERM')
          await waitForExit(exited, graceMs)
        }
        // A crashed/exited leader can leave live descendants. The owned process
        // group remains ours, so reap it even when the leader already exited.
        await signalBackend(child, 'SIGKILL', childEnv)
        if (!await waitForExit(exited, 2_000)) throw new Error(`${target.kind} backend did not exit after forced shutdown`)
      })()
      return stopping
    },
  }
}

async function signalBackend(child: ChildProcess, signal: 'SIGTERM' | 'SIGKILL', env: Record<string, string>) {
  if (!child.pid) return
  if (process.platform === 'win32') {
    const root = env.SYSTEMROOT ?? env.SystemRoot ?? env.WINDIR
    const command = root ? resolve(root, 'System32', 'taskkill.exe') : 'taskkill.exe'
    await new Promise<void>((resolveKill, reject) => {
      const killer = spawn(command, ['/T', '/F', '/PID', String(child.pid)], { env, stdio: 'ignore', windowsHide: true })
      killer.once('error', reject)
      killer.once('exit', code => code === 0 ? resolveKill() : reject(new Error(`taskkill exited ${code}`)))
    }).catch(() => { child.kill(signal) })
    return
  }
  try {
    // Every backend is the leader of its own group; reap its workers too.
    process.kill(-child.pid, signal)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ESRCH') child.kill(signal)
  }
}
