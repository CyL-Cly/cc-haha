import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import * as http from 'node:http'
import * as net from 'node:net'
import { mock, spyOn } from 'bun:test'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import axios from 'axios'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual MCPSettings auth UI and connection manager')
if (typeof input.now === 'number') Date.now = () => input.now
if (typeof input.cacheRaw === 'string') fs.writeFileSync(join(home, '.claude', 'mcp-needs-auth-cache.json'), input.cacheRaw)
if (typeof input.credentialsRaw === 'string') fs.writeFileSync(join(home, '.claude', '.credentials.json'), input.credentialsRaw)
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_AUTH_UI_SOURCE !== '1') throw new Error('Pinned owned MCP auth UI required')
const absentPaths = absentManagedSettingsFixturePaths()
const managedMCP = join(dirname(absentPaths[0]!), 'managed-mcp.json')
if (!fs.existsSync(managedMCP)) absentPaths.push(managedMCP)
for (const path of ['/home/claude/.claude/remote/.session_ingress_token', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absentPaths.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let parent = spelling; ; parent = dirname(parent)) {
    const candidate = join(parent, '.git')
    if (!fs.existsSync(candidate)) absentPaths.push(candidate)
    if (dirname(parent) === parent) break
  }
}
await import('yoga-layout')
const originalFetch = globalThis.fetch.bind(globalThis)
const originalCreateServer = http.createServer
const originalSocketConnect = net.Socket.prototype.connect
const originalServe = Bun.serve.bind(Bun)
const browser = join(home, 'private-browser')
const caps = await installClientFixtureCapabilities(home, {
  absentPaths, privateExecutableFiles: [browser], loopbackFetchOrigin: new URL(input.url).origin,
  approvedSpawn: ({ command, args }) => command === browser && args.length === 1 && new URL(args[0]!).origin === new URL(input.url).origin
})
// Extend the guard by exactly the original OAuth callback listener primitive.
// Business request routing, state validation, token exchange and persistence
// are the unchanged Source code. No general native listener is granted.
const callbackServers: any[] = []
Object.assign(createRequire(import.meta.url)('node:http'), { createServer: (...args: any[]) => {
  const server = (originalCreateServer as any)(...args)
  const listen = server.listen
  server.listen = function (port: number, host: string, ...rest: any[]) {
    if (port !== input.port || host !== '127.0.0.1') throw new Error('Unowned OAuth callback listener')
    callbackServers.push(server)
    return listen.call(this, port, host, ...rest)
  }
  return server
} })
Bun.serve = ((options: any) => {
  if (options.port !== input.port || options.hostname !== '127.0.0.1') throw new Error('Unowned Bun callback listener')
  return originalServe(options)
}) as typeof Bun.serve
syncBuiltinESMExports()
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const fallback = createFallbackStorage({ name: 'owned-unavailable-keychain', read: () => null, readAsync: async () => null, update: () => ({ success: false }), delete: () => false }, plainTextStorage)
mock.module('../../../src/utils/secureStorage/index.ts', () => ({ getSecureStorage: () => fallback }))
// Axios's native socket is a transport primitive. Preserve its fully prepared
// original revocation request and only route that exact owned endpoint.
axios.defaults.adapter = async request => {
  const target = new URL(request.url!)
  if (target.origin !== new URL(input.url).origin || target.pathname !== '/revoke' || request.method !== 'post') throw new Error('Unowned original OAuth axios request')
  const response = await fetch(target, { method: 'POST', headers: request.headers.toJSON() as any, body: request.data })
  return { data: await response.text(), status: response.status, statusText: response.statusText, headers: {}, config: request }
}
;(globalThis as any).MACRO = { VERSION: '999.0.0-mcp-auth-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
const cwd = fs.realpathSync(home)
state.setCwdState(cwd)
state.setOriginalCwd(cwd)
state.setProjectRoot(cwd)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const { AppStoreContext, getDefaultAppState } = await import('../../../src/state/AppState')
const { createStore } = await import('../../../src/state/store')
const { MCPConnectionManager } = await import('../../../src/services/mcp/MCPConnectionManager')
const { MCPSettings } = await import('../../../src/components/mcp/MCPSettings')
const { clearServerCache, clearMcpAuthCache, getMcpToolsCommandsAndResources, reconnectMcpServerImpl } = await import('../../../src/services/mcp/client')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const mcpLogFailures: string[] = []
const mcpLogCompletions: string[] = []
let diagnosticSink: typeof import('../../../src/utils/errorLogSink') | undefined
if (input.observeMCPLog) {
  diagnosticSink = await import('../../../src/utils/errorLogSink')
  diagnosticSink.initializeErrorLogSink()
  const log = await import('../../../src/utils/log')
  const actual = log.logMCPError
  spyOn(log, 'logMCPError').mockImplementation((name, error) => { actual(name, error); mcpLogFailures.push(String(error)) })
  const actualDebug = log.logMCPDebug
  spyOn(log, 'logMCPDebug').mockImplementation((name, message) => { actualDebug(name, message); mcpLogCompletions.push(message) })
}
const store = createStore(getDefaultAppState())
const handlers = { current: new Map() }
const pending = { current: null }
const active = new Set(['Confirmation'])
const provider = {
  bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: handlers, pendingChordRef: pending, pendingChord: null,
  setPendingChord: (value: any) => pending.current = value, activeContexts: active,
  registerActiveContext: (value: any) => active.add(value), unregisterActiveContext: (value: any) => active.delete(value)
}
const mcpConfig = { auth: { type: 'http' as const, url: input.url, oauth: { callbackPort: input.port }, scope: 'user' as const } }
const completions: any[] = []
let generation = 0
let menuOpen = false
function view() {
  return React.createElement(AppStoreContext.Provider, { value: store },
    React.createElement(KeybindingProvider, provider as any,
      React.createElement(MCPConnectionManager, { dynamicMcpConfig: mcpConfig, isStrictMcpConfig: true },
        menuOpen ? React.createElement(MCPSettings, { key: generation, onComplete: (message: any) => completions.push(message) }) : null)))
}
const stdin = new PassThrough() as any
stdin.isTTY = true
stdin.setRawMode = () => {}
stdin.ref = () => stdin
stdin.unref = () => stdin
const stdout = new PassThrough() as any
stdout.columns = 120
stdout.isTTY = false
let output = ''
stdout.on('data', (chunk: any) => output += chunk.toString())
const app = await render(view(), { stdin, stdout, stderr: stdout, exitOnCtrlC: false, patchConsole: false })
const frames: any[] = []
const snapshot = () => ({ clients: store.getState().mcp.clients.map(client => ({ name: client.name, type: client.type })), tools: store.getState().mcp.tools.map(tool => tool.name) })
const deadline = Date.now() + 18000
async function until(predicate: () => boolean, label: string) {
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error(label + ': ' + JSON.stringify({ blocked: caps.blocked }) + ' ' + output.slice(-18000))
    await new Promise(resolve => setTimeout(resolve, 10))
  }
  for (let index = 0; index < 6; index++) await new Promise(resolve => setImmediate(resolve))
}
async function key(value: string) {
  output = ''
  stdin.write(value)
  for (let index = 0; index < 6; index++) await new Promise(resolve => setImmediate(resolve))
}
let receipt: any
try {
  await until(() => snapshot().clients.some(client => input.mode === 'cache-probe' ? client.type !== 'pending' : client.type === 'needs-auth'), 'initial actual MCP status')
  if (input.mode === 'cache-probe') {
    // Join the original writeChain through a clear of an absent reserved ID;
    // it neither deletes nor prepares the original server's cache entry.
    await clearMcpAuthCache('owned-absent-cache-flush')
    const path = join(home, '.claude', 'mcp-needs-auth-cache.json')
    receipt = { state: snapshot(), cacheRaw: fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : null, credentialsRaw: fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8'), caps: { blocked: caps.blocked } }
    if (input.followup === 'external-clear') {
      fs.writeFileSync(path, input.nextCacheRaw)
      const results: any[] = []
      await getMcpToolsCommandsAndResources(result => results.push({ type: result.client.type, tools: result.tools.map(tool => tool.name) }), mcpConfig)
      receipt.followup = results[0]
    } else if (input.followup === 'explicit-reconnect') {
      const result = await reconnectMcpServerImpl('auth', mcpConfig.auth)
      receipt.followup = { type: result.client.type, tools: result.tools.map(tool => tool.name) }
    }
    if (input.followup) {
      await clearMcpAuthCache('owned-absent-cache-flush')
      receipt.nextCacheRaw = fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : null
      receipt.nextCredentialsRaw = fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')
    }
  } else if (input.mode === 'auth-tool' || input.mode === 'auth-tool-pending' || input.mode === 'auth-tool-late-error' || input.mode === 'auth-tool-agent') {
    const tool = store.getState().mcp.tools.find(tool => tool.name === 'mcp__auth__authenticate')!
    if (!tool) throw new Error('Original actual manager omitted authentication tool')
    const parsed = tool.inputSchema.parse({ dropped: 'Source empty object strips extra properties' })
    const permission = await tool.checkPermissions(parsed, {} as any)
    let callContext: any = { setAppState: update => store.setState(update) }
    let agentAdmission: any
    if (input.mode === 'auth-tool-agent') {
      if (!input.observeMCPLog) throw new Error('Actual Agent probe needs the original completion log observer')
      const { runAgent } = await import('../../../src/tools/AgentTool/runAgent')
      const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
      const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
      const definition = { agentType: 'owned-auth-agent', source: 'projectSettings', whenToUse: 'Owned auth consumer', getSystemPrompt: () => 'Owned auth consumer' }
      const parent: any = {
        options: { commands: [], tools: store.getState().mcp.tools, mainLoopModel: 'fixture-model', mcpClients: store.getState().mcp.clients, mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [definition], allAgents: [definition] } },
        abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: store.getState, setAppState: (update: any) => store.setState(update), setResponseLength: () => {}, messages: []
      }
      const stop = new Error('Actual runAgent reached its pre-query consumer')
      const generator = runAgent({ agentDefinition: definition as any, promptMessages: [], toolUseContext: parent, canUseTool: async () => ({ behavior: 'allow' }) as any, isAsync: Boolean(input.background), querySource: 'agent:custom', availableTools: store.getState().mcp.tools, override: { agentId: 'actual-auth-fixture-agent' as any, userContext: {}, systemContext: {}, systemPrompt: asSystemPrompt([]) }, onCacheSafeParams: params => { callContext = params.toolUseContext; throw stop } })
      try { await generator.next(); throw new Error('Agent never reached actual command/tool admission') } catch (error) { if (error !== stop) throw error }
      agentAdmission = { background: Boolean(input.background), sameTool: callContext.options.tools.find((candidate: any) => candidate.name === tool.name) === tool, tools: callContext.options.tools.map((candidate: any) => candidate.name), clients: callContext.options.mcpClients.map((client: any) => ({ name: client.name, type: client.type })), sharesSetAppState: callContext.setAppState === parent.setAppState }
      if (!agentAdmission.sameTool) throw new Error('Actual runAgent removed/recreated inherited authentication tool')
    }
    const result = await tool.call(parsed, callContext)
    const data = result.data as any
    if (data.status !== 'auth_url' || !data.authUrl || fs.existsSync(join(home, 'browser-url'))) throw new Error('Original authentication tool did not return URL without browser')
    const mapped = tool.mapToolResultToToolResultBlockParam(data, 'owned-auth-tool-id')
    const authorization = new URL(data.authUrl)
    const callback = new URL(authorization.searchParams.get('redirect_uri')!)
    if (callback.hostname !== 'localhost' || Number(callback.port) !== input.port || callback.pathname !== '/callback') throw new Error('Unowned original auth callback')
    callback.hostname = '127.0.0.1'
    callback.searchParams.set('code', 'private-auth-code')
    callback.searchParams.set('state', authorization.searchParams.get('state')!)
    if (input.mode === 'auth-tool-late-error') {
      callback.searchParams.delete('code')
      callback.searchParams.set('error', 'access_denied')
      callback.searchParams.set('error_description', 'private owned denied')
    }
    if (input.mode !== 'auth-tool-pending') {
      const response = await originalFetch(callback)
      if (input.mode === 'auth-tool-late-error') {
        await until(() => mcpLogFailures.some(value => value.startsWith('OAuth flow failed after tool-triggered start:')), 'actual original background auth failure log')
      } else {
        if (!response.ok) throw new Error('Original callback failed: ' + response.status)
        if (agentAdmission?.background) await until(() => mcpLogCompletions.includes('OAuth complete, reconnected with 1 tool(s)'), 'actual async Agent OAuth completion')
        else await until(() => snapshot().tools.includes('mcp__auth__inspect') && !snapshot().tools.includes(tool.name), 'actual auth tool background OAuth+catalog update')
      }
    }
    const { createMcpAuthTool } = await import('../../../src/tools/McpAuthTool/McpAuthTool')
    const unsupported: any[] = []
    for (const config of [{ type: 'claudeai-proxy', url: input.url, id: 'owned-connector', scope: 'user' }, { type: 'stdio', command: 'unexecuted', scope: 'user' }]) {
      unsupported.push((await createMcpAuthTool('auth', config as any).call({}, {} as any)).data)
    }
    receipt = { pending: input.mode === 'auth-tool-pending', parsed, permission, name: tool.name, description: await tool.description(), prompt: await tool.prompt(), concurrencySafe: tool.isConcurrencySafe(parsed), readOnly: tool.isReadOnly(parsed), threshold: tool.maxResultSizeChars, mapped, data, connected: snapshot(), unsupported, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }
    if (agentAdmission) receipt.agentAdmission = { ...agentAdmission, capturedToolsAfterAuth: callContext.options.tools.map((candidate: any) => candidate.name) }
    if (diagnosticSink) {
      diagnosticSink._flushLogWritersForTesting()
      const path = diagnosticSink.getMCPLogsPath('auth')
      receipt.diagnostics = fs.readFileSync(path, 'utf8').trim().split('\n').map(row => JSON.parse(row))
      diagnosticSink._clearLogWritersForTesting()
    }
  } else {
  // Normal REPL invokes this local command after startup. Mount the same menu
  // at that user boundary, rather than concurrently with the manager's mount.
  menuOpen = true
  app.rerender(view())
  await until(() => output.includes('auth'), 'actual MCPSettings command mount')
  await key('\r')
  await until(() => output.includes('Authenticate') && output.includes('Auth MCP Server'), 'actual remote menu')
  await key('\r')
  await until(() => fs.existsSync(join(home, 'browser-url')) && output.includes('address bar'), 'actual browser and manual callback UI')
  frames.push({ stage: 'manual-callback', output, state: snapshot() })
  const address = fs.readFileSync(join(home, 'browser-url'), 'utf8')
  const query = new URL(address).searchParams
  const callback = query.get('redirect_uri') + '?code=private-auth-code&state=' + encodeURIComponent(query.get('state')!)
  await key(callback)
  await until(() => output.includes('private-auth-code'), 'manual callback actually entered original TextInput')
  await key('\r')
  await until(() => completions.includes('Authentication successful. Connected to auth.') && snapshot().tools.includes('mcp__auth__inspect'), 'actual original OAuth+reconnect')
  const connected = snapshot()
  generation++
  output = ''
  app.rerender(view())
  await until(() => output.includes('auth') && output.includes('connected'), 'actual connected menu list')
  await key('\r')
  await until(() => output.includes('Clear authentication'), 'actual authenticated remote menu')
  await key('\x1b[B')
  await key('\x1b[B')
  await key('\r')
  await until(() => completions.includes('Authentication cleared for auth.') && snapshot().clients.some(client => client.type === 'failed') && snapshot().tools.length === 0, 'actual original revoke+clear')
  const cleared = snapshot()
  generation++
  output = ''
  app.rerender(view())
  await until(() => output.includes('Manage MCP servers') && output.includes('auth'), 'post-clear list')
  await key('\r')
  await until(() => output.includes('Authenticate'), 'post-clear remote menu')
  fs.rmSync(join(home, 'browser-url'), { force: true })
  await key('\r')
  await until(() => fs.existsSync(join(home, 'browser-url')) && output.includes('address bar'), 'second actual original OAuth mount')
  const finalURL = new URL(fs.readFileSync(join(home, 'browser-url'), 'utf8'))
  app.unmount()
  // The original component's cleanup aborts the same real listener. Probe it
  // physically; an unowned URL is never fetched by this fixture.
  let listenerClosed = false
  while (!listenerClosed) {
    listenerClosed = await new Promise<boolean>(resolve => {
      const socket = new net.Socket()
      socket.once('error', () => { socket.destroy(); resolve(true) })
      socket.once('connect', () => { socket.destroy(); resolve(false) })
      originalSocketConnect.call(socket, input.port, '127.0.0.1')
    })
    if (Date.now() >= deadline) throw new Error('Original unmounted callback listener survived')
    if (!listenerClosed) await new Promise(resolve => setTimeout(resolve, 5))
  }
  frames.push({ stage: 'clear', state: cleared })
  receipt = { completions, connected, cleared, frames, listenerClosed, finalRedirect: finalURL.searchParams.get('redirect_uri'), caps: { blocked: caps.blocked, secureReads: caps.secureReads } }
  }
  if (caps.blocked.length) throw new Error('Original MCP auth escaped private capability: ' + JSON.stringify(caps.blocked))
  await clearServerCache('auth', mcpConfig.auth)
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(receipt), error => error ? reject(error) : resolve()))
} finally {
  app.unmount()
  await clearServerCache('auth', mcpConfig.auth)
}
if (input.mode !== 'auth-tool-pending') process.exit(0)
