import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'Actual PluginOptionsDialog key/input consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_OPTIONS_INPUT_SOURCE !== '1') throw new Error('Unowned plugin options input Source')
const absentPaths = absentManagedSettingsFixturePaths()
const packagedRg = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(packagedRg)) absentPaths.push(packagedRg)
fs.mkdirSync(join(home, 'bin'), { recursive: true })
process.env.PATH = join(home, 'bin')
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) {
  if (!fs.existsSync(path)) absentPaths.push(path)
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-plugin-options-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
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
const { PluginOptionsDialog } = await import('../../../src/commands/plugin/PluginOptionsDialog')
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
  const handlers = { current: new Map() }
  const pending = { current: null }
  const active = new Set(['Confirmation'])
  const provider = {
    bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: handlers, pendingChordRef: pending, pendingChord: null,
    setPendingChord: (value: any) => pending.current = value, activeContexts: active,
    registerActiveContext: (value: any) => active.add(value), unregisterActiveContext: (value: any) => active.delete(value)
  }
  const node = React.createElement(AppStateProvider, { initialState: getDefaultAppState() }, React.createElement(KeybindingProvider, provider as any,
    React.createElement(PluginOptionsDialog, { title: 'Configure original options', subtitle: 'Plugin options', configSchema: row.schema, initialValues: row.initial, onSave: (values: any) => events.push({ op: 'save', values }), onCancel: () => events.push({ op: 'cancel' }) })))
  const instance = await render(node, { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  const settle = async () => {
    for (let index = 0; index < 10; index++) await new Promise(resolve => setImmediate(resolve))
    await new Promise(resolve => setTimeout(resolve, 20))
  }
  const frames: any[] = []
  await settle()
  frames.push({ key: 'mount', output, events: structuredClone(events) })
  for (const key of row.keys) {
    output = ''
    stdin.write(key)
    await settle()
    frames.push({ key, output, events: structuredClone(events) })
  }
  instance.unmount()
  await settle()
  results.push({ frames, events })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Plugin options escaped private fixture: ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
process.exit(0)
