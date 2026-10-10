import { parse } from 'acorn'
import { isAbsolute, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original FileChanged watcher')
if (process.env.CC_HAHA_ENV_WATCHER_FIXTURE !== '1') throw new Error('Unowned environment watcher fixture')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/hooks/fileChangedWatcher.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n).filter((n: any) => n?.type === 'FunctionDeclaration')
const extract = (names: string[]) => names.map(name => { const n = nodes.find((n: any) => n.id.name === name); if (!n) throw new Error('Missing watcher Source ' + name); return source.slice(n.start, n.end) }).join('\n')
const rows = input.rows.map((row: any) => {
 const counts: any[] = []
 const native = { watch: (paths: any[], options: any) => { counts.push({ paths, options }); return { on() { return this }, close() { return Promise.resolve() } } } }
 const production = new Function('dependencies', `with(dependencies){let watcher=null,currentCwd,dynamicWatchPaths=[],dynamicWatchPathsSorted=[],initialized=false,hasEnvHooks=false,notifyCallback=null;${extract(['resolveWatchPaths','startWatching','restartWatching','initializeFileChangedWatcher','updateWatchPaths'])};return {initializeFileChangedWatcher,updateWatchPaths,resolveWatchPaths}}`)({ isAbsolute, join, chokidar: native, logForDebugging() {}, registerCleanup() {}, getHooksConfigFromSnapshot: () => row.config, handleFileEvent() {}, dispose() {} })
 production.initializeFileChangedWatcher(row.cwd)
 for (const paths of row.updates) production.updateWatchPaths(paths)
 return { paths: production.resolveWatchPaths(), native: counts }
})
process.stdout.write(JSON.stringify({ rows }))
