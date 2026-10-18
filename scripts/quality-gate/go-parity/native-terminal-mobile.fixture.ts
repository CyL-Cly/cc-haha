import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual mobile command and QR/tab consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MOBILE_SOURCE !== '1') throw new Error('Pinned mobile Source required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-mobile-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const { AppStateProvider, getDefaultAppState } = await import('../../../src/state/AppState')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const { call } = await import('../../../src/commands/mobile/mobile')
const declaration = (await import('../../../src/commands/mobile/index')).default
const rows = []
for (const keys of input.rows) {
  const events: any[] = []
  const active = new Set(['Confirmation'])
  const provider = { bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: { current: new Map() }, pendingChordRef: { current: null }, pendingChord: null, setPendingChord: () => {}, activeContexts: active, registerActiveContext: (value: string) => active.add(value), unregisterActiveContext: (value: string) => active.delete(value) }
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = 100
  let output = ''
  stdout.on('data', chunk => output += chunk.toString())
  const node = React.createElement(AppStateProvider, { initialState: getDefaultAppState() }, React.createElement(KeybindingProvider, provider as any, await call((...values: any[]) => events.push(values))))
  const app = await render(node, { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  const settle = async () => { for (let index = 0; index < 10; index++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 25)) }
  await settle()
  const frames = [{ key: 'mount', output, events: structuredClone(events) }]
  for (const key of keys) { output = ''; stdin.write(key); if (key === '\x1b') await new Promise(resolve => setTimeout(resolve, 120)); await settle(); frames.push({ key, output, events: structuredClone(events) }) }
  app.unmount(); await settle(); rows.push({ frames, events })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Mobile Source escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declaration: { name: declaration.name, aliases: declaration.aliases, type: declaration.type }, rows }), error => error ? reject(error) : resolve()))
process.exit(0)
