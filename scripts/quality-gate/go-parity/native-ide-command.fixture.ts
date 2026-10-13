import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original IDE command selection')
const source = new Bun.Transpiler({ loader: 'tsx' }).transformSync(await Bun.file(new URL('../../../src/commands/ide/ide.tsx', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
let selection: any
function walk(node: any) {
  if (!node || typeof node !== 'object') return
  if (node.type === 'VariableDeclarator' && node.id.name === 'handleSelectIDE' && node.init?.type === 'CallExpression' && node.init.callee.name === 'useCallback') selection = node.init.arguments[0]
  for (const value of Object.values(node)) if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value)
}
walk(ast)
if (!selection) throw new Error('Original IDE command dynamic MCP callback missing')
const results = input.cases.map((row: any) => {
  const events: any[] = []
  const deps: Record<string, any> = {
    onChangeDynamicMcpConfig: (config: any) => events.push({ config }),
    onDone: (notice: string) => events.push({ notice }),
    dynamicMcpConfig: row.config,
    currentIDE: row.current ?? null,
    ideClient: row.current ? { type: 'connected', config: row.config.ide, client: {} } : undefined,
    clearServerCache: (name: string, config: any) => events.push({ close: name, config }),
    setAppState: (update: any) => events.push({ appState: update({ mcp: { clients: [{ name: 'ide' }, { name: 'other' }], tools: [{ name: 'mcp__ide__getDiagnostics' }, { name: 'mcp__other__echo' }], commands: [{ name: 'mcp__ide__prompt' }, { name: 'mcp__other__prompt' }] } }) }),
    isFirstCheckRef: { current: false },
    setConnectingIDE: (ide: any) => events.push({ connecting: ide }),
  }
  new Function(...Object.keys(deps), `return (${source.slice(selection.start, selection.end)})`)(...Object.values(deps))(row.selected)
  return events
})
process.stdout.write(JSON.stringify(results))
