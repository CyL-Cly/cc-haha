import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original agent MCP declarations and actual list scope ordering')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_AGENTS_SOURCE !== '1') throw new Error('Missing owned pinned agent MCP Source')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const parse = (path: string) => ts.createSourceFile(path, fs.readFileSync(new URL('../../../' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const transpile = (value: string) => ts.transpileModule(value, { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const utils = parse('src/services/mcp/utils.ts')
const functions = ['isStdioConfig', 'isSSEConfig', 'isHTTPConfig', 'isWebSocketConfig', 'extractAgentMcpServers']
const extract = new Function(transpile(functions.map(name => {
  const declaration = utils.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === name)
  if (!declaration) throw new Error('Original agent MCP function changed: ' + name)
  return declaration.getText(utils).replace(/^export /, '')
}).join('\n')) + ';return extractAgentMcpServers')()
const stringUtils = parse('src/utils/stringUtils.ts')
const capitalizeDeclaration = stringUtils.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'capitalize')
if (!capitalizeDeclaration) throw new Error('Original MCP capitalize helper changed')
const capitalize = new Function(transpile(capitalizeDeclaration.getText(stringUtils).replace(/^export /, '')) + ';return capitalize')()
const panel = parse('src/components/mcp/MCPListPanel.tsx')
const listFunctions = ['groupServersByScope', '_temp', '_temp2', '_temp3', '_temp4']
let listCode = listFunctions.map(name => {
  const declaration = panel.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === name)
  if (!declaration) throw new Error('Original MCP list helper changed: ' + name)
  return declaration.getText(panel)
}).join('\n')
const scopes = panel.statements.find((node: any) => ts.isVariableStatement(node) && node.declarationList.declarations.some((value: any) => value.name.text === 'SCOPE_ORDER'))
const component = panel.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'MCPListPanel')
const items = component?.body.statements.find((node: any) => ts.isIfStatement(node) && node.expression.getText(panel).includes('$[9] !== agentServers'))
if (!scopes || !items) throw new Error('Original MCP list registration changed')
// Execute the original compiled component's selection body; remove only its
// React memo-slot publication, which is not part of the item computation.
const body = items.thenStatement.statements.filter((node: any) => !node.getText(panel).startsWith('$[')).map((node: any) => node.getText(panel)).join('\n')
listCode += '\n' + scopes.getText(panel)
const order = new Function('servers', 'agentServers', transpile(listCode) + `
  const serversByScope=groupServersByScope(servers.filter(_temp));
  const claudeAiServers=servers.filter(_temp2).sort(_temp3);
  const dynamicServers=(serversByScope.get('dynamic')??[]).sort(_temp4);
  let items; ${transpile(body)}
  return items.map(item=>({name:(item.server??item.agentServer).name,agentOnly:item.type==='agent-server'}));
`)
const output = input.profiles.map((profile: any) => {
  const agents = extract(profile.agents)
  // This is the unchanged MCPSettings filteredClients boundary before the
  // original MCPListPanel component receives its props.
  const servers = profile.servers.filter((server: any) => server.name !== 'ide').map((server: any) => ({ ...server, client: { config: server.config } }))
  return { agents, order: order(servers, agents), capitalized: (input.names ?? []).map((name: string) => Buffer.from(capitalize(name), 'utf8').toString('utf8')) }
})
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(output), error => error ? reject(error) : resolve()))
