import { mock, spyOn } from 'bun:test'
import fs from 'node:fs'
import * as fsExports from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'
import { join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { assertDisposableFixtureHome } from './fixture-guard'
import type { AuthStep } from './cliauth-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; nativeStdio?: boolean; steps: (AuthStep & { parsed?: { path: string; options: { text?: boolean; json?: boolean; openai?: boolean } }; parser?: { stdout: string; stderr: string; exitCode: number } })[] }
const home = assertDisposableFixtureHome(input.homeDir, 'CLI OpenAI auth status')
const root = resolve(fileURLToPath(new URL('../../../', import.meta.url)))
const canonicalHome = fs.realpathSync(home), canonicalRoot = fs.realpathSync(root)
const tokenFile = join(home, 'tokens.json'), tokenFileTwo = join(home, 'tokens-two.json')
const marker = join(home, '.claude', 'cc-haha', 'openai-oauth-file-backed')
const results: unknown[] = [], blocked: string[] = [], calls: string[] = [], loggedErrors: unknown[] = []
const deny = (operation: string): never => { blocked.push(operation); throw new Error(`auth status fixture blocked ${operation}`) }
const inside = (value: unknown, write = false) => {
  if (typeof value === 'number') return value >= 0 && value <= 2
  if (value instanceof URL) value = fileURLToPath(value)
  if (Buffer.isBuffer(value)) value = value.toString()
  if (typeof value !== 'string') return false
  const path = resolve(value)
  const under = (directory: string) => path === directory || path.startsWith(directory + sep)
  return under(home) || under(canonicalHome) || !write && (under(root) || under(canonicalRoot))
}
for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'realpathSync', 'readlinkSync', 'accessSync', 'openSync', 'writeFileSync', 'appendFileSync', 'mkdirSync', 'rmSync', 'unlinkSync', 'renameSync', 'copyFileSync', 'chmodSync']) {
  const original = (fs as any)[name]
  const write = ['writeFileSync', 'appendFileSync', 'mkdirSync', 'rmSync', 'unlinkSync', 'renameSync', 'copyFileSync', 'chmodSync'].includes(name)
  const wrapper: any = (...args: any[]) => {
    const writing = name === 'openSync' ? ![undefined, 'r', 0].includes(args[1]) : write
    if (!inside(args[0], writing) || ['renameSync', 'copyFileSync'].includes(name) && !inside(args[1], true)) return deny('filesystem:' + name + ':' + String(args[0]))
    if (name === 'readFileSync' && [tokenFile, tokenFileTwo].includes(resolve(String(args[0])))) calls.push('file:' + (resolve(String(args[0])) === tokenFile ? 'one' : 'two'))
    return original(...args)
  }
  if (original.native) wrapper.native = wrapper
  ;(fs as any)[name] = wrapper
  if ((fsExports as any)[name]) spyOn(fsExports as any, name).mockImplementation(wrapper)
}
const originalExists = fs.existsSync
const existsWrapper = ((path: any) => {
  if (!inside(path)) return deny('filesystem:existsSync')
  if (resolve(String(path)) === marker) calls.push('marker')
  return originalExists(path)
}) as typeof fs.existsSync
fs.existsSync = existsWrapper
spyOn(fsExports, 'existsSync').mockImplementation(existsWrapper)
for (const name of ['readFile', 'writeFile', 'mkdir', 'rm', 'rename', 'copyFile', 'open', 'appendFile', 'readdir', 'stat', 'lstat', 'access', 'realpath', 'unlink', 'chmod']) {
  const original = (fs.promises as any)[name]
  const write = ['writeFile', 'mkdir', 'rm', 'rename', 'copyFile', 'appendFile', 'unlink', 'chmod'].includes(name)
  ;(fs.promises as any)[name] = async (...args: any[]) => {
    if (!inside(args[0], name === 'open' ? !['r', 0].includes(args[1]) : write) || ['rename', 'copyFile'].includes(name) && !inside(args[1], true)) return deny('filesystem:' + name)
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
globalThis.fetch = (() => deny('network:fetch')) as typeof fetch
globalThis.WebSocket = class { constructor() { deny('network:websocket') } } as any
for (const name of ['spawn', 'spawnSync', 'connect', 'listen', 'serve', 'udpSocket', 'dlopen']) (Bun as any)[name] = () => deny('native:' + name)
const bunFile = Bun.file.bind(Bun), bunWrite = Bun.write.bind(Bun)
Bun.file = ((value: any, ...args: any[]) => inside(value) ? (bunFile as any)(value, ...args) : deny('filesystem:Bun.file')) as typeof Bun.file
Bun.write = ((value: any, ...args: any[]) => inside(value, true) ? (bunWrite as any)(value, ...args) : deny('filesystem:Bun.write')) as typeof Bun.write
syncBuiltinESMExports()
let secure: string | null = null, secureError = false
// Only the external secure-storage capability is replaced. Production file
// normalization, marker precedence, memoization and authStatus run unchanged.
mock.module(join(root, 'src/utils/secureStorage/index.ts'), () => ({ getSecureStorage: () => ({
  name: 'fixture-external-adapter', read: () => { calls.push('secure'); if (secureError) throw new Error('fixture secure reader unavailable'); return secure === null ? null : JSON.parse(secure) },
  readAsync: () => deny('secure-storage:readAsync'), update: () => deny('secure-storage:update'), delete: () => deny('secure-storage:delete'),
}) }))
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home)
const storage = await import('../../../src/services/openaiAuth/storage')
const legacy = await import('../../../src/cli/handlers/auth')
const logging = await import('../../../src/utils/log')
const originalLog = logging.logError
spyOn(logging, 'logError').mockImplementation(error => { loggedErrors.push(error instanceof Error ? { name: error.name, message: error.message, stack: error.stack } : { value: error }); originalLog(error) })
const originalWrite = process.stdout.write.bind(process.stdout)
let stdout = '', stderr = ''
class StatusExit { constructor(readonly code: number) {} }
if (input.nativeStdio) {
  if (input.steps.length !== 1) throw new Error('native stdio probe requires one real process-exiting handler')
  process.on('exit', () => fs.writeFileSync(join(home, 'native-evidence.json'), JSON.stringify({ calls, blocked })))
} else {
  spyOn(process.stdout, 'write').mockImplementation(((chunk: any) => { stdout += Buffer.from(chunk).toString('utf8'); return true }) as any)
  spyOn(process.stderr, 'write').mockImplementation(((chunk: any) => { stderr += Buffer.from(chunk).toString('utf8'); return true }) as any)
  spyOn(process, 'exit').mockImplementation(((code: number) => { throw new StatusExit(code) }) as any)
}
for (const step of input.steps) {
  if ('fileEnv' in step) { if (step.fileEnv === null) delete process.env.OPENAI_CODEX_OAUTH_FILE; else process.env.OPENAI_CODEX_OAUTH_FILE = step.fileEnv!.replaceAll('$TOKEN_FILE_TWO', tokenFileTwo).replaceAll('$TOKEN_FILE', tokenFile) }
  if ('secure' in step) secure = step.secure!
  if ('secureError' in step) secureError = step.secureError!
  const chosen = process.env.OPENAI_CODEX_OAUTH_FILE?.trim() || tokenFile
  if ('file' in step) { if (step.file === null) fs.rmSync(chosen, { force: true }); else fs.writeFileSync(chosen, typeof step.file === 'string' ? step.file : new Uint8Array(step.file!)) }
  if ('marker' in step) { if (step.marker) { fs.mkdirSync(join(home, '.claude', 'cc-haha'), { recursive: true }); fs.writeFileSync(marker, '1\n') } else fs.rmSync(marker, { force: true }) }
  if (step.clear) storage.clearOpenAIOAuthTokenCache()
  stdout = ''; stderr = ''; calls.length = 0
  if (step.parser) {
    if (input.nativeStdio) { process.stdout.write(step.parser.stdout); process.stderr.write(step.parser.stderr); process.exit(step.parser.exitCode) }
    results.push({ ...step.parser, calls: [] }); continue
  }
  if (step.parsed?.path !== 'claude auth status' || !step.parsed.options.openai) throw new Error('fixture executes only actual parsed auth status --openai')
  let exitCode: number | null = null, pendingDiagnostic: unknown
  try { await legacy.authStatus(step.parsed.options) } catch (error) {
    if (error instanceof StatusExit) exitCode = error.code
    else if (error instanceof Error) pendingDiagnostic = { name: error.name, message: error.message, rawStack: error.stack }
    else throw error
  }
  results.push({ stdout, stderr, exitCode, calls: [...calls], ...(pendingDiagnostic ? { pendingDiagnostic } : {}) })
}
originalWrite(JSON.stringify({ results, blocked, loggedErrors }) + '\n')
