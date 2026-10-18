import fs from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original PromptInput footer item getters and mounted keyboard handlers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_FOOTER_SOURCE !== '1') throw new Error('Pinned footer Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = ts.createSourceFile('PromptInput.tsx', fs.readFileSync(new URL('../../../src/components/PromptInput/PromptInput.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const functions = new Map<string, any>(), declarations = new Map<string, any>()
let footerBindings: any, typeToExit: any
const names = ['runningTaskCount', 'tasksFooterVisible', 'teamsFooterVisible', 'footerItems', 'rawFooterSelection', 'footerItemSelected', 'tasksSelected']
function visit(node: any) {
  if (ts.isFunctionDeclaration(node) && ['selectFooterItem', 'navigateFooter', 'handleHistoryDown'].includes(node.name?.text)) functions.set(node.name.text, node)
  if (ts.isVariableDeclaration(node) && names.includes(node.name?.text)) declarations.set(node.name.text, node)
  if (ts.isCallExpression(node) && node.expression.getText(source) === 'useKeybindings' && node.arguments[1]?.getText(source).includes("context: 'Footer'")) footerBindings = node
  if (ts.isIfStatement(node) && node.expression.getText(source).startsWith('footerItemSelected && char &&')) typeToExit = node
  ts.forEachChild(node, visit)
}
visit(source)
if (functions.size !== 3 || declarations.size !== names.length || !footerBindings || !typeToExit) throw new Error('Original footer consumers missing')
const code = new Bun.Transpiler({ loader: 'tsx' }).transformSync(`function Consumer() {
 const tasks = useAppState(s => s.tasks);
 const viewSelectionMode = useAppState(s => s.viewSelectionMode);
 const viewingAgentTaskId = useAppState(s => s.viewingAgentTaskId);
 const [teammateFooterIndex, setTeammateFooterIndex] = React.useState(0);
 const setAppState = useSetAppState();
 const inProcessTeammates = getRunningTeammatesSorted(tasks);
 const isTeammateMode = inProcessTeammates.length > 0 || tasks[viewingAgentTaskId]?.type === 'in_process_teammate';
 ${names.map(name => 'const ' + declarations.get(name).getText(source) + ';').join('\n')}
 React.useEffect(() => { if (rawFooterSelection && !footerItemSelected) setAppState(prev => ({ ...prev, footerSelection: null })) }, [rawFooterSelection, footerItemSelected]);
 ${[...functions.values()].map(n => n.getText(source)).join('\n')}
 ${footerBindings.getText(source)};
 useInput((char, key) => {
   if (!footerItemSelected && (key.downArrow || key.ctrl && char === 'n')) { handleHistoryDown(); return; }
   ${typeToExit.getText(source)}
 });
 report({ items: footerItems, selection: footerItemSelected, index: teammateFooterIndex, view: viewSelectionMode, viewed: viewingAgentTaskId ?? '' });
 return React.createElement(React.Fragment);
}`)
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-footer-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home); bootstrap.setAllowedSettingSources(['userSettings']); bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { useMemo } = React
const { render, useInput, Box } = await import('../../../src/ink')
const { AppStateProvider, getDefaultAppState, useAppState, useSetAppState } = await import('../../../src/state/AppState')
const { shouldHideTasksFooter } = await import('../../../src/components/tasks/taskStatusUtils')
const { getRunningTeammatesSorted } = await import('../../../src/tasks/InProcessTeammateTask/InProcessTeammateTask')
const { enterTeammateView, exitTeammateView, stopOrDismissAgent } = await import('../../../src/state/teammateViewHelpers')
const { useKeybindings } = await import('../../../src/keybindings/useKeybinding')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const { BackgroundTaskStatus } = await import('../../../src/components/tasks/BackgroundTaskStatus')
const { TeamStatus } = await import('../../../src/components/teams/TeamStatus')
const { ThemeProvider } = await import('../../../src/components/design-system/ThemeProvider')
const { renderToAnsiString } = await import('../../../src/utils/staticRender')
const { Parser } = await import('../../../src/ink/termio')
const instances = (await import('../../../src/ink/instances')).default
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 75)) }
const results = []
for (const row of input.rows ?? []) {
  config.saveGlobalConfig(current => ({ ...current, hasSeenTasksHint: false }))
  let current: any = {}, draft = row.draft ?? '', cursor = draft.length, store: any
  const opened: string[] = [], submitted: string[] = [], observations: any[] = []
  const deps = { React, useMemo, useAppState, useSetAppState, useInput, useKeybindings, shouldHideTasksFooter, getRunningTeammatesSorted, enterTeammateView, exitTeammateView, stopOrDismissAgent,
    cachedTeams: row.teams ?? [], showSpinnerTree: row.expanded === 'teammates', tmuxFooterVisible: false, bagelFooterVisible: false, bridgeFooterVisible: false, companionFooterVisible: row.companion === true,
    count: (values: any[], predicate: any) => values.filter(predicate).length, suggestions: Array(row.suggestions ?? 0), isCursorOnLastLine: row.lastLine !== false, onHistoryDown: () => row.bottom !== false,
    coordinatorTaskCount: 0, coordinatorTaskIndex: -1, minCoordinatorIndex: -1, setCoordinatorTaskIndex: (value: number) => { if (value !== -1) throw new Error('Frozen external Source cannot select coordinator rows') }, getGlobalConfig: config.getGlobalConfig, saveGlobalConfig: config.saveGlobalConfig,
    getVisibleAgentTasks: () => [], setShowBashesDialog: (value: boolean) => { if (value) opened.push('tasks') }, setShowTeamsDialog: (value: boolean) => { if (value) opened.push('teams') }, setShowBridgeDialog: () => { throw new Error('No bridge producer in this profile') },
    onSubmit: (value: string) => submitted.push(value), get input() { return draft }, get cursorOffset() { return cursor }, onChange: (value: string) => { draft = value }, setCursorOffset: (value: number) => cursor = value, report: (value: any) => current = value, isModalOverlayActive: false }
  function MountedConsumer() {
    const setAppState = useSetAppState()
    store = setAppState
    const actual = { ...deps, input: draft, cursorOffset: cursor, onChange: (value: string) => { draft = value; setAppState(prev => ({ ...prev, footerSelection: null })) } }
    const Consumer = new Function(...Object.keys(actual), code + '\nreturn Consumer')(...Object.values(actual))
    return Consumer()
  }
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = 100; stdout.on('data', () => {})
  const pending = { current: null }, handlers = { current: new Map() }, active = new Set(['Footer'])
  const provider = { bindings: parseBindings([...DEFAULT_BINDINGS, ...(row.bindings ?? [])]), pendingChordRef: pending, pendingChord: null, setPendingChord: (value: any) => pending.current = value, handlerRegistryRef: handlers, activeContexts: active, registerActiveContext: (value: any) => active.add(value), unregisterActiveContext: (value: any) => active.delete(value) }
  const app = await render(React.createElement(AppStateProvider, { initialState: { ...getDefaultAppState(), tasks: row.tasks ?? {}, footerSelection: row.selected ?? null, viewSelectionMode: row.selecting ? 'selecting-agent' : 'none' } }, React.createElement(KeybindingProvider, provider as any, React.createElement(MountedConsumer))), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  await settle()
  for (const key of row.keys) {
    if (typeof key === 'object') store(prev => ({ ...prev, tasks: key.tasks }))
    else stdin.write(key)
    await settle()
    observations.push({ ...current, draft, opened: [...opened], submitted: [...submitted], hint: config.getGlobalConfig().hasSeenTasksHint })
  }
  app.unmount(); await settle()
  results.push(observations)
}
const pills = []
const cells = (value: string) => {
  const values: any[] = []
  for (const action of new Parser().feed(value)) if (action.type === 'text') for (const grapheme of action.graphemes) values.push({ text: grapheme.value, style: action.style })
  // A zero-height root still writes the terminal frame's newline. It is not
  // a row painted by either original pill component.
  return values.every(value => value.text === '\n') ? [] : values
}
for (const row of input.pills ?? []) {
  const node = row.team ? React.createElement(TeamStatus, { teamsSelected: row.selected, showHint: true }) : React.createElement(BackgroundTaskStatus, { tasksSelected: row.selected, teammateFooterIndex: row.index ?? 0, isViewingTeammate: row.viewed != null, isLeaderIdle: row.leaderIdle ?? true, onOpenDialog: () => {} })
  const appState = { ...getDefaultAppState(), tasks: row.tasks ?? {}, expandedView: row.expanded ?? 'none', viewingAgentTaskId: row.viewed, teamContext: row.team }
  // Ordinary tasks use the fixed-height caller's flexShrink:0 Box. Teammate
  // pills use its separate row Box and can grow vertically in narrow terminals.
  const teammatePills = Object.values(row.tasks ?? {}).some((task: any) => task.type === 'in_process_teammate')
  const caller = row.team || teammatePills ? React.createElement(Box, null, node) : React.createElement(Box, { height: 1, overflow: 'hidden' }, React.createElement(Box, { flexShrink: 0 }, node))
  const original = await renderToAnsiString(React.createElement(ThemeProvider, { initialState: row.theme }, React.createElement(AppStateProvider, { initialState: appState }, caller)), row.width)
  let layout
  if (row.captureLayout) {
    const stdout = new PassThrough() as any
    stdout.isTTY = false; stdout.columns = row.width; stdout.on('data', () => {})
    const app = await render(React.createElement(ThemeProvider, { initialState: row.theme }, React.createElement(AppStateProvider, { initialState: appState }, caller)), { stdout, stderr: stdout, patchConsole: false })
    await settle()
    const visit = (node: any): any => ({ name: node.nodeName, text: node.nodeValue, style: node.style, box: node.yogaNode ? { width: node.yogaNode.getComputedWidth(), height: node.yogaNode.getComputedHeight(), left: node.yogaNode.getComputedLeft(), top: node.yogaNode.getComputedTop() } : undefined, children: node.childNodes?.map(visit) })
    layout = visit(instances.get(stdout)!.rootNode)
    app.unmount()
  }
  pills.push({ original, cells: cells(original), nativeCells: cells((row.nativeRows ?? []).join('\n')), layout })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Footer Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(input.pills ? pills : results), error => error ? reject(error) : resolve()))
process.exit(0)
