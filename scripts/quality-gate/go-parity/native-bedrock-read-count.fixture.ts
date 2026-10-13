import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Bedrock Read count')
if (process.env.CC_HAHA_BEDROCK_READ_COUNT_FIXTURE !== '1') throw new Error('Unowned Bedrock Read count fixture')
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: new URL(input.url).origin, absentPaths: absentManagedSettingsFixturePaths() })
for (const [key, value] of Object.entries(input.env ?? {})) process.env[key] = String(value)
async function declaration(file: string, name: string) {
  const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(file, import.meta.url)).text())
  const ast = parse(code, { ecmaVersion: 'latest', sourceType: 'module' }) as any
  const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
  const node = nodes.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === name)
  if (!node) throw new Error(`Missing actual Source ${name}`)
  return code.slice(node.start, node.end)
}
const clientBody = await declaration('../../../src/utils/model/bedrock.ts', 'createBedrockRuntimeClient')
const foundationBody = await declaration('../../../src/utils/model/bedrock.ts', 'isFoundationModel')
const countBody = await declaration('../../../src/services/tokenEstimation.ts', 'countTokensWithBedrock')
// The original AWS SDK builds, signs, retries and decodes its own request. Only
// its native HTTP handler is a physical loopback fetch capability. No request
// body, count response or business decision is prepared in this adapter.
const handler = { handle: async (request: any) => {
  const url = `${request.protocol}//${request.hostname}${request.port ? ':' + request.port : ''}${request.path}`
  const response = await fetch(url, { method: request.method, headers: request.headers, body: request.body, redirect: 'error' })
  return { response: { statusCode: response.status, headers: Object.fromEntries(response.headers), body: new Uint8Array(await response.arrayBuffer()) } }
} }
const createClient = new Function('getAWSRegion', 'isEnvTruthy', 'getAWSClientProxyConfig', 'refreshAndGetAwsCredentials', `${clientBody};return createBedrockRuntimeClient`)(() => input.region ?? 'us-east-1', (value: string) => ['1', 'true', 'yes', 'on'].includes(value?.trim().toLowerCase()), async () => ({ requestHandler: handler }), async () => input.credentials ?? undefined)
const profileLookups: string[] = []
const count = new Function('createBedrockRuntimeClient', 'getInferenceProfileBackingModel', 'jsonStringify', 'logError', `${foundationBody};const TOKEN_COUNT_MAX_TOKENS=2048,TOKEN_COUNT_THINKING_BUDGET=1024;${countBody};return countTokensWithBedrock`)(createClient, async (model: string) => { profileLookups.push(model); throw new Error('Profile lookup is outside the foundation fixture') }, JSON.stringify, () => {})
const results = []
for (const row of input.rows) results.push(await count({ model: row.model, messages: [{ role: 'user', content: row.content }], tools: [], betas: row.betas ?? [], containsThinking: false }))
if (profileLookups.length) throw new Error('Foundation count unexpectedly entered profile lookup')
if (capabilities.blocked.length) throw new Error('Blocked Bedrock count capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify(results))
