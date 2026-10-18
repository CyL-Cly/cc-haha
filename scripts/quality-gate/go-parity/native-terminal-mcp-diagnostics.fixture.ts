import fs from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual MCP parsing diagnostics and mounted Ink component')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_DIAGNOSTICS_SOURCE !== '1' || input.cwd !== join(home, 'work') || input.managed !== join(home, 'managed')) throw new Error('Missing MCP diagnostics Source inputs')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const absentPaths = absentManagedSettingsFixturePaths()
for (let parent = dirname(home);; parent = dirname(parent)) {
  for (const name of ['.mcp.json', '.git']) {
    const path = join(parent, name)
    if (fs.existsSync(path)) throw new Error('Source diagnostic ancestor is not absent')
    absentPaths.push(path)
  }
  if (dirname(parent) === parent) break
}
const rg = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(rg)) absentPaths.push(rg)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths })
await import('../../../preload')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = input.managed
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== input.managed) throw new Error('Source managed path escaped')
process.env.USER_TYPE = 'external'
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd); state.setProjectRoot(input.cwd); state.setCwdState(input.cwd)
state.setAllowedSettingSources(input.sources)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const api = await import('../../../src/services/mcp/config')
const utils = await import('../../../src/services/mcp/utils')
const React = await import('react')
const { c: _c } = await import('react/compiler-runtime')
const { Box, Text, Link } = await import('../../../src/ink')
const { renderToAnsiString } = await import('../../../src/utils/staticRender')
const file = new URL('../../../src/components/mcp/McpParsingWarnings.tsx', import.meta.url)
const source = ts.createSourceFile(file.pathname, fs.readFileSync(file, 'utf8').replace(/\/\/# sourceMappingURL=.*$/gm, ''), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const body = source.statements.filter((node: any) => !ts.isImportDeclaration(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
const code = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
const scopes: unknown[] = []
const calls: string[] = []
const deps = { React, _c, Box, Text, Link, useMemo: React.useMemo,
  describeMcpConfigFilePath: utils.describeMcpConfigFilePath, getScopeLabel: utils.getScopeLabel,
  getMcpConfigsByScope(scope: 'user' | 'project' | 'local' | 'enterprise') {
    calls.push(scope)
    const config = api.getMcpConfigsByScope(scope)
    scopes.push({ scope, path: utils.describeMcpConfigFilePath(scope), label: utils.getScopeLabel(scope), errors: config.errors.filter(error => error.mcpErrorMetadata?.severity === 'fatal'), warnings: config.errors.filter(error => error.mcpErrorMetadata?.severity === 'warning') })
    return config
  }
}
const Component = new Function(...Object.keys(deps), code + '\nreturn McpParsingWarnings')(...Object.values(deps))
const rendered = await renderToAnsiString(React.createElement(Component), 180)
if (capabilities.blocked.length) throw new Error('Source MCP diagnostics escaped private fixture: ' + JSON.stringify(capabilities))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ scopes, calls, rendered, capabilities }), error => error ? reject(error) : resolve()))
