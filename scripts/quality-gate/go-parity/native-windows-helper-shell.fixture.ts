import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Windows apiKeyHelper shell consumer')
const authSource = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/auth.ts', import.meta.url)).text())
const authAst: any = parse(authSource, { ecmaVersion: 'latest', sourceType: 'module' })
const execute = authAst.body.find((node: any) => node.type === 'FunctionDeclaration' && node.id.name === '_executeApiKeyHelper')
if (!execute) throw new Error('Actual Source apiKeyHelper consumer missing')
const spawnAst: any = parse(input.nativeSpawnSource, { ecmaVersion: 'latest', sourceType: 'script' })
const normalize = spawnAst.body.find((node: any) => node.type === 'FunctionDeclaration' && node.id.name === 'normalizeSpawnArguments')
if (!normalize) throw new Error('Actual native Node shell projection missing')
const projectionRows = input.invocations.map((row: any) => {
  const environment = new Proxy(row.environment, { get: (target, name: string) => target[Object.keys(target).find(key => key.toLowerCase() === name.toLowerCase()) ?? name] })
  const deps: Record<string, any> = {
    process: { platform: 'win32', env: environment }, isZOS: false, emittedDEP0190Already: false,
    validateString: () => {}, validateArgumentNullCheck: () => {}, validateArgumentsNullCheck: () => {}, validateObject: () => {}, validateBoolean: () => {}, validateInt32: () => {}, getValidatedPath: (value: any) => value,
    kEmptyObject: {}, ArrayIsArray: Array.isArray,
    ArrayPrototypeSlice: (array: any[]) => array.slice(), ArrayPrototypeJoin: (array: any[], sep: string) => array.join(sep), ArrayPrototypeUnshift: (array: any[], ...values: any[]) => array.unshift(...values), ArrayPrototypePush: (array: any[], ...values: any[]) => array.push(...values), ArrayPrototypeFilter: (array: any[], fn: any) => array.filter(fn), ArrayPrototypeSort: (array: any[]) => array.sort(), StringPrototypeToUpperCase: (value: string) => value.toUpperCase(), RegExpPrototypeExec: (regex: RegExp, value: string) => regex.exec(value), SafeSet: Set,
    copyProcessEnvToEnv: () => {}, permission: { isEnabled: () => false },
  }
  const normalized = new Function(...Object.keys(deps), input.nativeSpawnSource.slice(normalize.start, normalize.end) + ';return normalizeSpawnArguments')(...Object.values(deps))(row.command, [], { shell: true, env: row.environment })
  return { Command: normalized.file, Arguments: normalized.args.slice(1), Verbatim: normalized.windowsVerbatimArguments ?? false }
})
const results = []
for (const row of input.results) {
  const requests: any[] = []
  const deps: Record<string, any> = {
    getConfiguredApiKeyHelper: () => row.command,
    isApiKeyHelperFromProjectOrLocalSettings: () => row.project,
    checkHasTrustDialogAccepted: () => row.trusted,
    logAntError: () => {}, logEvent: () => {}, MACRO: { FEEDBACK_CHANNEL: 'isolated fixture' },
    execa: async (...args: any[]) => { requests.push(args); return row.output },
  }
  const executeActual = new Function(...Object.keys(deps), authSource.slice(execute.start, execute.end) + ';return _executeApiKeyHelper')(...Object.values(deps))
  try { results.push({ value: await executeActual(row.nonInteractive), requests }) }
  catch (error: any) { results.push({ error: error.message, requests }) }
}
process.stdout.write(JSON.stringify({ invocations: projectionRows, results }))
