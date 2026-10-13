import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native IDE original hook')
if (process.env.CC_HAHA_IDE_HOOK_FIXTURE !== '1') throw new Error('Unowned IDE hook fixture')
const transpiler = new Bun.Transpiler({ loader: 'tsx' })
const source = transpiler.transformSync(await Bun.file(new URL('../../../src/hooks/useIDEIntegration.tsx', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const entry = ast.body.find((n: any) => n.type === 'ExportNamedDeclaration' && n.declaration?.id?.name === 'useIDEIntegration')?.declaration
if (!entry) throw new Error('Actual Source useIDEIntegration entry absent')
const envSource = transpiler.transformSync(await Bun.file(new URL('../../../src/utils/envUtils.ts', import.meta.url)).text())
const envAst: any = parse(envSource, { ecmaVersion: 'latest', sourceType: 'module' })
const envFunctions = ['isEnvTruthy', 'isEnvDefinedFalsy'].map(name => {
 const entry = envAst.body.find((n: any) => n.type === 'ExportNamedDeclaration' && n.declaration?.id?.name === name)?.declaration
 if (!entry) throw new Error(`Actual Source ${name} absent`)
 return envSource.slice(entry.start, entry.end)
}).join('\n')
const rows = []
for (const row of input.rows) {
 let dynamic = row.previous
 let findStarted = 0
 let installed = 0
 const run = new Function('_c', 'useEffect', 'getGlobalConfig', 'isSupportedTerminal', 'initializeIdeIntegration', 'process', `${envFunctions}\n${source.slice(entry.start, entry.end)};return useIDEIntegration`)
 const hook = run(() => Array(7), (f: Function) => f(), () => row.global ?? {}, () => row.supported ?? false, (found: Function) => { findStarted++; found(row.ide) }, { env: row.env ?? {} })
 hook({ autoConnectIdeFlag: row.flag, ideToInstallExtension: row.install ?? null, setDynamicMcpConfig: (f: Function) => { dynamic = f(dynamic) }, setShowIdeOnboarding: () => { installed++ }, setIDEInstallationState: () => { installed++ } })
 rows.push({ dynamic: dynamic ?? null, findStarted, installed })
}
process.stdout.write(JSON.stringify({ rows }))
