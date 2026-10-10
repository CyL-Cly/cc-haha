import { parse } from 'acorn'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'CLI MCP Desktop import Source')
if (process.env.CC_HAHA_CLI_MCP_DESKTOP_FIXTURE !== '1') throw new Error('Unowned Desktop import fixture')
await import('../../../preload')
const { McpStdioServerConfigSchema } = await import('../../../src/services/mcp/types')
const { safeParseJSON } = await import('../../../src/utils/json')
const { getErrnoCode } = await import('../../../src/utils/errors')
const jsxBindings = new Map<string, string>()
const extract = async (path: string, names: string[]) => {
  const source = new Bun.Transpiler({ loader: path.endsWith('tsx') ? 'tsx' : 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  for (const node of ast.body) if (node.type === 'ImportDeclaration' && String(node.source.value).includes('react/jsx')) {
    for (const specifier of node.specifiers) jsxBindings.set(specifier.local.name, specifier.imported?.name)
  }
  // Bun's standalone Transpiler also emits implicit JSX runtime aliases.
  // Bind only aliases actually present in this parsed rendering scaffold.
  const walk = (node: any) => {
    if (!node || typeof node !== 'object') return
    if (node.type === 'Identifier' && /^jsx(?:DEV|s)?_/.test(node.name)) jsxBindings.set(node.name, 'jsx')
    if (node.type === 'Identifier' && /^Fragment_/.test(node.name)) jsxBindings.set(node.name, 'Fragment')
    for (const value of Object.values(node)) if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value)
  }
  walk(ast)
  const nodes = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n)
  return names.map(name => {
    const node = nodes.find((n: any) => n.type === 'FunctionDeclaration' && n.id.name === name)
    if (!node) throw new Error('Missing actual Source ' + name)
    return source.slice(node.start, node.end)
  }).join('\n')
}
const reader = await extract('../../../src/utils/claudeDesktop.ts', ['getClaudeDesktopConfigPath', 'readClaudeDesktopMcpServers'])
const dialog = await extract('../../../src/components/MCPServerDesktopImportDialog.tsx', ['MCPServerDesktopImportDialog'])
const rows = []
for (const row of input.rows) {
  const calls: any[] = []
  const deps = {
    join, homedir: () => input.homeDir, getPlatform: () => row.platform, SUPPORTED_PLATFORMS: ['macos', 'wsl'],
    McpStdioServerConfigSchema, safeParseJSON, getErrnoCode, logError: () => {},
    stat: async (path: string) => { calls.push({ op: 'stat', path }); if (row.present?.includes(path)) return {}; throw Object.assign(new Error('missing'), { code: 'ENOENT' }) },
    readdir: async (path: string) => { calls.push({ op: 'readdir', path }); if (row.users) return row.users.map((name: string) => ({ name })); throw new Error('missing directory') },
    readFile: async (path: string) => { calls.push({ op: 'read', path }); if (row.content !== undefined) return row.content; throw Object.assign(new Error('missing'), { code: row.readError ?? 'ENOENT' }) },
  }
  if (row.userProfile === undefined) delete process.env.USERPROFILE
  else process.env.USERPROFILE = row.userProfile
  const funcs = new Function('deps', `with(deps){${reader}\nreturn {getClaudeDesktopConfigPath,readClaudeDesktopMcpServers}}`)(deps)
  let path, pathError, servers, readError
  try { path = await funcs.getClaudeDesktopConfigPath() } catch (e: any) { pathError = e.message }
  calls.length = 0
  try { servers = await funcs.readClaudeDesktopMcpServers() } catch (e: any) { readError = e.message }
  rows.push({ path: path ?? null, pathError: pathError ?? null, servers: servers ?? null, readError: readError ?? null, calls })
}
const selections = []
for (const row of input.selections ?? []) {
  let selection: any
  const mutations: any[] = [], output: string[] = []
  const createElement = (kind: any, props: any, ...children: any[]) => { if (kind === 'select') selection = props; return { kind, props, children } }
  const deps = {
    _c: (n: number) => new Array(n).fill(Symbol.for('react.memo_cache_sentinel')),
    React: { createElement, Fragment: 'fragment' },
    useState: () => [row.existing ?? {}, () => {}], useEffect: () => {}, useTheme: () => ['fixture'],
    getAllMcpConfigs: async () => ({ servers: row.existing ?? {} }),
    addMcpConfig: async (name: string, config: any, scope: string) => mutations.push({ name, config, scope }),
    color: () => (s: string) => s, plural: (n: number, s: string) => n === 1 ? s : s + 's',
    writeToStdout: (s: string) => output.push(s), gracefulShutdown: () => {},
    SelectMulti: 'select', Text: 'text', Box: 'box', Dialog: 'dialog', Byline: 'byline', KeyboardShortcutHint: 'shortcut', ConfigurableShortcutHint: 'hint',
  }
  for (const [local, imported] of jsxBindings) (deps as any)[local] = imported === 'Fragment' ? 'fragment' : (kind: any, props: any) => createElement(kind, props)
  const render = new Function('deps', `with(deps){${dialog}\nreturn MCPServerDesktopImportDialog}`)(deps)
  render({ servers: row.servers, scope: row.scope, onDone() {} })
  if (!selection) throw new Error('Actual Source selection not rendered')
  const defaults = selection.defaultValue
  if (row.cancel) selection.onCancel()
  else await selection.onSubmit(row.selected ?? defaults)
  selections.push({ defaults, mutations, output: output.join('') })
}
process.stdout.write(JSON.stringify({ rows, selections }))
