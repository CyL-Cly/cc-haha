import { parse } from 'acorn'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import * as React from 'react'
import { jsxDEV } from 'react/jsx-dev-runtime'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Doctor original React consumer')
const raw = await Bun.file(new URL('../../../../src/screens/Doctor.tsx', import.meta.url)).text()
const source = new Bun.Transpiler({ loader: 'tsx', jsxFactory: 'React.createElement', jsxFragment: 'React.Fragment' }).transformSync(raw)
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const body = ast.body.flatMap((n: any) => n.type === 'ExportNamedDeclaration' ? [n.declaration] : [n]).filter((n: any) => n?.type === 'FunctionDeclaration').map((n: any) => source.slice(n.start, n.end)).join('\n')
const results = []
for (const action of input.actions) {
  const callbacks: any[] = []
  let bindings: any = {}
  let context: any
  let index = 0
  const state = [input.diagnostic, { activeAgents: [], failedFiles: [] }, null, { enabled: false }]
  const deps: Record<string, any> = { React, _c: (length: number) => Array(length).fill(Symbol.for('react.memo_cache_sentinel')), Box: 'Box', Text: 'Text', Pane: 'Pane', Suspense: React.Suspense, useState: () => [state[index++], () => {}], useMemo: (fn: Function) => fn(), useCallback: (fn: Function) => fn, useEffect: () => {}, use: () => ({ latest: null, stable: null }), useExitOnCtrlCDWithKeybindings: () => {}, useAppState: (selector: Function) => selector({ agentDefinitions: { activeAgents: [], allAgents: [] }, mcp: { tools: [] }, toolPermissionContext: {}, plugins: { errors: [] } }), useSettingsErrors: () => [], useKeybindings: (handlers: any, options: any) => { bindings = handlers; context = options }, getDoctorDiagnostic: async () => input.diagnostic, getGcsDistTags: async () => ({ latest: null, stable: null }), getNpmDistTags: async () => ({ latest: null, stable: null }), getInitialSettings: () => ({}), BASH_MAX_OUTPUT_DEFAULT: 30000, BASH_MAX_OUTPUT_UPPER_LIMIT: 150000, TASK_MAX_OUTPUT_DEFAULT: 32000, TASK_MAX_OUTPUT_UPPER_LIMIT: 160000, getModelMaxOutputTokens: () => ({ default: 64000, upperLimit: 128000 }), validateBoundedIntEnvVar: () => ({ effective: 0, status: 'valid' }), PressEnterToContinue: 'PressEnterToContinue', SandboxDoctorSection: 'SandboxDoctorSection', ValidationErrorsList: 'ValidationErrorsList', KeybindingWarnings: 'KeybindingWarnings', McpParsingWarnings: 'McpParsingWarnings', figures: { tick: '✔', cross: '✖' }, process: { env: {} } }
  for (const node of ast.body) { if (node.type === 'ImportDeclaration' && /^react\/jsx/.test(node.source.value)) { for (const specifier of node.specifiers) { deps[specifier.local.name] = specifier.imported?.name === 'Fragment' ? React.Fragment : (type: any, props: any, key: any) => React.createElement(type, { ...props, key }) } } }
  for (const name of source.match(/\bjsxDEV_[A-Za-z0-9_]+/g) ?? []) deps[name] = jsxDEV
  for (const name of source.match(/\bFragment_[A-Za-z0-9_]+/g) ?? []) deps[name] = React.Fragment
  const Doctor = new Function(...Object.keys(deps), body + ';return Doctor')(...Object.values(deps))
  const element = Doctor({ onDone: (...args: any[]) => callbacks.push(args) })
  bindings[action]?.()
  const text = (node: any): string => typeof node === 'string' || typeof node === 'number' ? String(node) : Array.isArray(node) ? node.map(text).join('') : node && typeof node === 'object' ? text(node.props?.children) : ''
  results.push({ callbacks, context, text: text(element) })
}
process.stdout.write(JSON.stringify(results))
