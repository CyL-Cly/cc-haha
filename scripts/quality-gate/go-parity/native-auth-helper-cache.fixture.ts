import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native auth helper cache')
if (process.env.CC_HAHA_AUTH_HELPER_FIXTURE !== '1') throw new Error('Unowned helper cache fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/auth.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const names = ['calculateApiKeyHelperTTL', 'getApiKeyHelperElapsedMs', 'getApiKeyFromApiKeyHelper', '_runAndCache', 'getApiKeyFromApiKeyHelperCached', 'clearApiKeyHelperCache']
const declarations = ['DEFAULT_API_KEY_HELPER_TTL', '_apiKeyHelperCache', '_apiKeyHelperInflight', '_apiKeyHelperEpoch']
const code = [...declarations.map(name => {
  const node = nodes.find((node: any) => node?.type === 'VariableDeclaration' && node.declarations.some((decl: any) => decl.id.name === name))
  if (!node) throw new Error('Missing actual helper state ' + name)
  return source.slice(node.start, node.end)
}), ...names.map(name => {
  const node = nodes.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name)
  if (!node) throw new Error('Missing actual helper function ' + name)
  return source.slice(node.start, node.end)
})].join('\n')
const make = new Function('process', 'Date', '_executeApiKeyHelper', 'getConfiguredApiKeyHelper', 'console', 'chalk', 'logForDebugging', code + '\nreturn { ttl: calculateApiKeyHelperTTL, get: getApiKeyFromApiKeyHelper, cached: getApiKeyFromApiKeyHelperCached, clear: clearApiKeyHelperCache, settle: () => _apiKeyHelperInflight?.promise }')
const ttl = input.ttls.map((value: string) => make({ env: { CLAUDE_CODE_API_KEY_HELPER_TTL_MS: value } }, Date, () => {}, () => '', console, { red: (value: string) => value }, () => {}).ttl())
const rows = []
for (const row of input.rows) {
  let now = 1000, calls = 0
  const errors: string[] = []
  const engine = make({ env: { CLAUDE_CODE_API_KEY_HELPER_TTL_MS: '10' } }, { now: () => now }, async () => {
    const value = row.responses[calls++]
    if (value.error) throw new Error(value.error)
    return value.value ?? null
  }, () => 'actual-private-command', { error: (value: string) => errors.push(value) }, { red: (value: string) => value }, () => {})
  const steps = []
  for (const action of row.actions) {
    if (action.now !== undefined) now = action.now
    if (action.clear) engine.clear()
    const value = await engine.get(true)
    await engine.settle()
    steps.push({ value, cached: engine.cached(), calls })
  }
  rows.push({ steps, errors })
}
process.stdout.write(JSON.stringify({ ttl, rows }))
