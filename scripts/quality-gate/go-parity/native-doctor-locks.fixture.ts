import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import * as fs from 'node:fs'
import { join } from 'node:path'
import { execFileSync } from 'node:child_process'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Doctor version locks')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/nativeInstaller/pidLock.ts', import.meta.url)).text())
const names = ['FALLBACK_STALE_MS', 'readLockContent', 'isProcessRunning', 'isClaudeProcess', 'isLockActive', 'cleanupStaleLocks', 'getAllLockInfo']
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const body = ast.body.flatMap((n: any) => n.type === 'ExportNamedDeclaration' ? [n.declaration] : [n]).filter((n: any) => n && (n.type === 'FunctionDeclaration' && names.includes(n.id.name) || n.type === 'VariableDeclaration' && n.declarations.some((d: any) => names.includes(d.id.name)))).map((n: any) => source.slice(n.start, n.end)).join('\n')
const deps: Record<string, any> = { process, Date, join, getFsImplementation: () => ({ ...fs, readdirStringSync: fs.readdirSync }), jsonParse: JSON.parse, logForDebugging: () => {}, logError: () => {}, toError: (e: unknown) => e, isENOENT: (e: any) => e.code === 'ENOENT', getProcessCommand: (pid: number) => { try { return execFileSync('/bin/ps', ['-o', 'command=', '-p', String(pid)], { encoding: 'utf8', timeout: 1000 }).trim() } catch { return '' } } }
const api = new Function(...Object.keys(deps), body + ';return {cleanupStaleLocks,getAllLockInfo}')(...Object.values(deps))
const cleaned = api.cleanupStaleLocks(input.directory)
process.stdout.write(JSON.stringify({ cleaned, locks: api.getAllLockInfo(input.directory), remaining: fs.readdirSync(input.directory) }))
