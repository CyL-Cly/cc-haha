import fs from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original PromptInput companion footer handlers with real Ink keybindings')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BUDDY_SOURCE !== '1') throw new Error('Pinned companion Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = ts.createSourceFile('PromptInput.tsx', fs.readFileSync(new URL('../../../src/components/PromptInput/PromptInput.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const functions = new Map<string, any>()
let footerBindings: any, typeToExit: any
function visit(node: any) {
  if (ts.isFunctionDeclaration(node) && ['selectFooterItem', 'navigateFooter', 'handleHistoryDown'].includes(node.name?.text)) functions.set(node.name.text, node)
  if (ts.isCallExpression(node) && node.expression.getText(source) === 'useKeybindings' && node.arguments[1]?.getText(source).includes("context: 'Footer'")) footerBindings = node
  if (ts.isIfStatement(node) && node.expression.getText(source).startsWith('footerItemSelected && char &&')) typeToExit = node
  ts.forEachChild(node, visit)
}
visit(source)
if (functions.size !== 3 || !footerBindings || !typeToExit) throw new Error('Original footer consumers missing')
const code = new Bun.Transpiler({ loader: 'tsx' }).transformSync(`function Consumer() {
 const rawFooterSelection = useAppState(s => s.footerSelection);
 const footerItemSelected = rawFooterSelection && footerItems.includes(rawFooterSelection) ? rawFooterSelection : null;
 const tasksSelected = false;
 const setAppState = useSetAppState();
 ${[...functions.values()].map(n => n.getText(source)).join('\n')}
 ${footerBindings.getText(source)};
 useInput((char, key) => {
   if (!footerItemSelected && (key.downArrow || key.ctrl && char === 'n')) { handleHistoryDown(); return; }
   ${typeToExit.getText(source)}
 });
 report(footerItemSelected);
 return React.createElement(CompanionSprite);
}`)
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-buddy-footer-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home); bootstrap.setAllowedSettingSources(['userSettings']); bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render, useInput } = await import('../../../src/ink')
const { AppStateProvider, getDefaultAppState, useAppState, useSetAppState } = await import('../../../src/state/AppState')
const { CompanionSprite } = await import('../../../src/buddy/CompanionSprite')
const { useKeybindings } = await import('../../../src/keybindings/useKeybinding')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 75)) }
const results = []
for (const row of input.rows) {
  config.saveGlobalConfig(current => ({ ...current, companion: row.companion, companionMuted: false }))
  let selected: string | null = null, draft = row.draft ?? '', cursor = draft.length
  const submitted: string[] = [], observations: any[] = []
  const forbidden = () => { throw new Error('Companion-only profile called a different footer consumer') }
  const deps = { React, useAppState, useSetAppState, useInput, useKeybindings, CompanionSprite, footerItems: ['companion'], suggestions: Array(row.suggestions ?? 0), isCursorOnLastLine: row.lastLine !== false, onHistoryDown: () => row.bottom !== false,
    coordinatorTaskCount: 0, coordinatorTaskIndex: 0, minCoordinatorIndex: 0, setTeammateFooterIndex: forbidden, setCoordinatorTaskIndex: forbidden, getGlobalConfig: config.getGlobalConfig, saveGlobalConfig: config.saveGlobalConfig,
    isTeammateMode: false, inProcessTeammates: [], setShowBashesDialog: forbidden, viewSelectionMode: row.selecting ? 'selecting-agent' : 'normal', enterTeammateView: forbidden, exitTeammateView: forbidden, getVisibleAgentTasks: forbidden, setShowTeamsDialog: forbidden, setShowBridgeDialog: forbidden, stopOrDismissAgent: forbidden,
    onSubmit: (value: string) => submitted.push(value), get input() { return draft }, get cursorOffset() { return cursor }, onChange: (value: string) => { draft = value; selected = null }, setCursorOffset: (value: number) => cursor = value, report: (value: any) => selected = value, isModalOverlayActive: false }
  // input/cursor are render-time bindings in the real function; rebuild after
  // each observed key while retaining the actual AppState store and provider.
  let Consumer: any
  function MountedConsumer() {
    const setAppState = useSetAppState()
    const actual = { ...deps, input: draft, cursorOffset: cursor, onChange: (value: string) => { draft = value; setAppState(prev => ({ ...prev, footerSelection: null })) } }
    Consumer = new Function(...Object.keys(actual), code + '\nreturn Consumer')(...Object.values(actual))
    return Consumer()
  }
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = 100; stdout.on('data', () => {})
  const pending = { current: null }, handlers = { current: new Map() }, active = new Set(['Footer'])
  const provider = { bindings: parseBindings([...DEFAULT_BINDINGS, ...(row.bindings ?? [])]), pendingChordRef: pending, pendingChord: null, setPendingChord: (value: any) => pending.current = value, handlerRegistryRef: handlers, activeContexts: active, registerActiveContext: (value: any) => active.add(value), unregisterActiveContext: (value: any) => active.delete(value) }
  const app = await render(React.createElement(AppStateProvider, { initialState: { ...getDefaultAppState(), footerSelection: row.focused ? 'companion' : null } }, React.createElement(KeybindingProvider, provider as any, React.createElement(MountedConsumer))), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  await settle()
  for (const key of row.keys) {
    stdin.write(key); await settle()
    observations.push({ focused: selected === 'companion', draft, submitted: [...submitted] })
  }
  app.unmount(); await settle()
  results.push(observations)
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Companion footer Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
