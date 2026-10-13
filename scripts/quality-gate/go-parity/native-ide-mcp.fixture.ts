import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native IDE MCP original transport')
if (process.env.CC_HAHA_IDE_MCP_FIXTURE !== '1') throw new Error('Unowned IDE transport fixture')
await import('../../../preload')
const { WebSocketTransport } = await import('../../../src/utils/mcpWebSocketTransport')
const { Client } = await import('@modelcontextprotocol/sdk/client/index.js')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/services/mcp/client.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
let branch: any
const walk = (node: any) => {
  if (!node || typeof node !== 'object') return
  if (node.type === 'IfStatement' && node.test.type === 'BinaryExpression' && node.test.right.value === input.config.type && source.slice(node.test.left.start, node.test.left.end) === 'serverRef.type') branch = node.consequent
  for (const value of Object.values(node)) if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value)
}
walk(ast)
if (!branch) throw new Error('Actual Source IDE transport branch missing')
const makeTransport = new Function('serverRef', 'getWebSocketTLSOptions', 'getMCPUserAgent', 'getWebSocketProxyUrl', 'createNodeWsClient', 'WebSocketTransport', 'getMcpServerHeaders', 'sessionIngressToken', 'logMCPDebug', 'name', 'mapValues', 'jsonStringify', `return async () => { let transport; ${source.slice(branch.start, branch.end)}; return transport }`)
const transport = await makeTransport(input.config, () => undefined, () => input.userAgent, () => undefined, () => { throw new Error('Unexpected Node path under pinned Bun') }, WebSocketTransport, async () => input.config.headers ?? {}, input.sessionIngressToken, () => {}, 'physical-ws', (object: any, fn: any) => Object.fromEntries(Object.entries(object).map(([k, v]) => [k, fn(v, k)])), JSON.stringify)()
const client = new Client({ name: 'claude-code', title: 'Claude Code', version: 'fixture', description: "Anthropic's agentic coding tool", websiteUrl: 'https://claude.com/claude-code' }, { capabilities: { roots: {}, elicitation: {} } })
await client.connect(transport)
const ideSource = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/ide.ts', import.meta.url)).text())
const ideAst: any = parse(ideSource, { ecmaVersion: 'latest', sourceType: 'module' })
const notify = ideAst.body.find((n: any) => n.type === 'ExportNamedDeclaration' && n.declaration?.id?.name === 'maybeNotifyIDEConnected')?.declaration
if (!notify) throw new Error('Actual Source IDE connected notification missing')
if (input.config.type === 'ws-ide') await new Function('process', `${ideSource.slice(notify.start, notify.end)};return maybeNotifyIDEConnected`)({ pid: 170123 })(client)
const tools = await client.listTools()
const result = await client.callTool({ name: 'actual_ide_echo', arguments: { input: '原始 IDE 😀' } })
await client.close()
process.stdout.write(JSON.stringify({ tools, result, binaryType: (transport as any).ws.binaryType }))
