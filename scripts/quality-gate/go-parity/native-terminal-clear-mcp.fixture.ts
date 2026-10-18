import fs from 'node:fs'
import * as cp from 'node:child_process'
import { spyOn } from 'bun:test'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual clear MCP React effect')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CLEAR_MCP_FIXTURE !== '1') throw new Error('Pinned owned clear MCP fixture required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.session_ingress_token', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absentPaths.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let parent = spelling; ; parent = dirname(parent)) {
    const candidate = join(parent, '.git')
    if (!fs.existsSync(candidate)) absentPaths.push(candidate)
    if (dirname(parent) === parent) break
  }
}
// Yoga loads its bundled base64 WASM through a data: URL. Finish this local
// dependency before the client network guard; actual hook I/O remains guarded.
await import('yoga-layout')
const caps = await installClientFixtureCapabilities(home, { absentPaths, privateExecutableFiles: [join(home, 'bin', 'bash')], loopbackFetchOrigin: new URL(input.url).origin })
const externalProbes: any[] = []
const guardedExec = cp.execFileSync
spyOn(cp, 'execFileSync').mockImplementation(((...args: any[]) => { externalProbes.push({ args: args.slice(0, 2), stack: new Error().stack }); return (guardedExec as any)(...args) }) as any)
;(globalThis as any).MACRO = { VERSION: '999.0.0-clear-mcp-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
const canonicalCwd = fs.realpathSync(home)
state.setCwdState(canonicalCwd); state.setOriginalCwd(canonicalCwd); state.setProjectRoot(canonicalCwd); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
state.switchSession(input.sid)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('ink')
const { AppStoreContext, getDefaultAppState } = await import('../../../src/state/AppState')
const { createStore } = await import('../../../src/state/store')
const { useManageMCPConnections } = await import('../../../src/services/mcp/useManageMCPConnections')
const { clearServerCache } = await import('../../../src/services/mcp/client')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const clear = (await import('../../../src/commands/clear/index')).default
const original = await clear.load()
const initial = getDefaultAppState()
initial.mcp.pluginReconnectKey = 19
const store = createStore(initial)
const mcpConfig = { 'owned-peer': { type: 'http' as const, url: input.url, scope: 'user' as const } }
function Harness() { useManageMCPConnections(mcpConfig, true); return null }
function view() { return React.createElement(AppStoreContext.Provider, { value: store }, React.createElement(Harness)) }
const stdout = new PassThrough(), stderr = new PassThrough(), stdin = new PassThrough()
const app = render(view(), { stdout, stderr, stdin, exitOnCtrlC: false, patchConsole: false })
function snapshot() {
  const { mcp } = store.getState()
  return { clients: mcp.clients.map(peer => ({ name: peer.name, type: peer.type })), tools: mcp.tools.map(tool => tool.name), commands: mcp.commands.map(command => command.name), resources: Object.keys(mcp.resources), key: mcp.pluginReconnectKey }
}
function connected() { return store.getState().mcp.clients.some(peer => peer.name === 'owned-peer' && peer.type === 'connected') }
function untilConnected() {
  if (connected()) return Promise.resolve()
  return new Promise<void>(resolve => { const off = store.subscribe(() => { if (connected()) { off(); resolve() } }) })
}
let messages: any[] = []
let reset: ReturnType<typeof snapshot> | undefined
const observations: ReturnType<typeof snapshot>[] = []
const unsubscribe = store.subscribe(() => observations.push(snapshot()))
try {
  if (input.holdList) await fetch(new URL('/held', input.url))
  else await untilConnected()
  const before = snapshot()
  const peerBefore = store.getState().mcp.clients.find(peer => peer.type === 'connected')
  await original.call('', {
    setMessages: (update: any) => { messages = update(messages) }, readFileState: createFileStateCacheWithSizeLimit(100), discoveredSkillNames: new Set(), loadedNestedMemoryPaths: new Set(),
    getAppState: store.getState, setAppState: (update: any) => { store.setState(update); if (store.getState().mcp.clients.length === 0) reset = snapshot() }, setConversationId: () => {},
    abortController: new AbortController(), messages, options: { commands: [clear], tools: [], agentDefinitions: { activeAgents: [] } },
  } as any)
  // The actual REPL rerenders after the local-command result. Both original
  // effects read the regenerated session ID; their cleanup cancels config
  // publication but retains connectToServer's existing physical peer promise.
  app.rerender(view())
  if (input.holdList) await fetch(new URL('/release', input.url))
  await untilConnected()
  const peerAfter = store.getState().mcp.clients.find(peer => peer.type === 'connected')
  const after = snapshot()
  unsubscribe(); app.unmount()
  await clearServerCache('owned-peer', mcpConfig['owned-peer'])
  if (caps.blocked.length) throw new Error('Source MCP clear escaped private boundary: ' + JSON.stringify({ blocked: caps.blocked, externalProbes }))
  process.stdout.write(JSON.stringify({ before, reset, after, sameClient: !!peerBefore && !!peerAfter && (peerBefore as any).client === (peerAfter as any).client, sidChanged: state.getSessionId() !== input.sid, observations, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }))
} finally { unsubscribe(); app.unmount() }
process.exit(0)
