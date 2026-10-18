import fs from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original mounted REPL persistent task-list consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TASK_LIST_SOURCE !== '1') throw new Error('Pinned original task-list Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = ts.createSourceFile('REPL.tsx', fs.readFileSync(new URL('../../../src/screens/REPL.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let spinner: any, standalone: any, timingReset: any, queryReset: any, pauseEffect: any, focusedDialog: any, responseLengthConsumer: any, queryEventConsumer: any, streamingTextConsumer: any, visibleStreamingConsumer: any, showStreamingConsumer: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node) && node.name?.text === 'showSpinner') spinner = node
  if (ts.isVariableDeclaration(node) && node.name?.text === 'resetTimingRefs') timingReset = node
  if (ts.isVariableDeclaration(node) && node.name?.text === 'setResponseLength') responseLengthConsumer = node
  if (ts.isVariableDeclaration(node) && node.name?.text === 'onQueryEvent') queryEventConsumer = node
  if (ts.isVariableDeclaration(node) && node.name?.text === 'onStreamingText') streamingTextConsumer = node
  if (ts.isVariableDeclaration(node) && node.name?.text === 'visibleStreamingText') visibleStreamingConsumer = node
  if (ts.isVariableDeclaration(node) && node.name?.text === 'showStreamingText') showStreamingConsumer = node
  if (ts.isIfStatement(node) && node.expression.getText(source) === 'isQueryActive && !wasQueryActiveRef.current') queryReset = node
  if (ts.isFunctionDeclaration(node) && node.name?.text === 'getFocusedInputDialog') focusedDialog = node
  if (ts.isCallExpression(node) && node.expression.getText(source) === 'useEffect' && node.arguments[1]?.getText(source) === '[focusedInputDialog, isLoading]') pauseEffect = node
  if (ts.isJsxExpression(node) && node.expression?.getText(source).startsWith('!showSpinner && !toolJSX?.isLocalJSXCommand && showExpandedTodos')) standalone = node.expression
  ts.forEachChild(node, visit)
}
visit(source)
if (!spinner || !standalone || !timingReset || !queryReset || !pauseEffect || !focusedDialog || !responseLengthConsumer || !queryEventConsumer || !streamingTextConsumer || !visibleStreamingConsumer || !showStreamingConsumer) throw new Error('Original REPL task-list and spinner clock expressions missing')
const slashSource = ts.createSourceFile('processSlashCommand.tsx', fs.readFileSync(new URL('../../../src/utils/processUserInput/processSlashCommand.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let commandMount: any
function visitSlash(node: any) {
  if (ts.isCallExpression(node) && node.expression.getText(slashSource) === 'setToolJSX' && node.arguments.length === 1 && ts.isObjectLiteralExpression(node.arguments[0])) {
    const properties = node.arguments[0].properties
    if (properties.some((property: any) => property.name?.getText(slashSource) === 'isLocalJSXCommand' && property.initializer?.kind === ts.SyntaxKind.TrueKeyword)) commandMount = node.arguments[0]
  }
  ts.forEachChild(node, visitSlash)
}
visitSlash(slashSource)
if (!commandMount) throw new Error('Original local-JSX command mount producer missing')
const createCommandMount = new Function('command', 'jsx', `return (${commandMount.getText(slashSource)})`)
const clockSource = ts.createSourceFile('ClockContext.tsx', fs.readFileSync(new URL('../../../src/ink/components/ClockContext.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let clockProvider: any, clockFactory: any
for (const node of clockSource.statements) {
  if (ts.isFunctionDeclaration(node) && node.name?.text === 'ClockProvider') clockProvider = node
  if (ts.isFunctionDeclaration(node) && node.name?.text === '_temp') clockFactory = node
}
if (!clockProvider || !clockFactory) throw new Error('Original whole ClockProvider and factory missing')
const clockProviderCode = ts.transpileModule(clockProvider.getText(clockSource).replace(/^export /, '') + '\n' + clockFactory.getText(clockSource), { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const code = ts.transpileModule(`function Consumer() {
 const store=useAppStateStore();
 const tasksV2=useTasksV2WithCollapseEffect();
 const expandedView=useAppState(s=>s.expandedView);
 const background=useAppState(s=>s.tasks);
 const teamContext=useAppState(s=>s.teamContext);
 const [props,setProps]=React.useState(row);
 const showExpandedTodos=expandedView==='tasks';
 const toolJSX=menuCommands[props.menu]?createCommandMount(menuCommands[props.menu],null):props.localJSX?{isLocalJSXCommand:true,showSpinner:props.localSpinner}:undefined;
 const toolUseConfirmQueue=props.permission?[{}]:[],promptQueue=props.question?[{}]:[];
 const isLoading=!!props.loading,userInputOnProcessing=!!props.processing;
 const hasRunningTeammates=getAllInProcessTeammateTasks(background).some(t=>t.status==='running');
 const pendingWorkerRequest=props.workerPermission,onlySleepToolActive=props.sleepOnly;
 const seededVisibleStreamingText=props.streaming,isBriefOnly=false;
 const getCommandQueueLength=()=>props.queued??0;
 const loadingStartTimeRef=React.useRef(row.clockConsumer?0:Date.now()-(row.elapsedMs??0)),totalPausedMsRef=React.useRef(row.totalPausedMs??0),pauseStartTimeRef=React.useRef(row.pauseElapsedMs===undefined?null:loadingStartTimeRef.current+row.pauseElapsedMs),responseLengthRef=React.useRef(row.responseLength??0);
 if(!row.animationClock&&props.responseLength!==undefined)responseLengthRef.current=props.responseLength;
 const apiMetricsRef=React.useRef([]),useCallback=React.useCallback;
 const ${responseLengthConsumer.getText(source)};
 const [streamMode,setStreamMode]=React.useState(props.mode??'responding'),[streamingToolUses,setStreamingToolUses]=React.useState([]),[streamingThinking,setStreamingThinking]=React.useState(null),[streamingText,setStreamingText]=React.useState(null),[messages,setMessages]=React.useState([]),[conversationId,setConversationId]=React.useState('spinner-source-initial');
 const reducedMotion=useAppState(s=>s.settings.prefersReducedMotion)??false;
 const ${showStreamingConsumer.getText(source)};
 const ${streamingTextConsumer.getText(source)};
 const ${queryEventConsumer.getText(source)};
 const visibleStreamingText=row.streamConsumer?(${visibleStreamingConsumer.initializer.getText(source)}):seededVisibleStreamingText;
 const ${timingReset.getText(source)};
 const isQueryActive=!!props.loading,wasQueryActiveRef=React.useRef(false);
 if(row.clockConsumer){${queryReset.getText(source)}}
 wasQueryActiveRef.current=isQueryActive;
 const isExiting=!!props.exiting,exitFlow=props.exitFlow,isMessageSelectorVisible=!!props.messageSelector,isPromptInputActive=!!props.typing;
 const sandboxPermissionRequestQueue=props.sandboxPermission?[{}]:[],workerSandboxPermissions={queue:props.workerPermission?[{}]:[]},elicitation={queue:props.elicitation?[{}]:[]};
 const showingCostDialog=!!props.cost,idleReturnPending=props.idleReturn,ultraplanPendingChoice=undefined,ultraplanLaunchPending=undefined;
 const showIdeOnboarding=!!props.ideOnboarding,showModelSwitchCallout=false,showUndercoverCallout=false,showEffortCallout=!!props.effortCallout,showRemoteCallout=!!props.remoteCallout;
 const lspRecommendation=props.lspRecommendation,hintRecommendation=props.pluginHint,showDesktopUpsellStartup=!!props.desktopUpsell;
 const feature=()=>false;
 ${focusedDialog.getText(source)}
 const focusedInputDialog=getFocusedInputDialog();
 const useEffect=(callback:any,deps:any)=>React.useEffect(()=>row.clockConsumer?callback():undefined,deps);
 ${pauseEffect.getText(source)};

 const [screen,setScreen]=React.useState('prompt'),[showAllInTranscript,setShowAllInTranscript]=React.useState(false);
 const ${spinner.getText(source)};
 const parentCommits=React.useRef(0);
 control.current={store,setProps,setResponseLength,onQueryEvent,timingRefs:{loadingStartTimeRef,totalPausedMsRef,pauseStartTimeRef,responseLengthRef}};
 const reported={expandedView,tasks:tasksV2??null,showSpinner,focusedInputDialog,teamContext,background,teamsEnabled:isAgentSwarmsEnabled(),props,now:Date.now(),loadingStartTime:loadingStartTimeRef.current,totalPausedMs:totalPausedMsRef.current,pauseStartTime:pauseStartTimeRef.current,responseLength:responseLengthRef.current,streamMode,streamingToolUses,streamingText,visibleStreamingText,messages,parentCommits:parentCommits.current};
 report(reported);
 React.useLayoutEffect(()=>{if(row.streamConsumer){parentCommits.current++;report({...reported,parentCommits:parentCommits.current})}});
 const content = <KeybindingSetup>
  <GlobalKeybindingHandlers screen={screen} setScreen={setScreen} showAllInTranscript={showAllInTranscript} setShowAllInTranscript={setShowAllInTranscript} messageCount={0}/>
  <Box flexDirection="column" width="100%">
   {showSpinner&&<SpinnerWithVerb mode={row.streamConsumer?streamMode:props.mode??"responding"} loadingStartTimeRef={loadingStartTimeRef} totalPausedMsRef={totalPausedMsRef} pauseStartTimeRef={pauseStartTimeRef} responseLengthRef={responseLengthRef} overrideMessage={row.sourceMessage?props.message:props.message??"TASK_SOURCE_RUNNING"} overrideColor={props.overrideColor} overrideShimmerColor={props.overrideShimmerColor} spinnerTip={props.spinnerTip} spinnerSuffix={props.spinnerSuffix} verbose={props.verbose??false} hasActiveTools={props.hasActiveTools??false} leaderIsIdle={!isLoading&&!userInputOnProcessing}/>}
   {${standalone.getText(source)}}
  </Box>
 </KeybindingSetup>;
 return row.providerClock?React.createElement(ObservedClockProvider,null,content):row.animationClock?<ClockContext.Provider value={clock}>{content}</ClockContext.Provider>:content;
}`, { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText

process.env.ANTHROPIC_API_KEY = 'task-list-private-api-key'
process.env.USER_TYPE = 'external'
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'
process.env.CLAUDE_INTERNAL_FC_OVERRIDES = JSON.stringify({ tengu_keybinding_customization_release: true })
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify({ prefersReducedMotion: true, spinnerTipsEnabled: false, ...input.settings }))
fs.writeFileSync(join(home, '.claude', '.claude.json'), JSON.stringify({ hasCompletedOnboarding: true, customApiKeyResponses: { approved: [process.env.ANTHROPIC_API_KEY.slice(-20)], rejected: [] } }))
const absentPaths = absentManagedSettingsFixturePaths()
for (let directory = dirname(home);; directory = dirname(directory)) {
  const probe = join(directory, '.git')
  if (!fs.existsSync(probe)) absentPaths.push(probe)
  if (dirname(directory) === directory) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-task-list-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package', VERSION_CHANGELOG: '', FEEDBACK_CHANNEL: 'local' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
bootstrap.setMainLoopModelOverride('claude-sonnet-4-6')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const settings = await import('../../../src/utils/settings/settings')
const managedEnvironment = await import('../../../src/utils/managedEnv')
const React = await import('react')
const { render, Box } = await import('../../../src/ink')
const { TaskListV2 } = await import('../../../src/components/TaskListV2')
const { SpinnerWithVerb } = await import('../../../src/components/Spinner')
const { useTasksV2WithCollapseEffect } = await import('../../../src/hooks/useTasksV2')
const tasks = await import('../../../src/utils/tasks')
const { TaskCreateTool } = await import('../../../src/tools/TaskCreateTool/TaskCreateTool')
const { TaskUpdateTool } = await import('../../../src/tools/TaskUpdateTool/TaskUpdateTool')
const { AppStateProvider, getDefaultAppState, useAppState, useAppStateStore } = await import('../../../src/state/AppState')
const { onChangeAppState } = await import('../../../src/state/onChangeAppState')
const { ThemeProvider } = await import('../../../src/components/design-system/ThemeProvider')
const { KeybindingSetup } = await import('../../../src/keybindings/KeybindingProviderSetup')
const { GlobalKeybindingHandlers } = await import('../../../src/hooks/useGlobalKeybindings')
const { getAllInProcessTeammateTasks } = await import('../../../src/tasks/InProcessTeammateTask/InProcessTeammateTask')
const { isAgentSwarmsEnabled } = await import('../../../src/utils/agentSwarmsEnabled')
const { handleMessageFromStream, isCompactBoundaryMessage, getMessagesAfterCompactBoundary } = await import('../../../src/utils/messages')
const { isEphemeralToolProgress, removeTranscriptMessage } = await import('../../../src/utils/sessionStorage')
const { isFullscreenEnvEnabled } = await import('../../../src/utils/fullscreen')
const { hasCursorUpViewportYankBug } = await import('../../../src/ink/terminal')
const { randomUUID } = await import('node:crypto')
// The actual command declarations and processSlashCommand's original mount
// object decide local JSX. The sibling menu paint is outside this task-region
// oracle. PromptInput's direct model shortcut never sets this REPL toolJSX.
const menuCommands = {
  model: (await import('../../../src/commands/model/index')).default,
  doctor: (await import('../../../src/commands/doctor/index')).default,
  ide: (await import('../../../src/commands/ide/index')).default,
}
for (const command of Object.values(menuCommands)) if (command.type !== 'local-jsx') throw new Error('Original command mount kind changed')
const instances = (await import('../../../src/ink/instances')).default
const { cellAt } = await import('../../../src/ink/screen')
const { ClockContext, createClock } = await import('../../../src/ink/components/ClockContext')
const { FRAME_INTERVAL_MS } = await import('../../../src/ink/constants')
const { useTerminalFocus } = await import('../../../src/ink/hooks/use-terminal-focus')
const { c: compilerMemo } = await import('react/compiler-runtime')
const loader = await import('../../../src/keybindings/loadUserBindings')
const settle = async (ms = 120) => {
  await new Promise(resolve => setTimeout(resolve, ms))
  for (let i = 0; i < 12; i++) await new Promise(resolve => setImmediate(resolve))
}
const layout = (node: any): any => ({ name: node.nodeName, text: node.nodeValue, style: node.style, box: node.yogaNode ? { width: node.yogaNode.getComputedWidth(), height: node.yogaNode.getComputedHeight(), left: node.yogaNode.getComputedLeft(), top: node.yogaNode.getComputedTop() } : undefined, children: node.childNodes?.map(layout) })
const results = []
const actualNow = Date.now.bind(Date)
let clockOffset = 0, clockFixed: number | undefined
let teammatePhysicalRenderClock = false
const teammateDateReadStacks = new Set<string>()
Date.now = () => {
  // Fixed business time must not freeze Ink's real 16ms paint throttle.
  // Only this original lodash OS-time read remains physical in these
  // explicit interval profiles; no renderer method is called by the probe.
  if (teammatePhysicalRenderClock) {
    const stack = new Error().stack ?? ''
    if (teammateDateReadStacks.size < 30) teammateDateReadStacks.add(stack)
    // Bun inlines lodash's now() into debounce() in the stack. Both the
    // original commit call and its timerExpired callback use this primitive.
    if (stack.includes('/node_modules/lodash-es/debounce.js')) return actualNow()
  }
  return (clockFixed ?? actualNow()) + clockOffset
}
// Only the timer primitives of this explicitly owned original createClock
// are controlled. The original tick, subscriber filtering, useAnimationFrame,
// token smoothing and all Spinner children run unchanged. Ink's render timer
// keeps the physical wall clock so snapshots cannot get ahead of paint.
const nativeSetInterval = globalThis.setInterval
const nativeClearInterval = globalThis.clearInterval
let ownedTimerCapture: ((tick: () => void, handle: any, interval: number) => void) | undefined
const ownedTimerHandles = new Set<any>()
// This opt-in probe controls only the OS interval created by the original
// useElapsedTime hook. Its real subscribe/notify/getSnapshot closures and
// the keyed TeammateSpinnerLine components remain mounted unchanged.
type TeammateTimer = { id: number, tick: () => void, handle: any, milliseconds: number, notifications: number, retired: boolean, stack: string }
let teammateTimerCapture: { active: Map<any, TeammateTimer>, records: TeammateTimer[] } | undefined
const elapsedHookPath = new URL('../../../src/hooks/useElapsedTime.ts', import.meta.url).pathname
globalThis.setInterval = ((tick: () => void, milliseconds: number, ...args: any[]) => {
  if (!ownedTimerCapture) {
    const stack = teammateTimerCapture && milliseconds === 1000 ? new Error().stack ?? '' : ''
    if (!teammateTimerCapture || !stack.includes(elapsedHookPath)) return nativeSetInterval(tick, milliseconds, ...args)
    const handle = nativeSetInterval(() => {}, 1_000_000)
    const record = { id: teammateTimerCapture.records.length + 1, tick, handle, milliseconds, notifications: 0, retired: false, stack }
    teammateTimerCapture.active.set(handle, record)
    teammateTimerCapture.records.push(record)
    return handle
  }
  const handle = nativeSetInterval(() => {}, 1_000_000)
  ownedTimerHandles.add(handle)
  ownedTimerCapture(tick, handle, milliseconds)
  return handle
}) as typeof setInterval
globalThis.clearInterval = ((handle: any) => {
  ownedTimerHandles.delete(handle)
  const teammateTimer = teammateTimerCapture?.active.get(handle)
  if (teammateTimer) {
    teammateTimer.retired = true
    teammateTimerCapture!.active.delete(handle)
  }
  nativeClearInterval(handle)
}) as typeof clearInterval
for (const [index, row] of input.rows.entries()) {
  clockOffset = 0
  clockFixed = row.clockConsumer ? row.clockBase ?? 1_700_000_000_000 : undefined
  teammateTimerCapture = row.teammateClock ? { active: new Map(), records: [] } : undefined
  teammatePhysicalRenderClock = !!row.teammateClock
  teammateDateReadStacks.clear()
  if (row.global) config.saveGlobalConfig(current => ({ ...current, ...row.global }))
  const list = row.listId ?? 'source-task-list-' + bootstrap.getSessionId() + '-' + index
  process.env.CLAUDE_CODE_TASK_LIST_ID = list
  await tasks.resetTaskList(list)
  for (const task of row.tasks ?? []) {
    const { id: ignored, ...data } = task
    await tasks.createTask(list, { subject: '', description: '', status: 'pending', blocks: [], blockedBy: [], ...data })
  }
  // IDs come from the original durable writer. Arbitrary synthetic IDs are
  // tested separately by the actual component, not fed through this caller.
  const initial = { ...getDefaultAppState(), tasks: row.background ?? {}, teamContext: row.teamContext, expandedView: row.expanded ?? 'none', ...row.spinnerState }
  let current: any, output = ''
  const control: any = { current: null }
  let animationTime = 0, animationTick: (() => void) | undefined, animationHandle: any, animationInterval: number | undefined
  const intervalRequests: number[] = []
  const animationBase = actualNow()
  const originalClock = row.animationClock && !row.providerClock ? createClock(FRAME_INTERVAL_MS) : undefined
  const inClock = (callback: () => any) => {
    const previousNow = Date.now
    Date.now = () => animationBase + animationTime
    ownedTimerCapture = (tick, handle, interval) => { animationTick = tick; animationHandle = handle; animationInterval = interval }
    try { return callback() } finally { Date.now = previousNow; ownedTimerCapture = undefined }
  }
  const observeClock = (original: ReturnType<typeof createClock>) => ({
    now: () => inClock(original.now),
    subscribe: (callback: () => void, keepAlive: boolean) => inClock(() => original.subscribe(callback, keepAlive)),
    setTickInterval: (milliseconds: number) => { intervalRequests.push(milliseconds); return inClock(() => original.setTickInterval(milliseconds)) },
  })
  const clock = originalClock ? observeClock(originalClock) : undefined
  const clockDeps = { React, _c: compilerMemo, useEffect: React.useEffect, useState: React.useState, useTerminalFocus, FRAME_INTERVAL_MS, BLURRED_TICK_INTERVAL_MS: FRAME_INTERVAL_MS * 2, ClockContext, createClock: (milliseconds: number) => observeClock(createClock(milliseconds)) }
  const ObservedClockProvider = row.providerClock ? new Function(...Object.keys(clockDeps), clockProviderCode + '\nreturn ClockProvider')(...Object.values(clockDeps)) : undefined
  const deps = { React, Box, row, control, clock, ObservedClockProvider, ClockContext, handleMessageFromStream, isCompactBoundaryMessage, getMessagesAfterCompactBoundary, isEphemeralToolProgress, removeTranscriptMessage, isFullscreenEnvEnabled, hasCursorUpViewportYankBug, randomUUID, menuCommands, createCommandMount, useAppStateStore, useAppState, useTasksV2WithCollapseEffect, getAllInProcessTeammateTasks, isAgentSwarmsEnabled, KeybindingSetup, GlobalKeybindingHandlers, TaskListV2, SpinnerWithVerb, report: (value: any) => current = value }
  const Consumer = new Function(...Object.keys(deps), code + '\nreturn Consumer')(...Object.values(deps))
  const stdin = new PassThrough() as any
  stdin.isTTY = true
  stdin.setRawMode = () => {}
  stdin.ref = () => stdin
  stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.columns = row.width ?? 100
  stdout.rows = row.height ?? 36
  stdout.isTTY = false
  stdout.on('data', (chunk: any) => output += chunk.toString())
  const app = await render(React.createElement(AppStateProvider, { initialState: initial, onChangeAppState }, React.createElement(ThemeProvider, { initialState: row.theme ?? 'dark' }, React.createElement(Consumer))), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  await settle()
  const snapshot = async () => {
    const ink: any = instances.get(stdout)
    if (!ink) throw new Error('Original task-list root exited: ' + output)
    const screen = ink.frontFrame.screen
    const rows: string[] = [], paintRows: string[] = []
    for (let y = 0; y < screen.height; y++) {
      let plain = '', painted = ''
      for (let x = 0; x < screen.width; x++) {
        const cell = cellAt(screen, x, y)
        plain += cell.char
        if (cell.char) painted += ink.stylePool.get(cell.styleId).map((item: any) => item.code).join('') + cell.char + '\x1b[0m'
      }
      rows.push(plain.trimEnd())
      paintRows.push(painted)
    }
    const listId = tasks.getTaskListId()
    const refs = control.current.timingRefs
    const clock = row.clockConsumer ? { started: refs.loadingStartTimeRef.current, paused: refs.totalPausedMsRef.current, pauseStart: refs.pauseStartTimeRef.current, responseLength: refs.responseLengthRef.current } : undefined
    const teammateClock = teammateTimerCapture ? { now: Date.now(), active: [...teammateTimerCapture.active.values()].map(timer => timer.id), records: teammateTimerCapture.records.map(({ tick, handle, ...record }) => record), dateReadStacks: [...teammateDateReadStacks] } : undefined
    return { ...current, clock, teammateClock, animationTime: row.animationClock || row.providerClock ? animationTime : undefined, animationInterval, intervalRequests: [...intervalRequests], responseLength: refs.responseLengthRef.current, rows, paintRows, layout: row.captureLayout ? layout(ink.rootNode) : undefined, listId, diskTasks: await tasks.listTasks(list), currentDiskTasks: await tasks.listTasks(listId), global: config.getGlobalConfig(), output }
  }
  const frames = [await snapshot()]
  for (const step of row.steps ?? []) {
    if (typeof step === 'string') stdin.write(step)
    else if (step.wait !== undefined) await settle(step.wait)
    else if (step.props) control.current.setProps((old: any) => ({ ...old, ...step.props }))
    else if (step.background) control.current.store.setState((old: any) => ({ ...old, tasks: step.background }))
    else if (step.appState) control.current.store.setState((old: any) => {
      const next = { ...old, ...step.appState }
      for (const key of step.removeState ?? []) delete next[key]
      return next
    })
    else if (step.teammateTick !== undefined) {
      if (!teammateTimerCapture) throw new Error('Unowned teammate timer input')
      clockOffset += step.teammateTick
      for (const timer of [...teammateTimerCapture.active.values()]) {
        timer.notifications++
        timer.tick()
      }
    }
    else if (step.advanceWallClock !== undefined) {
      if (!teammateTimerCapture) throw new Error('Unowned teammate wall clock input')
      clockOffset += step.advanceWallClock
    }
    else if (step.streamLength !== undefined) control.current.setResponseLength((old: number) => old + step.streamLength)
    else if (step.streamEvent) control.current.onQueryEvent(step.streamEvent)
    else if (step.animationTick !== undefined) {
      animationTime = step.animationTick
      if (animationTick && ownedTimerHandles.has(animationHandle)) inClock(animationTick)
    }
    else if (step.settingEnv) {
      const result = settings.updateSettingsForSource('userSettings', { env: step.settingEnv })
      if (result.error) throw result.error
      managedEnvironment.applyConfigEnvironmentVariables()
    }
    else if (step.advanceClock !== undefined) {
      clockOffset += step.advanceClock
      // Clock is an OS primitive. A real AppState task snapshot wakes the
      // original component; no ordering, completion-ref or rendering logic is
      // substituted in the fixture.
      control.current.store.setState((old: any) => ({ ...old, tasks: { ...old.tasks } }))
    }
    else if (step.update) await tasks.updateTask(list, step.update.id, step.update)
    else if (step.create) await tasks.createTask(list, { subject: '', description: '', status: 'pending', blocks: [], blockedBy: [], ...step.create })
    else if (step.toolCreate || step.toolUpdate) {
      const context: any = { abortController: new AbortController(), getAppState: control.current.store.getState, setAppState: control.current.store.setState }
      await (step.toolCreate ? TaskCreateTool.call(step.toolCreate, context) : TaskUpdateTool.call(step.toolUpdate, context))
    } else throw new Error('Unproduced original task-list input')
    await settle()
    frames.push(await snapshot())
  }
  app.unmount()
  loader.disposeKeybindingWatcher()
  await settle()
  if (teammateTimerCapture) {
    if (teammateTimerCapture.active.size) throw new Error('Original teammate elapsed timers were not retired on unmount')
    ;(frames[frames.length - 1] as any).teammateUnmount = { active: [], records: teammateTimerCapture.records.map(({ tick, handle, ...record }) => record) }
  }
  results.push(frames)
}
if (ownedTimerHandles.size) throw new Error('Original owned animation timers were not retired on unmount')
globalThis.setInterval = nativeSetInterval
globalThis.clearInterval = nativeClearInterval
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original task-list Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
