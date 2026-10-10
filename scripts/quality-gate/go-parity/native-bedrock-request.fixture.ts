import { parse } from 'acorn'
import { createRequire } from 'node:module'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native Bedrock request oracle')
if (process.env.CC_HAHA_BEDROCK_FIXTURE !== '1') throw new Error('Unowned Bedrock fixture')
const input = await Bun.stdin.json()
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const sourcePath = new URL('../../../backend/internal/provider/testdata/bedrock_0_26_4/client.ts', import.meta.url)
const raw = await Bun.file(sourcePath).text()
if (createHash('sha256').update(raw).digest('hex') !== 'fbfc8d40b86d6d4ac0b6bc640d21300935148cc82c7cbaa348eb95571c8e8ba8') throw new Error('Reviewed Bedrock source changed')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(raw)
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const client = ast.body.find((node: any) => node.type === 'ExportNamedDeclaration' && node.declaration?.id?.name === 'AnthropicBedrock').declaration
const method = client.body.body.find((node: any) => node.key.name === 'buildRequest')
const { path } = require(join(dirname(require.resolve('@anthropic-ai/sdk')), 'internal/utils/path.js'))
const build = new Function('path', 'buildHeaders', `
const Stream=class{},DEFAULT_VERSION='bedrock-2023-05-31',MODEL_ENDPOINTS=new Set(['/v1/complete','/v1/messages','/v1/messages?beta=true']),isObj=x=>x!==null&&typeof x==='object'&&!Array.isArray(x);
class Base{async buildRequest(options){return options}}
return new(class extends Base {${source.slice(method.start, method.end)}})();`)(path, (rows: any[]) => ({ values: new Headers(Object.assign({}, ...rows)) }))
const rows = []
for (const row of input.rows) {
  const options = { method: 'post', path: row.path, body: row.body, headers: row.headers }
  const before = JSON.stringify(options.body)
  try {
    const result = await build.buildRequest(options)
    rows.push({ path: result.path, body: result.body, originalUnchanged: before === JSON.stringify(row.body) })
  } catch (error) { rows.push({ error: error.message }) }
}
console.log(JSON.stringify({ rows }))
