import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import * as path from 'node:path'
import * as fs from 'node:fs/promises'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original installation diagnostic')
function declarations(source: string, names?: string[]) {
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  return ast.body.flatMap((n: any) => n.type === 'ExportNamedDeclaration' ? [n.declaration] : [n]).filter((n: any) => n && ((n.type === 'FunctionDeclaration' && (!names || names.includes(n.id.name))) || (n.type === 'VariableDeclaration' && n.declarations.some((d: any) => !names || names.includes(d.id.name))))).map((n: any) => source.slice(n.start, n.end)).join('\n')
}
async function source(url: string) { return new Bun.Transpiler({ loader: 'ts', define: { 'process.env.NODE_ENV': "__sourceProcessEnvironment.NODE_ENV" } }).transformSync(await Bun.file(new URL(url, import.meta.url)).text()) }
const packages = declarations(await source('../../../src/utils/nativeInstaller/packageManagers.ts'))
const diagnostic = declarations(await source('../../../src/utils/doctorDiagnostic.ts'))
const disabled = declarations(await source('../../../src/utils/config.ts'), ['getAutoUpdaterDisabledReason', 'formatAutoUpdaterDisabledReason'])
const envTruthy = declarations(await source('../../../src/utils/envUtils.ts'), ['isEnvTruthy'])
const privacy = declarations(await source('../../../src/utils/privacyLevel.ts'), ['getEssentialTrafficOnlyReason'])
const local = declarations(await source('../../../src/utils/localInstaller.ts'), ['getLocalInstallDir', 'localInstallationExists', 'getShellType', 'isRunningFromLocalInstallation'])
const environmentValidation = declarations(await source('../../../src/utils/envValidation.ts'))
const results = []
for (const row of input.cases) {
  const calls: any[] = []
  const reads: string[] = []
  const join = row.platform === 'windows' ? path.win32.join : path.join
  const files = row.physical ? fs : {
    stat: async (file: string) => { if (!row.files?.includes(file)) throw new Error('ENOENT'); return { isFile: () => true } },
    access: async (file: string) => { if (!row.files?.includes(file)) throw new Error('ENOENT') },
    readFile: async (file: string) => { reads.push(file); if (file === '/etc/os-release' && row.osRelease !== undefined) return row.osRelease; if (file === join(row.managedRoot ?? input.homeDir, 'managed-settings.json') && row.managed !== undefined) return JSON.stringify(row.managed); throw new Error('ENOENT') },
    realpath: async (file: string) => { if (row.realpaths?.[file]) return row.realpaths[file]; throw new Error('ENOENT') },
  }
  const run = async (command: string, args: string[], options: any) => { calls.push({ command, args, options }); if (row.physical) { const child = Bun.spawn([command, ...args], { cwd: row.cwd ?? input.homeDir, env: row.environment, stdout: 'pipe', stderr: 'pipe' }); const [stdout, stderr, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]); return { code, stdout: stdout.replace(/\r?\n$/, ''), stderr: stderr.replace(/\r?\n$/, '') } } return row.commands?.[command] ?? { code: 1, stdout: '', stderr: '' } }
  const deps: Record<string, any> = {
    __sourceProcessEnvironment: row.environment ?? {},
    process: { execPath: row.executable ?? '', argv: [row.argv0 ?? '', row.invoked ?? ''], env: row.environment ?? {} },
    getPlatform: () => row.platform,
    getGlobalConfig: () => row.global ?? {},
    isInBundledMode: () => row.bundled ?? true,
    readFile: files.readFile, realpath: files.realpath, access: files.access,
    getFsImplementation: () => files,
    memoize: (fn: Function) => { let value: any; let done = false; return (...args: any[]) => { if (!done) { done = true; value = fn(...args) } return value } },
    execFileNoThrow: (command: string, args: string[], options?: any) => run(command, args, options ?? {}),
    execa: async (command: string, options: any) => { const r = await run(command, [], options); return { ...r, exitCode: r.code } },
    logForDebugging: () => {}, homedir: () => input.homeDir, join,
    win32: path.win32, posix: path.posix, delimiter: row.platform === 'windows' ? ';' : ':',
    getCwd: () => row.cwd ?? input.homeDir,
    getClaudeConfigHomeDir: () => row.configDir ?? join(input.homeDir, '.claude'),
    getManagedFilePath: () => row.managedRoot ?? input.homeDir,
    jsonParse: JSON.parse, CUSTOMIZATION_SURFACES: ['skills', 'agents', 'hooks', 'mcp'],
    findClaudeAlias: async () => row.alias ?? null, findValidClaudeAlias: async () => row.validAlias ? row.alias : null,
    getShellConfigPaths: () => ({ zsh: join(row.environment?.ZDOTDIR || input.homeDir, '.zshrc'), bash: join(input.homeDir, '.bashrc'), fish: join(input.homeDir, '.config/fish/config.fish') }),
    which: async () => row.which ?? null,
    checkGlobalInstallPermissions: async () => ({ hasPermissions: row.permissions ?? false }),
    getRipgrepStatus: () => row.ripgrep ?? { working: true, mode: 'system', path: '/private/fixture/rg' },
    SandboxManager: { getLinuxGlobPatternWarnings: () => row.patterns ?? [] },
    MACRO: { VERSION: row.version ?? '2.1.288', PACKAGE_URL: row.packageName ?? '@fixture/go-cli' },
  }
  const api = new Function(...Object.keys(deps), packages + '\n' + envTruthy + '\n' + privacy + '\n' + disabled + '\n' + local + '\n' + diagnostic + ';return { getPackageManager,getOsRelease,getDoctorDiagnostic }')(...Object.values(deps))
  if (row.mode === 'environment') { const validate = new Function('logForDebugging', environmentValidation + ';return validateBoundedIntEnvVar')(() => {}); results.push({ validation: validate('fixture', row.value, row.default, row.upper) }); continue }
  if (row.mode === 'packages') { results.push({ manager: await api.getPackageManager(), release: await api.getOsRelease(), calls, reads, second: await api.getPackageManager() }) }
  else { results.push({ diagnostic: await api.getDoctorDiagnostic(), calls, reads }) }
}
process.stdout.write(JSON.stringify(results))
