import { parse } from 'acorn'
import nodePath from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original IDE local discovery')
if (process.env.CC_HAHA_IDE_DISCOVERY_FIXTURE !== '1') throw new Error('Unowned IDE discovery')
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/ide.ts', import.meta.url)).text())
const ast: any = parse(code, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const names = ['isProcessRunning', 'makeAncestorPidLookup', 'getSortedIdeLockfiles', 'readIdeLockfile', 'cleanupStaleIdeLockfiles', 'detectIDEs', 'findAvailableIDE', 'toIDEDisplayName']
const bodies = names.map(name => { const n = nodes.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name); if (!n) throw new Error('Missing original IDE function ' + name); return code.slice(n.start, n.end) }).join('\n')
const configs = nodes.find((node: any) => node?.type === 'VariableDeclaration' && node.declarations.some((d: any) => d.id.name === 'supportedIdeConfigs'))
if (!configs) throw new Error('Missing original IDE declarations')
const rows = []
for (const row of input.rows) {
  const paths = row.platform === 'windows' ? nodePath.win32 : nodePath.posix
  const locks = new Map(row.locks.map((lock: any) => [lock.path, lock]))
  const removed: string[] = []
  const directories = [...new Set(row.locks.map((lock: any) => paths.dirname(lock.path)))]
  let ancestryCalls = 0
  let elapsed = 0
  const noop = () => {}
  const fs = {
    readdir: async (directory: string) => row.locks.filter((lock: any) => paths.dirname(lock.path) === directory && !removed.includes(lock.path)).map((lock: any) => ({ name: paths.basename(lock.path) })),
    stat: async (path: string) => { const lock: any = locks.get(path); if (!lock || lock.statError) throw new Error('controlled stat failure'); return { mtime: new Date(lock.modified) } },
    readFile: async (path: string) => { const lock: any = locks.get(path); if (!lock || lock.readError) throw new Error('controlled read failure'); return lock.data },
    unlink: async (path: string) => { removed.push(path) },
  }
  const host = (_windows: boolean, port: number) => row.env?.CLAUDE_CODE_IDE_HOST_OVERRIDE || row.host || '127.0.0.1'
  const deps = {
 jsonParse: JSON.parse,
    process: { env: row.env ?? {}, ppid: row.parentPID ?? 10, kill: (pid: number) => { if (!(row.running ?? []).includes(pid)) throw new Error('controlled process absence') } },
    getPlatform: () => row.platform,
    getOriginalCwd: () => row.cwd,
    getFsImplementation: () => fs,
    getIdeLockfilesPaths: async () => directories,
    getAncestorPidsAsync: async () => { ancestryCalls++; return row.ancestors ?? [] },
    isSupportedTerminal: () => row.supported ?? false,
    isEnvTruthy: (value: string | undefined) => ['1', 'true', 'yes', 'on'].includes(value?.toLowerCase().trim() ?? ''),
    envDynamic: { terminal: row.terminal ?? '' },
    pathSeparator: paths.sep, resolve: (...values: string[]) => paths.resolve(row.cwd, ...values), join: paths.join, basename: paths.basename,
    detectHostIP: host, checkIdeConnection: async (_host: string, port: number) => (row.responding ?? []).includes(port),
    checkWSLDistroMatch: (path: string, distro: string) => { const match = path.match(/^\\\\wsl(?:\.localhost|\$)\\([^\\]+)(.*)$/); return !match || match[1] === distro },
    WindowsToWSLConverter: class { toLocalPath(path: string) { return row.converted?.[path] ?? path.replace(/\\/g, '/').replace(/^([a-z]):/i, (_: string, drive: string) => '/mnt/' + drive.toLowerCase()) } },
    getIsScrollDraining: () => false,
    createAbortController: () => new AbortController(),
    sleep: async (delay: number) => { elapsed += delay },
    Date: { now: () => elapsed }, logError: noop, logForDebugging: noop,
    capitalize: (value: string) => value ? value[0].toUpperCase() + value.slice(1).toLowerCase() : '',
    EDITOR_DISPLAY_NAMES: {},
  }
  const actual = new Function('dependencies', `with(dependencies){ ${code.slice(configs.start, configs.end)}; let currentIDESearch; ${bodies}; return {detectIDEs,cleanupStaleIdeLockfiles,findAvailableIDE} }`)(deps)
  let result
  if (row.operation === 'find') result = await actual.findAvailableIDE()
  else { if (row.cleanup) await actual.cleanupStaleIdeLockfiles(); result = await actual.detectIDEs(row.includeInvalid ?? false) }
  rows.push({ result, removed, ancestryCalls, elapsed })
}
process.stdout.write(JSON.stringify({ rows }))
