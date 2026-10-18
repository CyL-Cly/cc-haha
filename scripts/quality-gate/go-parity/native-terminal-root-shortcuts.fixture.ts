import fs from 'node:fs'
import {createRequire} from 'node:module'
import {join} from 'node:path'
import {PassThrough} from 'node:stream'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths, installClientFixtureCapabilities} from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original mounted REPL keyboard consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_ROOT_SHORTCUTS_SOURCE !== '1') throw new Error('Pinned original root keyboard guard missing')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const repl = ts.createSourceFile('REPL.tsx', fs.readFileSync(new URL('../../../src/screens/REPL.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const declarations = new Map<string, any>()
function visit(node: any) {
  if (ts.isVariableDeclaration(node) && ['globalKeybindingProps', 'cancelRequestProps', 'toolJsxCentered', 'centeredModal', 'mainReturn', 'transcriptReturn'].includes(node.name?.text)) declarations.set(node.name.text, node)
  ts.forEachChild(node, visit)
}
visit(repl)
if (declarations.size !== 6) throw new Error('Original REPL root consumer identity changed')
const rootHandlers = (name: string) => {
  const children = declarations.get(name).initializer.children
  const keep = ['GlobalKeybindingHandlers', 'CommandKeybindingHandlers', 'ScrollKeybindingHandler', 'CancelRequestHandler']
  const result: string[] = []
  for (const node of children) {
    const text = node.getText(repl)
    if (keep.some(name => text.startsWith('<' + name) || text.startsWith('{transcriptScrollRef ?') && text.includes('<' + name))) result.push(text)
  }
  if (result.length !== 4) throw new Error('Original root handler mount order changed: ' + name)
  return result.join('\n')
}
const footer = repl.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'TranscriptModeFooter')
if (!footer) throw new Error('Original transcript footer missing')
const code = ts.transpileModule(`
${footer.getText(repl)}
function RootConsumer() {
 const [screen,setScreen]=React.useState('prompt');
 const [showAllInTranscript,setShowAllInTranscript]=React.useState(false);
 const [toolJSX,setToolJSX]=React.useState(initialUI);
 const [showBashesDialog,setShowBashesDialog]=React.useState(false);
 const store=useAppStateStore();
 const messages=history;
 const permission=useAppState(s=>s.toolPermissionContext);
 const expandedView=useAppState(s=>s.expandedView);
 const preview=useAppState(s=>s.showTeammateMessagePreview);
 const notifications=useAppState(s=>s.notifications);
 const [searchOpen]=React.useState(false);
 const scrollRef=React.useRef(null),modalScrollRef=React.useRef(null),jumpRef=React.useRef(null);
 const disableVirtualScroll=false,dumpMode=false,focusedInputDialog=undefined,toolPermissionOverlay=null,viewedAgentTask=null;
 const virtualScrollActive=isFullscreenEnvEnabled()&&!disableVirtualScroll;
 const handleEnterTranscript=()=>trace.push('enter-transcript');
 const handleExitTranscript=()=>trace.push('exit-transcript');
 const onSubmit=(...args)=>commands.push(args);
 const setToolUseConfirmQueue=()=>trace.push('clear-tool-permissions');
 const onCancel=()=>{trace.push('cancel');controller.abort()};
 const setMessages=()=>{throw new Error('Unproduced Agent-kill branch')};
 const createAgentsKilledMessage=()=>{throw new Error('Unproduced Agent-kill record')};
 const isMessageSelectorVisible=false;
 const abortController=row.busy?controller:null;
 const handleQueuedCommandOnCancel=()=>{throw new Error('Unproduced queued-command branch')};
 const vimMode='INSERT',isSearchingHistory=false,isHelpOpen=false,inputMode='prompt',inputValue='',streamMode='responding';
 ${['globalKeybindingProps','cancelRequestProps','toolJsxCentered','centeredModal'].map(name=>'const '+declarations.get(name).getText(repl)+';').join('\n')}
 control.current={setToolJSX,store};
 useBackgroundTaskNavigation({onOpenBackgroundTasks:toolJSX?.isLocalJSXCommand?undefined:()=>setShowBashesDialog(true)});
 useTeammateViewAutoExit();
 report({screen,showAllInTranscript,expandedView,preview,showBashesDialog,settled:completionState.settled,notifications:notifications.current?.text??null,commands:[...commands],trace:[...trace],fullscreen:isFullscreenEnvEnabled()});
 const transcriptMessagesElement=<Box flexDirection="column">{history.map((text,i)=><Text key={i}>{text}</Text>)}</Box>;
 const transcriptToolJSX=toolJSX&&<Box flexDirection="column" width="100%">{toolJSX.jsx}</Box>;
 if(screen==='transcript') {
  const transcriptScrollRef=isFullscreenEnvEnabled()&&!disableVirtualScroll&&!dumpMode?scrollRef:undefined;
  const transcriptReturn=<KeybindingSetup>
   ${rootHandlers('transcriptReturn')}
   {transcriptScrollRef?<FullscreenLayout scrollRef={scrollRef} scrollable={<>{transcriptMessagesElement}{transcriptToolJSX}</>} bottom={<TranscriptModeFooter showAllInTranscript={showAllInTranscript} virtualScroll={true}/>}/>:<>{transcriptMessagesElement}{transcriptToolJSX}<TranscriptModeFooter showAllInTranscript={showAllInTranscript} virtualScroll={false}/></>}
  </KeybindingSetup>;
  return transcriptScrollRef?<AlternateScreen mouseTracking={false}>{transcriptReturn}</AlternateScreen>:transcriptReturn;
 }
 const composedOnScroll=()=>trace.push('scroll-chrome');
 const mainReturn=<KeybindingSetup>
  ${rootHandlers('mainReturn')}
  <FullscreenLayout scrollRef={scrollRef} modalScrollRef={modalScrollRef} modal={centeredModal} hidePill={true} hideSticky={true} scrollable={<>{transcriptMessagesElement}{!centeredModal&&toolJSX?.jsx}</>} bottom={toolJSX?.shouldHidePromptInput?null:<Text>ROOT_PRIVATE_PROMPT</Text>}/>
 </KeybindingSetup>;
 return isFullscreenEnvEnabled()?<AlternateScreen mouseTracking={false}>{mainReturn}</AlternateScreen>:mainReturn;
}`, {compilerOptions: {jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext}}).outputText

process.env.ANTHROPIC_API_KEY = 'root-shortcut-private-api-key'
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_INTERNAL_FC_OVERRIDES = JSON.stringify({tengu_keybinding_customization_release: true})
fs.mkdirSync(join(home, '.claude'), {recursive: true})
fs.writeFileSync(join(home, '.claude', 'settings.json'), '{}')
fs.writeFileSync(join(home, '.claude', '.claude.json'), JSON.stringify({hasCompletedOnboarding: true, customApiKeyResponses: {approved: [process.env.ANTHROPIC_API_KEY.slice(-20)], rejected: []}}))
const caps = await installClientFixtureCapabilities(home, {absentPaths: absentManagedSettingsFixturePaths()})
;(globalThis as any).MACRO = {VERSION: '999.0.0-root-shortcut-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package', NATIVE_PACKAGE_URL: 'https://fixture.invalid/package', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: ''}
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const {c: _c} = await import('react/compiler-runtime')
const {render, Box, Text} = await import('../../../src/ink')
const {AlternateScreen} = await import('../../../src/ink/components/AlternateScreen')
const {FullscreenLayout} = await import('../../../src/components/FullscreenLayout')
const {ScrollKeybindingHandler} = await import('../../../src/components/ScrollKeybindingHandler')
const {GlobalKeybindingHandlers} = await import('../../../src/hooks/useGlobalKeybindings')
const {CommandKeybindingHandlers} = await import('../../../src/hooks/useCommandKeybindings')
const {CancelRequestHandler} = await import('../../../src/hooks/useCancelRequest')
const {useBackgroundTaskNavigation} = await import('../../../src/hooks/useBackgroundTaskNavigation')
const {useTeammateViewAutoExit} = await import('../../../src/hooks/useTeammateViewAutoExit')
const {KeybindingSetup} = await import('../../../src/keybindings/KeybindingProviderSetup')
const loader = await import('../../../src/keybindings/loadUserBindings')
const {ThemeProvider} = await import('../../../src/components/design-system/ThemeProvider')
const {AppStateProvider, getDefaultAppState, useAppState, useAppStateStore} = await import('../../../src/state/AppState')
const {onChangeAppState} = await import('../../../src/state/onChangeAppState')
const {notifyChange} = await import('../../../src/utils/settings/changeDetector')
const {processSlashCommand} = await import('../../../src/utils/processUserInput/processSlashCommand')
const help = (await import('../../../src/commands/help/index')).default
const {isFullscreenEnvEnabled} = await import('../../../src/utils/fullscreen')
const {useShortcutDisplay} = await import('../../../src/keybindings/useShortcutDisplay')
const figures = (await import('figures')).default
const instances = (await import('../../../src/ink/instances')).default
const {cellAt} = await import('../../../src/ink/screen')
const stdout = process.stdout
const write = stdout.write.bind(stdout)
let output = ''
stdout.write = ((chunk: any, encoding: any, callback: any) => {
  output += chunk.toString()
  if (typeof encoding === 'function') encoding()
  if (typeof callback === 'function') callback()
  return true
}) as any
const settle = async () => {
 // Drive the original terminal parser's bare-Escape framing and Ink's real
 // frame timer before sampling its committed frame; never force a render.
 await new Promise(resolve => setTimeout(resolve, 80))
 for (let i = 0; i < 12; i++) await new Promise(resolve => setImmediate(resolve))
}
const results = []
const bindings = []
const visitLayout = (node: any): any => ({name: node.nodeName, text: node.nodeValue, style: node.style, box: node.yogaNode ? {width: node.yogaNode.getComputedWidth(), height: node.yogaNode.getComputedHeight(), left: node.yogaNode.getComputedLeft(), top: node.yogaNode.getComputedTop()} : undefined, children: node.childNodes?.map(visitLayout)})
try {
 for (const row of input.rows) {
  if (row.global) config.saveGlobalConfig(current => ({...current, ...row.global}))
  process.env.CLAUDE_CODE_NO_FLICKER = row.fullscreen ? '1' : '0'
  fs.writeFileSync(join(home, '.claude', 'keybindings.json'), JSON.stringify({bindings: row.bindings ?? []}))
  loader.resetKeybindingLoaderForTesting()
  Object.defineProperty(stdout, 'columns', {configurable: true, value: row.width ?? 100})
  Object.defineProperty(stdout, 'rows', {configurable: true, value: row.height ?? 36})
  Object.defineProperty(stdout, 'isTTY', {configurable: true, value: false})
  const trace: string[] = [], commands: any[] = [], controller = new AbortController()
  const completionState: any = {settled: false, result: null}
  const control: any = {current: null}
  let initialUI: any = null
  let current: any
  const history = row.history ?? Array.from({length: 70}, (_, i) => 'ROOT_TRANSCRIPT_' + String(i).padStart(3, '0'))
  if (row.help) {
   const pending = processSlashCommand('/help', [], [], [], {getAppState: () => control.current?.store.getState() ?? getDefaultAppState(), setAppState: (f: any) => control.current?.store.setState(f), abortController: controller, messages: [], options: {commands: [help], tools: [], isNonInteractiveSession: false, agentDefinitions: {activeAgents: []}}} as any, (value: any) => {initialUI = value})
   await new Promise<void>((resolve, reject) => {const start = Date.now();const poll = () => initialUI?.jsx ? resolve() : Date.now() - start > 5000 ? reject(new Error('Original Help JSX absent')) : setTimeout(poll, 5);poll()})
   void pending.then(result => {completionState.settled = true;completionState.result = result;control.current?.setToolJSX(null)})
  }
  const deps = {React, _c, Box, Text, KeybindingSetup, GlobalKeybindingHandlers, CommandKeybindingHandlers, ScrollKeybindingHandler, CancelRequestHandler, FullscreenLayout, AlternateScreen, useAppState, useAppStateStore, useBackgroundTaskNavigation, useTeammateViewAutoExit, isFullscreenEnvEnabled, useShortcutDisplay, figures, row, initialUI, history, controller, control, trace, commands, completionState, report: (state: any) => current = state}
  const RootConsumer = new Function(...Object.keys(deps), code + '\nreturn RootConsumer')(...Object.values(deps))
  const stdin = new PassThrough() as any
  stdin.isTTY = true
  stdin.setRawMode = () => {}
  stdin.ref = () => stdin
  stdin.unref = () => stdin
  const initialState = getDefaultAppState()
  if (row.restoreExpanded) initialState.expandedView = config.getGlobalConfig().showSpinnerTree ? 'teammates' : config.getGlobalConfig().showExpandedTodos ? 'tasks' : 'none'
  const app = await render(React.createElement(AppStateProvider, {initialState, onChangeAppState}, React.createElement(ThemeProvider, {initialState: 'dark'}, React.createElement(RootConsumer))), {stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false})
  await settle()
  bindings.push(loader.loadKeybindingsSyncWithWarnings().bindings)
  const snapshot = () => {
   const ink: any = instances.get(stdout)
   if (!ink) throw new Error('Original mounted root exited: ' + output)
   const frame = ink.frontFrame
   const rows: string[] = []
   const paintRows: string[] = []
   for (let y = 0; y < frame.screen.height; y++) {let line = '';for (let x = 0; x < frame.screen.width; x++) line += cellAt(frame.screen, x, y).char;rows.push(line.trimEnd())}
   if (row.capturePaint) for (let y = 0; y < frame.screen.height; y++) {
    let line = ''
    for (let x = 0; x < frame.screen.width; x++) {
     const cell = cellAt(frame.screen, x, y)!
     if (cell.char) line += ink.stylePool.get(cell.styleId).map((code: any) => code.code).join('') + cell.char + '\x1b[0m'
    }
    paintRows.push(line)
   }
   const global = config.getGlobalConfig()
   return {...current, globalExpanded: {showExpandedTodos: global.showExpandedTodos, showSpinnerTree: global.showSpinnerTree, futureRootView: (global as any).futureRootView}, globalFile: JSON.parse(fs.readFileSync(join(home, '.claude', '.claude.json'), 'utf8')), rows, paintRows, layout: row.captureLayout ? visitLayout(ink.rootNode) : undefined, altScreen: ink.isAltScreenActive, output, completion: completionState.result}
  }
  const frames = [snapshot()]
  for (const step of row.steps) {
   if (typeof step === 'string') stdin.write(step)
   else if (step.settings) {fs.writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(step.settings));notifyChange('userSettings')}
   else throw new Error('Unproduced original root input')
   await settle()
   frames.push(snapshot())
  }
  app.unmount()
  loader.disposeKeybindingWatcher()
  await settle()
  results.push(frames)
 }
 if (caps.blocked.length || caps.secureReads.length) throw new Error('Original root consumers escaped private boundary ' + JSON.stringify(caps))
} finally {stdout.write = write}
await new Promise<void>((resolve, reject) => write(JSON.stringify({results, bindings, blocked: caps.blocked}), error => error ? reject(error) : resolve()))
process.exit(0)
