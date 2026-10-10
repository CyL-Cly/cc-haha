import { readFile, writeFile, mkdir, stat } from 'node:fs/promises'
import { join } from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'QQ API consumer')
if (process.env.CC_HAHA_QQ_API_FIXTURE !== '1') throw new Error('Unowned QQ API fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const require = createRequire(new URL('../../../desktop/package.json', import.meta.url))
const ts = require('typescript')
const path = new URL('../../../src/server/api/adapters.ts', import.meta.url)
const text = await readFile(path, 'utf8')
const ast = ts.createSourceFile(path.pathname, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const names = new Set(['readSessionKey', 'createQrDataUrl', 'handleQqAdaptersApi'])
const chunks: string[] = []
for (const node of ast.statements) if (ts.isFunctionDeclaration(node) && names.has(node.name?.text)) chunks.push(node.getText(ast))
if (chunks.length !== names.size) throw new Error('Original handler declarations missing')
const transformer = (context: any) => {
  const visit = (node: any): any => ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword && node.arguments[0]?.text === 'qrcode' ? ts.factory.createCallExpression(ts.factory.createPropertyAccessExpression(ts.factory.createIdentifier('Promise'), 'resolve'), undefined, [ts.factory.createIdentifier('qrModule')]) : ts.visitEachChild(node, visit, context)
  return (node: any) => ts.visitNode(node, visit)
}
const js = ts.transpileModule(chunks.join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext }, transformers: { before: [transformer] } }).outputText
const { adapterService } = await import('../../../src/server/services/adapterService')
const { ApiError, errorResponse } = await import('../../../src/server/middleware/errorHandler')
const calls: any[] = []
let row: any
const begin = async (options: any) => { calls.push({ action: 'start', options }); if (row.error) throw new Error(row.error); return row.result }
const poll = (options: any) => { calls.push({ action: 'poll', options }); if (row.error) throw new Error(row.error); return row.result }
const cancel = (key: any) => { calls.push({ action: 'cancel', key }) }
const qrModule = require('qrcode')
const handler = new Function('qrModule', 'adapterService', 'ApiError', 'isRecord', 'startQqQrLogin', 'pollQqQrLogin', 'cancelQqQrLogin', `${js}; return handleQqAdaptersApi`)(qrModule, adapterService, ApiError, (value: any) => !!value && typeof value === 'object' && !Array.isArray(value), begin, poll, cancel)
await mkdir(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
const output = []
for (row of input.rows) {
  calls.length = 0
  await writeFile(file, JSON.stringify(row.seed), { mode: 0o600 })
  const url = new URL(`http://private.test/api/adapters/qq/${row.tail.join('/')}`)
  let response
  try { response = await handler(new Request(url, { method: row.method ?? 'POST', ...(row.method === 'GET' ? {} : { body: row.raw ?? '{}' }) }), row.tail) }
  catch (error) { response = errorResponse(error) }
  const body = await response.json()
  if (body.qrDataUrl) { body.qr = body.qrDataUrl.startsWith('data:image/png;base64,'); delete body.qrDataUrl }
  const stored = JSON.parse(await readFile(file, 'utf8'))
  output.push({ status: response.status, body, calls: [...calls], raw: stored, storedJson: JSON.stringify(stored), mode: (await stat(file)).mode & 0o777 })
}
process.stdout.write(JSON.stringify(output))
