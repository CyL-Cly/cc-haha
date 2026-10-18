import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual pending MCP publication and mounted settings consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_PENDING_SOURCE !== '1') throw new Error('Pinned pending MCP Source guard required')
const absentPaths = absentManagedSettingsFixturePaths()
const managedMCP = join(dirname(absentPaths[0]!), 'managed-mcp.json')
if (!fs.existsSync(managedMCP)) absentPaths.push(managedMCP)
for (const path of ['/home/claude/.claude/remote/.session_ingress_token', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = home; ; parent = dirname(parent)) {
  const candidate = join(parent, '.git')
  if (!fs.existsSync(candidate)) absentPaths.push(candidate)
  if (dirname(parent) === parent) break
}
await import('yoga-layout')
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: new URL(input.url).origin })
;(globalThis as any).MACRO = { VERSION: '999.0.0-mcp-pending-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const { AppStoreContext, getDefaultAppState } = await import('../../../src/state/AppState')
const { createStore } = await import('../../../src/state/store')
const { MCPConnectionManager } = await import('../../../src/services/mcp/MCPConnectionManager')
const { MCPSettings } = await import('../../../src/components/mcp/MCPSettings')
const { clearServerCache } = await import('../../../src/services/mcp/client')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const store = createStore(getDefaultAppState())
const transitions: string[][] = []
const snapshot = () => store.getState().mcp.clients.map(client => client.type)
const remove = store.subscribe(() => transitions.push(snapshot()))
const actualFetch = globalThis.fetch
let pendingAtInitialize = false
let initialized = false
globalThis.fetch = (async (...args: Parameters<typeof fetch>) => {
  const body = args[1]?.body
  if (typeof body === 'string' && JSON.parse(body).method === 'initialize') {
    pendingAtInitialize = snapshot().includes('pending')
    initialized = true
  }
  return actualFetch(...args)
}) as typeof fetch
const active = new Set(['Confirmation'])
const provider = { bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: { current: new Map() }, pendingChordRef: { current: null }, pendingChord: null, setPendingChord: () => {}, activeContexts: active, registerActiveContext: (value: string) => active.add(value), unregisterActiveContext: (value: string) => active.delete(value) }
const mcpConfig = { panel: { type: 'http' as const, url: input.url, scope: 'user' as const } }
const completions: any[] = []
let menuOpen = false
function view() {
  return React.createElement(AppStoreContext.Provider, { value: store }, React.createElement(KeybindingProvider, provider as any,
    React.createElement(MCPConnectionManager, { dynamicMcpConfig: mcpConfig, isStrictMcpConfig: true }, menuOpen ? React.createElement(MCPSettings, { onComplete: (message: any) => completions.push(message) }) : null)))
}
const stdin = new PassThrough() as any
stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
const stdout = new PassThrough() as any
stdout.isTTY = false; stdout.columns = 100
let output = ''; stdout.on('data', chunk => { output += chunk.toString() })
const app = await render(view(), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
const deadline = Date.now() + 8000
async function until(predicate: () => boolean) {
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error('Actual pending MCP boundary ' + JSON.stringify({ transitions, pendingAtInitialize, initialized, blocked: caps.blocked }) + output.slice(-5000))
    await new Promise(resolve => setTimeout(resolve, 10))
  }
}
await until(() => initialized)
menuOpen = true; app.rerender(view())
await until(() => output.includes('panel') && output.includes('connecting'))
const pendingOutput = output
fs.writeFileSync(join(home, 'pending-menu-mounted'), 'actual pending menu before initialize response')
await until(() => snapshot().includes('connected') && output.includes('connected'))
const connectedOutput = output
app.unmount(); remove(); await clearServerCache('panel', mcpConfig.panel)
if (caps.blocked.length) throw new Error('Pending source escaped owned fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ pendingAtInitialize, transitions, pendingOutput, connectedOutput, completions, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), error => error ? reject(error) : resolve()))
process.exit(0)
