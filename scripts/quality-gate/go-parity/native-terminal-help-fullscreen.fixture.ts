import fs from 'node:fs'
import {join} from 'node:path'
import {PassThrough} from 'node:stream'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths, installClientFixtureCapabilities} from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original fullscreen Help caller')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_FULLSCREEN_HELP_SOURCE !== '1') {
  throw new Error('Missing pinned original fullscreen Help guard')
}
process.env.ANTHROPIC_API_KEY = 'owned-help-source-api-key'
process.env.CLAUDE_CODE_NO_FLICKER = '1'
fs.mkdirSync(join(home, '.claude'), {recursive: true})
fs.writeFileSync(join(home, '.claude', 'settings.json'), '{}')
fs.writeFileSync(join(home, '.claude', '.claude.json'), JSON.stringify({
  hasCompletedOnboarding: true,
  customApiKeyResponses: {approved: ['-help-source-api-key'], rejected: []}
}))
const caps = await installClientFixtureCapabilities(home, {absentPaths: absentManagedSettingsFixturePaths()})
;(globalThis as any).MACRO = {
  VERSION: '999.0.0-local', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package',
  NATIVE_PACKAGE_URL: 'https://fixture.invalid/package', FEEDBACK_CHANNEL: 'local',
  VERSION_CHANGELOG: '', ISSUES_EXPLAINER: ''
}
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
const {render, Box, Text} = await import('../../../src/ink')
const {AlternateScreen} = await import('../../../src/ink/components/AlternateScreen')
const {FullscreenLayout} = await import('../../../src/components/FullscreenLayout')
const {ScrollKeybindingHandler} = await import('../../../src/components/ScrollKeybindingHandler')
const {ThemeProvider} = await import('../../../src/components/design-system/ThemeProvider')
const {KeybindingProvider} = await import('../../../src/keybindings/KeybindingContext')
const {parseBindings} = await import('../../../src/keybindings/parser')
const {DEFAULT_BINDINGS} = await import('../../../src/keybindings/defaultBindings')
const {AppStoreContext, getDefaultAppState} = await import('../../../src/state/AppState')
const {createStore} = await import('../../../src/state/store')
const {processSlashCommand} = await import('../../../src/utils/processUserInput/processSlashCommand')
const help = (await import('../../../src/commands/help/index')).default
const instances = (await import('../../../src/ink/instances')).default
const {cellAt} = await import('../../../src/ink/screen')
const {Parser} = await import('../../../src/ink/termio')
const {isFullscreenEnvEnabled} = await import('../../../src/utils/fullscreen')

