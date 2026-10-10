import axios from 'axios'
import { parse } from 'acorn'
import { lookup } from 'node:dns'
import { isIP } from 'node:net'
import https from 'node:https'
import { HttpsProxyAgent } from 'https-proxy-agent'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original HTTP Hook transport')
if (process.env.CC_HAHA_HTTP_HOOK_TRANSPORT_FIXTURE !== '1') throw new Error('Missing HTTP Hook transport fixture')
const declarations = async (path: string) => {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const functions = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
  return (names: string[]) => names.map(name => {
    const node = functions.find((node: any) => node.id.name === name)
    if (!node) throw new Error('Missing original HTTP transport function ' + name)
    return source.slice(node.start, node.end)
  }).join('\n')
}
const policy = await declarations('../../../src/utils/hooks/execHttpHook.ts')
const address = await declarations('../../../src/utils/hooks/ssrfGuard.ts')
const abort = await declarations('../../../src/utils/combinedAbortSignal.ts')
const createCombinedAbortSignal = new Function('createAbortController', abort(['createCombinedAbortSignal']) + '\nreturn createCombinedAbortSignal')(() => new AbortController())
const ssrfGuardedLookup = new Function('isIP', 'dnsLookup', address(['ssrfGuardedLookup', 'ssrfError', 'isBlockedAddress', 'isBlockedV4', 'isBlockedV6', 'expandIPv6Groups', 'extractMappedIPv4']) + '\nreturn ssrfGuardedLookup')(isIP, lookup)
if (input.ca) axios.defaults.httpsAgent = new https.Agent({ ca: input.ca })
const rows = []
for (const row of input.rows) {
  const target = new URL(row.hook.url)
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(target.hostname) && !(input.proxy && target.hostname === '10.0.0.1')) throw new Error('HTTP transport fixture must use controlled loopback proxy')
  let proxyAgent: any
  if (input.proxy) {
    if (new URL(input.proxy).hostname !== '127.0.0.1') throw new Error('Proxy fixture must use loopback')
    proxyAgent = new HttpsProxyAgent(input.proxy, input.ca ? { ca: input.ca } : undefined)
    axios.defaults.httpAgent = proxyAgent
    axios.defaults.httpsAgent = proxyAgent
  }
  for (const [name, value] of Object.entries(row.environment ?? {})) {
    if (!/^HTTP_HOOK_FIXTURE_[A-Z0-9_]+$/.test(name) || typeof value !== 'string') throw new Error('Unsafe fixture environment')
    process.env[name] = value
  }
  const execute = new Function('axios', 'createCombinedAbortSignal', 'logForDebugging', 'errorMessage', 'getProxyUrl', 'shouldBypassProxy', 'settingsModule', 'ssrfGuardedLookup', 'getSandboxProxyConfig', 'DEFAULT_HTTP_HOOK_TIMEOUT_MS', policy(['getHttpHookPolicy', 'urlMatchesPattern', 'sanitizeHeaderValue', 'interpolateEnvVars', 'execHttpHook']) + '\nreturn execHttpHook')(axios, createCombinedAbortSignal, () => {}, (error: any) => error.message, () => input.proxy, () => false, { getInitialSettings: () => row.settings }, ssrfGuardedLookup, async () => undefined, 600000)
  const controller = new AbortController()
  if (row.preAborted) controller.abort()
  rows.push(await execute(row.hook, 'UserPromptSubmit', row.body, controller.signal))
  proxyAgent?.destroy()
  for (const name of Object.keys(row.environment ?? {})) delete process.env[name]
}
axios.defaults.httpsAgent?.destroy()
process.stdout.write(JSON.stringify({ rows }))
