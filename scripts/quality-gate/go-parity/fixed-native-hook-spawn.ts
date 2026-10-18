import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import { sep } from 'node:path'
import { mock } from 'bun:test'

// Capture primitives before the common deny guard. The returned adapter must
// be installed only after that guard. No asynchronous dynamic grant is used.
export function captureFixedHookSpawn() {
  const require = createRequire(import.meta.url)
  const processes = require('node:child_process')
  const nativeSpawn = processes.spawn
  const nativeBunSpawn = Bun.spawn
  return (options: { home: string; cwd: string; effects: string; corpusPath: string; corpusSHA256: string; roots: string[]; onPhysicalStdout?: (text: string) => void }) => {
    const guardedSpawn = processes.spawn
    const home = fs.realpathSync(options.home), cwd = fs.realpathSync(options.cwd), effects = fs.realpathSync(options.effects)
    const privatePath = (value: string) => value.startsWith(home + sep) && fs.realpathSync(value).startsWith(home + sep)
    if (![cwd, effects, options.corpusPath].every(privatePath)) throw new Error('Fixed native hook capability escaped owned home')
    const stat = fs.lstatSync(options.corpusPath)
    const bytes = fs.readFileSync(options.corpusPath)
    if (!stat.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077) || stat.uid !== process.getuid!() || createHash('sha256').update(bytes).digest('hex') !== options.corpusSHA256) throw new Error('Fixed hook corpus identity mismatch')
    const parsed = JSON.parse(bytes.toString('utf8'))
    if (!parsed || Array.isArray(parsed) || Object.values(parsed).some(value => typeof value !== 'string' || !value)) throw new Error('Invalid fixed command corpus')
    const commands = new Set(Object.values(parsed))
    const roots = new Set(options.roots.map(root => fs.realpathSync(root)))
    if ([...roots].some(root => !privatePath(root))) throw new Error('Unowned skill root')
    const rows: any[] = [], rejectedNativeRequests: any[] = [], joins: Promise<void>[] = [], owned = new Set<number>(), live = new Map<number, any>()
    const deny = (reason: string): never => { throw new Error('Fixed native hook spawn rejected: ' + reason) }
    const invoke = (command: string, args: string[], originalOptions: any, kind: string) => {
      const guardedBunSpawn = Bun.spawn
      let child: any
      // Bun's Node compatibility spawn invokes this primitive synchronously.
      // Restore before returning the actual ChildProcess or any event turn.
      try { Bun.spawn = nativeBunSpawn; child = nativeSpawn(command, args, originalOptions) } finally { Bun.spawn = guardedBunSpawn }
      const row: any = { kind, command, args: [...args], cwd: originalOptions.cwd, env: { ...originalOptions.env }, stdio: originalOptions.stdio ?? ['pipe', 'pipe', 'pipe'], shell: originalOptions.shell ?? false, pid: child.pid ?? null, spawned: false, exit: null, close: null, error: null, stdout: '', stderr: '' }
      rows.push(row)
      if (typeof child.pid === 'number') { owned.add(child.pid); live.set(child.pid, child) }
      // Promise never rejects: all errors are immutable receipt fields and the
      // original Source lifecycle still sees its real error/exit/close events.
      joins.push(new Promise<void>(resolve => {
        child.once('spawn', () => { row.spawned = true; row.pid = child.pid; if (typeof child.pid === 'number') { owned.add(child.pid); live.set(child.pid, child) } })
        child.once('error', (error: any) => { row.error = { name: error.name, code: error.code ?? null, message: error.message } })
        child.once('exit', (code: number | null, signal: string | null) => { row.exit = { code, signal } })
        child.once('close', (code: number | null, signal: string | null) => { row.close = { code, signal }; if (kind === 'owned-pgrep' || kind === 'owned-ps') for (const value of row.stdout.trim().split(/\s+/)) if (/^\d+$/.test(value)) owned.add(Number(value)); owned.delete(child.pid); live.delete(child.pid); resolve() })
      }))
      child.stdout?.on('data', (chunk: any) => {
        const text = chunk.toString(); row.stdout += text
        if (kind === 'hook') options.onPhysicalStdout?.(row.stdout)
      })
      child.stderr?.on('data', (chunk: any) => { row.stderr += chunk.toString() })
      return child
    }
    const spawn = (command: string, args?: any, inputOptions?: any) => {
      if (typeof command === 'string' && commands.has(command) && Array.isArray(args) && args.length === 0 && inputOptions?.shell === true) {
        const env = inputOptions.env, base = { ...process.env, CLAUDE_PROJECT_DIR: cwd }
        if (!env || inputOptions.cwd !== options.cwd || fs.realpathSync(inputOptions.cwd) !== cwd || inputOptions.windowsHide !== true || inputOptions.uid !== undefined || inputOptions.gid !== undefined || inputOptions.detached || inputOptions.signal !== undefined || inputOptions.stdio !== undefined && !(Array.isArray(inputOptions.stdio) && inputOptions.stdio.length === 3 && inputOptions.stdio.every((value: any) => value === 'pipe'))) return deny('original hook options/cwd/stdio')
        if (env.HOME !== options.home || env.USERPROFILE !== options.home || env.HOOK_OUTPUT_DIR !== options.effects || !privatePath(env.CLAUDE_CONFIG_DIR) || ['TMPDIR', 'TMP', 'TEMP', 'CLAUDE_CODE_TMPDIR'].some(key => env[key] !== undefined && !privatePath(env[key]))) return deny('private environment')
        if (env.CLAUDE_PLUGIN_ROOT !== undefined && !roots.has(fs.realpathSync(env.CLAUDE_PLUGIN_ROOT))) return deny('skill context')
        const expected: any = { ...base, ...(env.CLAUDE_PLUGIN_ROOT !== undefined ? { CLAUDE_PLUGIN_ROOT: env.CLAUDE_PLUGIN_ROOT } : {}) }
        const keys = new Set([...Object.keys(expected), ...Object.keys(env)])
        if ([...keys].some(key => expected[key] !== env[key])) return deny('unexpected original environment')
        if (Object.keys(inputOptions).some(key => !['shell', 'env', 'cwd', 'windowsHide', 'stdio'].includes(key))) return deny('extra spawn options')
        return invoke(command, args, inputOptions, 'hook')
      }
      // Original tree-kill recursively queries only PID descendants proven by
      // these actual private children. Every other process API stays guarded.
      if ((command === 'pgrep' || command === '/usr/bin/pgrep') && process.env.PATH === '/usr/bin:/bin' && Array.isArray(args) && args.length === 2 && args[0] === '-P' && /^\d+$/.test(String(args[1])) && owned.has(Number(args[1])) && inputOptions === undefined) {
        return invoke(command, args, { cwd: options.cwd, env: { ...process.env }, stdio: ['pipe', 'pipe', 'pipe'], detached: false }, 'owned-pgrep')
      }
      if (process.platform === 'linux' && command === 'ps' && process.env.PATH === '/usr/bin:/bin' && Array.isArray(args) && args.length === 5 && args[0] === '-o' && args[1] === 'pid' && args[2] === '--no-headers' && args[3] === '--ppid' && /^\d+$/.test(String(args[4])) && owned.has(Number(args[4])) && inputOptions === undefined) {
        return invoke(command, args, { cwd: options.cwd, env: { ...process.env }, stdio: ['pipe', 'pipe', 'pipe'], detached: false }, 'owned-ps')
      }
      // Preserve the original denial. Record only this attempted primitive's
      // command/argv and option names, never environment values or mock output.
      rejectedNativeRequests.push({ command: typeof command === 'string' ? command : null, commandType: typeof command, args: Array.isArray(args) ? [...args] : null, argsType: typeof args, cwd: typeof inputOptions?.cwd === 'string' ? inputOptions.cwd : null, shell: inputOptions?.shell ?? false, optionKeys: inputOptions && typeof inputOptions === 'object' ? Object.keys(inputOptions) : [] })
      return guardedSpawn(command, args, inputOptions)
    }
    processes.spawn = spawn
    mock.module('child_process', () => ({ ...processes, spawn }))
    mock.module('node:child_process', () => ({ ...processes, spawn }))
    syncBuiltinESMExports()
    return { rows, rejectedNativeRequests, async join() { for (;;) { const admitted = joins.length; await Promise.all(joins); if (admitted === joins.length) break }; if (live.size) throw new Error('Source physical hooks remained live'); return { corpusSHA256: options.corpusSHA256, rows, allClosed: rows.every(row => row.close !== null), remaining: live.size, rejectedNativeRequests } }, async cleanupAfterFailure() { for (;;) { for (const child of live.values()) child.kill('SIGKILL'); const admitted = joins.length; await Promise.all(joins); if (admitted === joins.length && live.size === 0) break } } }
  }
}
