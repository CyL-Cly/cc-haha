import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import * as fs from 'node:fs/promises'
import * as path from 'node:path'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original IDE native runtime utilities')
function declarations(source: string, names: string[]) {
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  return ast.body.flatMap((node: any) => node.type === 'ExportNamedDeclaration' ? [node.declaration] : [node]).filter((node: any) => node && (node.type === 'FunctionDeclaration' && names.includes(node.id.name) || node.type === 'VariableDeclaration' && node.declarations.some((d: any) => names.includes(d.id.name)))).map((node: any) => source.slice(node.start, node.end)).join('\n')
}
const transpile = async (url: URL) => new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(url).text())
const ide = await transpile(new URL('../../../src/utils/ide.ts', import.meta.url))
const running = declarations(ide, ['supportedIdeConfigs', 'detectRunningIDEsImpl'])
const jetbrains = await transpile(new URL('../../../src/utils/jetbrains.ts', import.meta.url))
const plugins = declarations(jetbrains, ['PLUGIN_PREFIX', 'ideNameToDirMap', 'buildCommonPluginDirectoryPaths', 'detectPluginDirectories', 'isJetBrainsPluginInstalled'])
const outputs = []
for (const row of input.cases) {
  if (row.mode === 'running') {
    const calls: any[] = []
    const run = new Function('getPlatform', 'execa', 'logError', running + ';return detectRunningIDEsImpl')(() => row.platform, async (...args: any[]) => { calls.push(args); if (row.throw) throw new Error('native process fixture failure'); return { stdout: row.stdout } }, () => {})
    outputs.push({ ides: await run(), calls })
  } else {
    const deps: Record<string, any> = { homedir: () => input.homeDir, platform: () => row.platform, join: row.platform === 'win32' ? path.win32.join : path.join, process: { env: row.environment ?? {} }, getFsImplementation: () => ({ readdir: (directory: string) => fs.readdir(directory, { withFileTypes: true }), stat: fs.stat }) }
    const functions = new Function(...Object.keys(deps), plugins + ';return { isJetBrainsPluginInstalled, buildCommonPluginDirectoryPaths }')(...Object.values(deps))
    outputs.push({ directories: functions.buildCommonPluginDirectoryPaths(row.kind), installed: row.pathsOnly ? undefined : await functions.isJetBrainsPluginInstalled(row.kind) })
  }
}
process.stdout.write(JSON.stringify(outputs))
