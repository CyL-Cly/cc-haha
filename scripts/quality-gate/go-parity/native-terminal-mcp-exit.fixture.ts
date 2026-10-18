import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original MCP mounted double-press hook')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_EXIT_SOURCE !== '1') throw new Error('Missing pinned MCP exit Source')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function compile(path: string, dependencies: any, result: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL('../../../' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const code = source.statements.filter((node: any) => !ts.isImportDeclaration(node) && !ts.isTypeAliasDeclaration(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
  return new Function(...Object.keys(dependencies), js + ';return ' + result)(...Object.values(dependencies))
}
const mounts = ['MCPStdioServerMenu', 'MCPRemoteServerMenu', 'MCPListPanel', 'MCPToolListView', 'MCPToolDetailView', 'MCPAgentServerMenu']
for (const name of mounts) {
  const source = fs.readFileSync(new URL('../../../src/components/mcp/' + name + '.tsx', import.meta.url), 'utf8').split('//# sourceMappingURL=')[0]!
  if (!(source.includes('const exitState = useExitOnCtrlCDWithKeybindings()') || source.includes('<Dialog '))) throw new Error('Actual MCP mounted exit hook changed: ' + name)
}
const observations = input.profiles.map((profile: any) => {
  let values: any[] = [], cursor = 0, now = 10000, sequence = 0, exits = 0, handlers: any
  const timers = new Map<number, { at: number, callback: Function, args: any[] }>()
  const cleanup: Function[] = []
  const hooks = {
    useRef: (initial: any) => { const slot = cursor++; return values[slot] ??= { current: initial } },
    useState: (initial: any) => { const slot = cursor++; if (!(slot in values)) values[slot] = initial; return [values[slot], (value: any) => values[slot] = typeof value === 'function' ? value(values[slot]) : value] },
    useCallback: (fn: any) => fn, useMemo: (fn: any) => fn(), useEffect: (fn: any) => { const result = fn(); if (result) cleanup.push(result) }
  }
  const timerPrimitives = {
    Date: { now: () => now },
    setTimeout: (callback: Function, delay: number, ...args: any[]) => { const id = ++sequence; timers.set(id, { at: now + delay, callback, args }); return id },
    clearTimeout: (id: number) => timers.delete(id)
  }
  const useDoublePress = compile('src/hooks/useDoublePress.ts', { ...hooks, ...timerPrimitives }, 'useDoublePress')
  const useExit = compile('src/hooks/useExitOnCtrlCD.ts', { ...hooks, useDoublePress, useApp: () => ({ exit: () => exits++ }) }, 'useExitOnCtrlCD')
  const render = () => { cursor = 0; return useExit((registered: any, options: any) => { if (options.context !== 'Global' || options.isActive !== true) throw new Error('Original MCP exit context changed'); handlers = registered }) }
  let state = render()
  const steps = profile.steps.map((step: any) => {
    now = 10000 + step.at
    for (const [id, timer] of timers) if (timer.at <= now) { timers.delete(id); timer.callback(...timer.args) }
    if (step.key) handlers[step.key === 'ctrl+c' ? 'app:interrupt' : 'app:exit']()
    if (step.unmount) { cleanup.forEach(fn => fn()); values = []; state = render() }
    else state = render()
    return { pending: state.pending, keyName: state.keyName, exits }
  })
  cleanup.forEach(fn => fn())
  return { steps, timersAfterUnmount: timers.size }
})
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ mounts, observations }), error => error ? reject(error) : resolve()))
