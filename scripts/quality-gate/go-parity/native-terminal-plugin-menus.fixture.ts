import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original MCP menus and useManagePlugins error merge')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_MENUS_SOURCE !== '1') throw new Error('Missing menus Source guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const parse = (path: string) => ts.createSourceFile(path, fs.readFileSync(new URL('../../../' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const transpile = (code: string) => ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
function menuFunction(path: string) {
  const source = parse(path)
  const component = source.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === (path.includes('Stdio') ? 'MCPStdioServerMenu' : 'MCPRemoteServerMenu'))
  if (!component) throw new Error('Original MCP menu missing')
  const statements = component.body.statements
  const index = statements.findIndex((node: any) => ts.isVariableStatement(node) && node.declarationList.declarations.some((d: any) => d.name.text === 'menuOptions'))
  let code = ''
  for (let i = index; i < statements.length; i++) {
    const node = statements[i]
    if (i > index && (!(ts.isIfStatement(node) || ts.isExpressionStatement(node)) || !node.getText(source).includes('menuOptions.push'))) break
    code += node.getText(source) + '\n'
  }
  if (!code.includes('toggle-enabled')) throw new Error('Original menu registration changed')
  return new Function('server', 'serverToolsCount', 'isEffectivelyAuthenticated', transpile(code) + ';return menuOptions')
}
const stdio = menuFunction('src/components/mcp/MCPStdioServerMenu.tsx')
const remote = menuFunction('src/components/mcp/MCPRemoteServerMenu.tsx')
const hook = parse('src/hooks/useManagePlugins.ts')
let merge: any
function visit(node: any) {
  if (ts.isCallExpression(node) && node.expression.getText(hook) === 'setAppState' && node.arguments[0]?.getText(hook).includes('newErrorKeys')) merge = node.arguments[0]
  ts.forEachChild(node, visit)
}
visit(hook)
if (!merge) throw new Error('Original plugin error merge changed')
const mergeErrors = new Function('prevState', 'errors', 'enabled', 'disabled', 'commands', transpile('const merge=' + merge.getText(hook)) + ';return merge(prevState).plugins.errors')
const output = {
  menus: input.menus.map((row: any) => {
    const server = { ...row, client: { type: row.status }, config: row.config }
    const count = row.toolMetadata.length
    return (row.config.type === 'stdio' ? stdio : remote)(server, count, server.isAuthenticated || row.status === 'connected' && count > 0)
  }),
  errors: input.errors.map((row: any) => mergeErrors({ plugins: { errors: row.previous } }, row.incoming, [], [], []))
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(output), error => error ? reject(error) : resolve()))