// Only the output primitive is captured. The original AlternateScreen must
// find the same real Ink instance via instances.get(process.stdout).
const stdout = process.stdout
const originalWrite = stdout.write.bind(stdout)
let output = ''
stdout.write = ((chunk: any, encoding: any, callback: any) => {
  output += chunk.toString()
  if (typeof encoding === 'function') encoding()
  else callback?.()
  return true
}) as any
const results = []
const flush = async () => {
  for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve))
}
const visit = (node: any): any => ({
  name: node.nodeName, text: node.nodeValue, style: node.style,
  box: node.yogaNode ? {
    width: node.yogaNode.getComputedWidth(), height: node.yogaNode.getComputedHeight(),
    left: node.yogaNode.getComputedLeft(), top: node.yogaNode.getComputedTop()
  } : undefined,
  children: node.childNodes?.map(visit)
})
try {
  for (const row of input.rows) {
    Object.defineProperty(stdout, 'columns', {configurable: true, value: row.width})
    Object.defineProperty(stdout, 'rows', {configurable: true, value: row.height})
    Object.defineProperty(stdout, 'isTTY', {configurable: true, value: false})
    const stdin = new PassThrough() as any
    stdin.isTTY = true
    stdin.setRawMode = () => {}
    stdin.ref = () => stdin
    stdin.unref = () => stdin
    const active = new Set(['Global', 'Help'])
    const provider = {
      bindings: parseBindings([...DEFAULT_BINDINGS, ...(row.bindings ?? [])]),
      handlerRegistryRef: {current: new Map()}, pendingChordRef: {current: null},
      pendingChord: null, setPendingChord: () => {}, activeContexts: active,
      registerActiveContext: (name: string) => active.add(name),
      unregisterActiveContext: (name: string) => active.delete(name)
    }
    const store = createStore(getDefaultAppState())
    let jsx: any, commandUI: any, settled = false, completion: any
    const pending = processSlashCommand('/help ' + (row.args ?? ''), [], [], [], {
      getAppState: store.getState, setAppState: store.setState,
      abortController: new AbortController(), messages: [],
      options: {commands: [help, ...row.commands], tools: [], isNonInteractiveSession: false, agentDefinitions: {activeAgents: []}}
    } as any, (value: any) => {if (value?.jsx) {jsx = value.jsx; commandUI = value}}).then(value => {
      settled = true
      completion = value
      return value
    })
    const deadline = Date.now() + 5000
    const until = async (predicate: () => boolean, label: string) => {
      while (!predicate()) {
        if (Date.now() > deadline) throw new Error(label + ': ' + JSON.stringify({output, blocked: caps.blocked}))
        await new Promise(resolve => setTimeout(resolve, 5))
      }
      await flush()
    }
    await until(() => jsx != null, 'Original Help JSX did not mount')
    if (!commandUI.isLocalJSXCommand || !commandUI.shouldHidePromptInput) throw new Error('Original fullscreen modal caller flags changed')
    const transcriptRef = React.createRef<any>()
    const modalRef = React.createRef<any>()
    const content = React.createElement(React.Fragment, null,
      React.createElement(ScrollKeybindingHandler, {scrollRef: transcriptRef, isActive: true}),
      React.createElement(FullscreenLayout, {
        scrollRef: transcriptRef, modalScrollRef: modalRef, modal: jsx,
        hidePill: true, hideSticky: true,
        scrollable: React.createElement(Box, {flexDirection: 'column'},
          ...Array.from({length: 70}, (_, i) => React.createElement(Text, {key: i}, 'SOURCE_TRANSCRIPT_' + String(i).padStart(3, '0')))),
        bottom: null
      }))
    const view = React.createElement(AppStoreContext.Provider, {value: store},
      React.createElement(ThemeProvider, {initialState: row.theme ?? 'dark'},
        React.createElement(KeybindingProvider, provider as any,
          React.createElement(AlternateScreen, {mouseTracking: false}, content))))
    output = ''
    const app = await render(view, {stdin, stdout, stderr: stdout, exitOnCtrlC: false, patchConsole: false})
    await until(() => {
      const ink: any = instances.get(stdout)
      return ink?.isAltScreenActive && ink.frontFrame.screen.height === row.height && ink.frontFrame.screen.cells.some((value: number, i: number) => i % 2 === 0 && value > 1)
    }, 'Original fullscreen frame did not commit')
    const snapshot = () => {
      const ink: any = instances.get(stdout)
      if (!ink) throw new Error('Original mounted fullscreen root exited: ' + output)
      const screen = ink.frontFrame.screen
      const rows: string[] = []
      for (let y = 0; y < screen.height; y++) {
        let line = ''
        for (let x = 0; x < screen.width; x++) {
          const cell = cellAt(screen, x, y)!
          if (cell.char === '') continue
          line += ink.stylePool.get(cell.styleId).map((code: any) => code.code).join('') + cell.char + '\x1b[0m'
        }
        rows.push(line)
      }
      const cells = (value: string) => new Parser().feed(value).flatMap(action =>
        action.type === 'text' ? action.graphemes.map(grapheme => ({text: grapheme.value, style: action.style})) : [])
      return {
        rows, cells: row.captureCells ? cells(rows.join('\n')) : undefined, settled, altScreen: ink.isAltScreenActive,
        transcriptTop: transcriptRef.current?.getScrollTop(), transcriptHeight: transcriptRef.current?.getViewportHeight(),
        modalTop: modalRef.current?.getScrollTop(), layout: row.captureLayout ? visit(ink.rootNode) : undefined,
        output: row.captureOutput ? output : undefined
      }
    }
    const snapshots = [snapshot()]
    for (const step of row.keys ?? []) {
      output = ''
      const previous = (instances.get(stdout) as any).frontFrame
      stdin.write(typeof step === 'string' ? step : step.input)
      if (typeof step !== 'string' && step.noRender) await flush()
      else await until(() => (instances.get(stdout) as any)?.frontFrame !== previous || settled, 'Original fullscreen key did not commit')
      snapshots.push(snapshot())
    }
    if (settled) await pending
    results.push({snapshots, completion, fullscreenEnabled: isFullscreenEnvEnabled()})
    app.unmount()
    await flush()
  }
} finally {
  stdout.write = originalWrite as any
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Original fullscreen Help escaped disposable capabilities ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => originalWrite(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
