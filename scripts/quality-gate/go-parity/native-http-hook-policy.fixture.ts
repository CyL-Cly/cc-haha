import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original HTTP Hook policy')
if (process.env.CC_HAHA_HTTP_HOOK_POLICY_FIXTURE !== '1') throw new Error('Missing HTTP Hook fixture')
const declarations = async (path: string) => {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const functions = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
  return (names: string[]) => names.map(name => {
    const node = functions.find((node: any) => node.id.name === name)
    if (!node) throw new Error('Missing original HTTP Hook function ' + name)
    return source.slice(node.start, node.end)
  }).join('\n')
}
const policy = await declarations('../../../src/utils/hooks/execHttpHook.ts')
const address = await declarations('../../../src/utils/hooks/ssrfGuard.ts')
const { isIP } = await import('node:net')
const guard = new Function('isIP', address(['isBlockedAddress', 'isBlockedV4', 'isBlockedV6', 'expandIPv6Groups', 'extractMappedIPv4']) + '\nreturn {isBlockedAddress}') (isIP)
const rows = []
for (const row of input.rows) {
  for (const [name, value] of Object.entries(row.environment ?? {})) {
    if (!/^HTTP_HOOK_FIXTURE_[A-Z0-9_]+$/.test(name) || typeof value !== 'string') throw new Error('Unsafe HTTP Hook fixture environment')
    process.env[name] = value
  }
  const original = new Function('logForDebugging', policy(['urlMatchesPattern', 'sanitizeHeaderValue', 'interpolateEnvVars']) + '\nreturn {urlMatchesPattern,sanitizeHeaderValue,interpolateEnvVars}')(() => {})
  let prepared
  if (row.hook) {
    let capture: any
    const execute = new Function('axios', 'createCombinedAbortSignal', 'logForDebugging', 'errorMessage', 'getProxyUrl', 'shouldBypassProxy', 'settingsModule', 'ssrfGuardedLookup', 'getSandboxProxyConfig', 'DEFAULT_HTTP_HOOK_TIMEOUT_MS', policy(['getHttpHookPolicy', 'urlMatchesPattern', 'sanitizeHeaderValue', 'interpolateEnvVars', 'execHttpHook']) + '\nreturn execHttpHook')({ post: async (url: any, body: any, config: any) => { capture = { url, body, headers: config.headers, maxRedirects: config.maxRedirects, proxy: config.proxy, responseType: config.responseType }; return { status: 200, data: 'original response' } } }, () => ({ signal: new AbortController().signal, cleanup: () => {} }), () => {}, (error: any) => error.message, () => undefined, () => false, { getInitialSettings: () => row.settings }, () => {}, async () => undefined, 600000)
    const result = await execute(row.hook, 'UserPromptSubmit', '{"actual":"input"}')
    prepared = { result, capture }
  }
  rows.push({ urlMatches: (row.patterns ?? []).map((pattern: string) => original.urlMatchesPattern(row.url, pattern)), values: (row.values ?? []).map((value: string) => original.interpolateEnvVars(value, new Set(row.allowed ?? []))), blocked: (row.addresses ?? []).map(guard.isBlockedAddress), prepared })
  for (const name of Object.keys(row.environment ?? {})) delete process.env[name]
}
process.stdout.write(JSON.stringify({ rows }))
