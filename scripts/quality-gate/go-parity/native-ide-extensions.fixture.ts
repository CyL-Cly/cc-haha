import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original IDE extension installation')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/ide.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const names = ['getVSCodeIDECommandByParentProcess', 'getVSCodeIDECommand', 'getInstallationEnv', 'getInstalledVSCodeExtensionVersion', 'getClaudeCodeVersion', 'installIDEExtension', 'isIDEExtensionInstalled', 'maybeInstallIDEExtension']
const code = ast.body.flatMap((node: any) => node.type === 'FunctionDeclaration' ? [node] : node.type === 'ExportNamedDeclaration' && node.declaration?.type === 'FunctionDeclaration' ? [node.declaration] : []).filter((node: any) => names.includes(node.id.name)).map((node: any) => source.slice(node.start, node.end)).join('\n')
const { lt } = await import('../../../src/utils/semver')
const rows = []
for (const row of input.cases) {
  let global = structuredClone(row.global ?? {})
  const requests: any[] = []
  const exec = async (command: string, args: string[], options: any) => {
    requests.push({ command, args, environment: options?.env ?? input.environment })
    if (args.includes('--install-extension')) return row.installResult
    if (args.includes('--show-versions')) return { code: 0, stdout: row.versionOutput }
    return { code: 0, stdout: row.installedOutput }
  }
  const deps: Record<string, any> = {
    getPlatform: () => row.platform,
    execFileNoThrowWithCwd: exec, execFileNoThrow: exec,
    execSyncWithDefaults_DEPRECATED: (command: string) => command.includes('command=') ? (row.parentPath ? row.parentPath + '/Contents/MacOS/Electron' : '') : '1',
    getFsImplementation: () => ({ stat: async () => { if (!row.parentPath) throw new Error('ENOENT'); return {} } }),
    process: { env: input.environment, ppid: 170123 },
    isVSCodeIde: (kind: string) => ['vscode', 'cursor', 'windsurf'].includes(kind),
    isJetBrainsIde: (kind: string) => kind === 'pycharm',
    isJetBrainsPluginInstalledCached: async () => row.pluginInstalled,
    EXTENSION_ID: 'anthropic.claude-code',
    MACRO: { VERSION: input.version }, lt,
    getGlobalConfig: () => global, saveGlobalConfig: (update: any) => { global = update(global) },
    sleep: async (ms: number) => { if (ms !== 500) throw new Error('Actual Source install delay changed') },
    logEvent: () => {}, logError: () => {}, installFromArtifactory: () => { throw new Error('Internal distribution must not execute in this public fixture') },
  }
  const functions = new Function(...Object.keys(deps), code + ';return { isIDEExtensionInstalled, maybeInstallIDEExtension }')(...Object.values(deps))
  const installed = await functions.isIDEExtensionInstalled(row.kind)
  const status = await functions.maybeInstallIDEExtension(row.kind)
  rows.push({ installed, status, global, requests })
}
process.stdout.write(JSON.stringify(rows))
