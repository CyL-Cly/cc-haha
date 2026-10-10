import { parse } from 'acorn'
import { createRequire } from 'node:module'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native Vertex request oracle')
if (process.env.CC_HAHA_VERTEX_FIXTURE !== '1') throw new Error('Unowned Vertex fixture')
const input = await Bun.stdin.json()
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const raw = await Bun.file(new URL('../../../backend/internal/provider/testdata/vertex_0_14_4/client.ts', import.meta.url)).text()
if (createHash('sha256').update(raw).digest('hex') !== '07d379c86df9b6e63f5384b4fb8d0981caeff87cc7d8fb0f19619884d41247fb') throw new Error('Reviewed Vertex source changed')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(raw)
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declaration = ast.body.find((node: any) => node.type === 'ExportNamedDeclaration' && node.declaration?.id?.name === 'AnthropicVertex').declaration
const methods = ['prepareOptions', 'buildRequest'].map(name => { const method = declaration.body.body.find(node => node.key.name === name); return source.slice(method.start, method.end) })
const { buildHeaders } = require(join(dirname(require.resolve('@anthropic-ai/sdk')), 'internal/headers.js'))
const make = new Function('buildHeaders', `
const DEFAULT_VERSION='vertex-2023-10-16',MODEL_ENDPOINTS=new Set(['/v1/messages','/v1/messages?beta=true']),isObj=x=>x!==null&&typeof x==='object'&&!Array.isArray(x);
class Base { async buildRequest(options) { return { url:new URL(this.base+options.path).href, body:JSON.stringify(options.body), headers:Object.fromEntries(options.headers.values), path:options.path } } }
return new(class extends Base {${methods.join('\n')}})();`)
const rows = []
for (const row of input.rows) {
  const client = make(buildHeaders)
  client.base = row.base || 'https://us-east5-aiplatform.googleapis.com/v1'
  client.region = row.region || 'us-east5'
  client.projectId = row.project || null
  client._authClientPromise = Promise.resolve({ projectId: row.clientProject, getRequestHeaders: async () => new Headers(row.authHeaders || {}) })
  const options = { method: 'post', path: row.path || '/v1/messages?beta=true', body: row.body, headers: row.headers || {} }
  const before = JSON.stringify(row.body)
  try { await client.prepareOptions(options); const value = await client.buildRequest(options); rows.push({ value, originalUnchanged: before === JSON.stringify(row.body) }) }
  catch (error) { rows.push({ error: error.message }) }
}
console.log(JSON.stringify({ rows }))
