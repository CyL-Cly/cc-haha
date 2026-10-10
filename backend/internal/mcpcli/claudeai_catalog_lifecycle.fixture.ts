import { parse } from 'acorn'
import axios from 'axios'
import memoize from 'lodash-es/memoize.js'
import { assertDisposableFixtureHome } from '../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'Claude.ai catalog lifecycle actual Source')
if (process.env.CC_HAHA_CLAUDEAI_CATALOG_FIXTURE !== '1') {
  throw new Error('Unowned catalog lifecycle fixture')
}
const target = new URL(input.address)
if (!['http:', 'https:'].includes(target.protocol) || target.hostname !== '127.0.0.1' || !target.port) {
  throw new Error('Non-loopback catalog transport')
}
await import('../../../preload')
const { isEnvDefinedFalsy } = await import('../../../src/utils/envUtils')
const { normalizeNameForMCP } = await import('../../../src/services/mcp/normalization')
const text = new Bun.Transpiler({ loader: 'ts' }).transformSync(
  await Bun.file(new URL('../../../src/services/mcp/claudeai.ts', import.meta.url)).text(),
)
const ast: any = parse(text, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const fetch = declarations.find((node: any) => node?.type === 'VariableDeclaration'
  && node.declarations.some((entry: any) => entry.id.name === 'fetchClaudeAIMcpConfigsIfEligible'))
const clear = declarations.find((node: any) => node?.type === 'FunctionDeclaration'
  && node.id.name === 'clearClaudeAIMcpConfigsCache')
if (!fetch || !clear) throw new Error('Missing original lifecycle Source')
const sourceConstant = (name: string) => {
  const declaration = declarations.find((node: any) => node?.type === 'VariableDeclaration'
    && node.declarations.some((entry: any) => entry.id.name === name))
  const initializer = declaration?.declarations.find((entry: any) => entry.id.name === name)?.init
  if (initializer?.type !== 'Literal') throw new Error(`Missing original constant ${name}`)
  return initializer.value
}
const deps = {
  axios,
  memoize,
  getClaudeAIOAuthTokens: () => input.tokens,
  isEnvDefinedFalsy,
  getOauthConfig: () => ({ BASE_API_URL: input.address }),
  logForDebugging: () => {},
  logEvent: () => {},
  FETCH_TIMEOUT_MS: sourceConstant('FETCH_TIMEOUT_MS'),
  MCP_SERVERS_BETA_HEADER: sourceConstant('MCP_SERVERS_BETA_HEADER'),
  normalizeNameForMCP,
  // Auth-client cache ownership is covered by the root's service binding.
  clearMcpAuthCache: () => {},
}
const original = new Function('dependencies', `with(dependencies){${text.slice(fetch.start, fetch.end)};${text.slice(clear.start, clear.end)};return {fetch:fetchClaudeAIMcpConfigsIfEligible,clear:clearClaudeAIMcpConfigsCache}}`)(deps)
if (input.timeout) {
  const first = original.fetch()
  const same = original.fetch() === first
  const value = await first
  const hit = original.fetch() === first
  process.stdout.write(JSON.stringify({ same, hit, value, timeoutMs: deps.FETCH_TIMEOUT_MS }) + '\n')
  process.exit(0)
}
const first = original.fetch()
const same = original.fetch() === first
// The owned file read completes while the first HTTP response is held.
const physical = await Bun.file(input.physicalFile).text()
await axios.post(input.address + '/control/physical-read', { physical })
let second: any
if (input.clear) {
  original.clear()
  second = original.fetch()
}
await axios.post(input.address + '/control/release', { clear: !!input.clear })
const firstValue = await first
const secondValue = second ? await second : await original.fetch()
const hit = original.fetch() === (second ?? first)
process.stdout.write(JSON.stringify({ same, hit, physical, firstValue, secondValue }) + '\n')
