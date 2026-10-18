import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Actual shell permission key consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PERMISSION_DEBUG_SOURCE !== '1') throw new Error('Unowned permission key Source')
const absentPaths = absentManagedSettingsFixturePaths()
const packagedRg = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(packagedRg)) absentPaths.push(packagedRg)
fs.mkdirSync(join(home, 'bin'), { recursive: true })
process.env.PATH = join(home, 'bin')
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) {
  if (!fs.existsSync(path)) absentPaths.push(path)
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-permission-debug-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const { AppStateProvider, getDefaultAppState } = await import('../../../src/state/AppState')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const { BashPermissionRequest } = await import('../../../src/components/permissions/BashPermissionRequest/BashPermissionRequest')
const { PowerShellPermissionRequest } = await import('../../../src/components/permissions/PowerShellPermissionRequest/PowerShellPermissionRequest')
const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
const { PowerShellTool } = await import('../../../src/tools/PowerShellTool/PowerShellTool')
const results = []
for (const row of input.rows) {
  const events: any[] = []
  const stdin = new PassThrough() as any
  stdin.isTTY = true
  stdin.setRawMode = (value: boolean) => events.push({ raw: value })
  stdin.ref = () => stdin
  stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.columns = 100
  stdout.isTTY = false
  let output = ''
  stdout.on('data', (chunk: any) => output += chunk.toString())
  const tool = row.tool === 'Bash' ? BashTool : PowerShellTool
  const toolUseContext: any = { options: { debug: row.debug, mcpClients: [] }, messages: [], abortController: new AbortController() }
  const confirm: any = {
    assistantMessage: { type: 'assistant', uuid: 'owned-debug-assistant', timestamp: new Date().toISOString(), message: { id: 'owned-debug-message', content: [] } },
    tool, input: { command: "echo 'Owned permission input'" }, description: 'Owned request description',
    toolUseID: 'owned-debug-tool', permissionResult: { behavior: 'ask', message: 'Owned decision', decisionReason: { type: 'other', reason: 'Owned reason' } },
    permissionPromptStartTimeMs: Date.now(), toolUseContext,
    onUserInteraction: () => events.push({ op: 'interaction' }), onAbort: () => events.push({ op: 'abort' }),
    onAllow: (...args: any[]) => events.push({ op: 'allow', args }), onReject: (...args: any[]) => events.push({ op: 'reject', args }),
    recheckPermission: async () => events.push({ op: 'recheck' })
  }
  const handlers = { current: new Map() }
  const pending = { current: null }
  const active = new Set(['Confirmation'])
  const provider = {
    bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: handlers, pendingChordRef: pending, pendingChord: null,
    setPendingChord: (value: any) => pending.current = value, activeContexts: active,
    registerActiveContext: (value: any) => active.add(value), unregisterActiveContext: (value: any) => active.delete(value)
  }
  const appState = getDefaultAppState()
  const component = row.tool === 'Bash' ? BashPermissionRequest : PowerShellPermissionRequest
  const node = React.createElement(AppStateProvider, { initialState: appState }, React.createElement(KeybindingProvider, provider as any,
    React.createElement(component, { toolUseConfirm: confirm, toolUseContext, onDone: () => events.push({ op: 'done' }), onReject: () => events.push({ op: 'outer-reject' }), verbose: false, workerBadge: undefined })))
  const instance = await render(node, { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  const frames: any[] = []
  const settle = async () => {
    for (let index = 0; index < 10; index++) await new Promise(resolve => setImmediate(resolve))
    await new Promise(resolve => setTimeout(resolve, 20))
  }
  await settle()
  frames.push({ key: 'mount', output, events: structuredClone(events), handlers: [...handlers.current.keys()] })
  for (const key of ['\x04', '\r', 'y', 'n', '\x1b', '\x1b[A', '\x1b[B', '\t', '\x04', '\r']) {
    output = ''
    stdin.write(key)
    await settle()
    frames.push({ key, output, events: structuredClone(events), handlers: [...handlers.current.keys()] })
  }
  instance.unmount()
  await settle()
  results.push({ tool: row.tool, debug: row.debug, frames, events })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Permission keys escaped private fixture: ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
