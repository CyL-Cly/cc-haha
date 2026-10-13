import { parse } from 'acorn'
import * as React from 'react'
import { jsxDEV } from 'react/jsx-dev-runtime'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original Doctor sandbox/configuration boundaries')
async function declarations(path: string, names?: string[], loader: 'ts' | 'tsx' = 'ts') {
  const source = new Bun.Transpiler({ loader }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const body = ast.body.flatMap((node: any) => node.type === 'ExportNamedDeclaration' ? [node.declaration] : [node])
    .filter((node: any) => node?.type === 'FunctionDeclaration' && (!names || names.includes(node.id.name)))
    .map((node: any) => source.slice(node.start, node.end)).join('\n')
  return { source, body }
}
const sandbox = await declarations('../../../src/components/sandbox/SandboxDoctorSection.tsx', undefined, 'tsx')
const glob = await declarations('../../../src/utils/sandbox/sandbox-adapter.ts', ['getLinuxGlobPatternWarnings'])
const parser = await declarations('../../../src/utils/permissions/permissionRuleParser.ts', ['permissionRuleValueFromString', 'normalizeLegacyToolName', 'unescapeRuleContent', 'findFirstUnescapedChar', 'findLastUnescapedChar'])
const keybinding = await declarations('../../../src/keybindings/loadUserBindings.ts', ['isKeybindingCustomizationEnabled'])
const toolSearch = await declarations('../../../src/utils/toolSearch.ts', ['isToolSearchToolAvailable', 'isToolSearchEnabled'])
function text(node: any): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(text).join('')
  return node && typeof node === 'object' ? text(node.props?.children) : ''
}
const results = []
for (const row of input.cases) {
  if (row.kind === 'sandbox') {
    const deps: Record<string, any> = { React, Box: 'Box', Text: 'Text', _c: (size: number) => Array(size).fill(Symbol.for('react.memo_cache_sentinel')), SandboxManager: { isSupportedPlatform: () => row.supported, isSandboxEnabledInSettings: () => row.enabled, checkDependencies: () => row.check } }
    for (const name of sandbox.source.match(/\bjsxDEV_[A-Za-z0-9_]+/g) ?? []) deps[name] = jsxDEV
    const component = new Function(...Object.keys(deps), sandbox.body + ';return SandboxDoctorSection')(...Object.values(deps))
    results.push({ text: text(component()) })
  } else if (row.kind === 'glob') {
    const deps = { getPlatform: () => row.platform, getSettings_DEPRECATED: () => row.settings, FILE_EDIT_TOOL_NAME: 'Edit', FILE_READ_TOOL_NAME: 'Read', LEGACY_TOOL_NAME_ALIASES: { Task: 'Agent', KillShell: 'TaskStop' }, logForDebugging: () => {} }
    const fn = new Function(...Object.keys(deps), parser.body + glob.body + ';return getLinuxGlobPatternWarnings')(...Object.values(deps))
    results.push({ patterns: fn() })
  } else if (row.kind === 'keybinding') {
    const calls: any[] = []
    const fn = new Function('getFeatureValue_CACHED_MAY_BE_STALE', keybinding.body + ';return isKeybindingCustomizationEnabled')((name: string, fallback: boolean) => { calls.push([name, fallback]); return row.gate ?? fallback })
    results.push({ enabled: fn(), calls })
  } else if (row.kind === 'mcp_only') {
    // Model compatibility and mode are the two native feature primitives;
    // the actual consumer must still reject a catalog without ToolSearch.
    const deps = { count: (tools: any[], predicate: Function) => tools.filter(t => predicate(t)).length, modelSupportsToolReference: () => true, toolMatchesName: (tool: any, name: string) => tool.name === name, TOOL_SEARCH_TOOL_NAME: 'ToolSearch', logEvent: () => {}, logForDebugging: () => {}, getToolSearchMode: () => 'tst', process: { env: {} } }
    const fn = new Function(...Object.keys(deps), toolSearch.body + ';return isToolSearchEnabled')(...Object.values(deps))
    results.push({ enabled: await fn('source-compatible-model', row.tools, async () => ({}), [], 'doctor') })
  } else throw new Error('Unknown actual Doctor boundary')
}
process.stdout.write(JSON.stringify(results))
