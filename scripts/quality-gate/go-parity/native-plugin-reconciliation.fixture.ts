import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual plugin startup reconciler')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_RECONCILIATION_SOURCE !== '1') throw new Error('Pinned plugin startup Source guard required')
fs.mkdirSync(join(home, 'bin'), { recursive: true })
process.env.PATH = join(home, 'bin')
if (input.seedDirs?.length) process.env.CLAUDE_CODE_PLUGIN_SEED_DIR = input.seedDirs.join(process.platform === 'win32' ? ';' : ':')
if (input.zipCache) {
  process.env.CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE = '1'
  process.env.CLAUDE_CODE_PLUGIN_CACHE_DIR = join(home, '.claude', 'plugins')
}
const absentPaths = absentManagedSettingsFixturePaths()
const packagedRipgrep = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(packagedRipgrep)) absentPaths.push(packagedRipgrep)
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let parent = spelling; ; parent = dirname(parent)) {
    for (const relative of ['.git', '.claude/settings.json', '.claude/settings.local.json', '.claude/agents', '.agents/agents']) {
      const path = join(parent, relative)
      if (!fs.existsSync(path)) absentPaths.push(path)
    }
    if (dirname(parent) === parent) break
  }
}
await import('yoga-layout')
const caps = await installClientFixtureCapabilities(home, { absentPaths, ...(input.url ? { loopbackFetchOrigin: new URL(input.url).origin } : {}) })
let afterManifestRead: (() => Promise<void>) | undefined
const heldReadPaths: string[] = []
if (input.operation === 'loader-held-full') {
  const { spyOn } = await import('bun:test')
  const io = await import('node:fs/promises')
  const readFile = fs.promises.readFile
  const heldRead = async (...args: any[]) => {
    const result = await (readFile as any)(...args)
    heldReadPaths.push(String(args[0]))
    if (afterManifestRead && String(args[0]).endsWith('/.claude-plugin/plugin.json')) await afterManifestRead()
    return result
  }
  fs.promises.readFile = heldRead as any
  spyOn(io, 'readFile').mockImplementation(heldRead as any)
}
// Source uses Axios for marketplace URL downloads. Bind its transport to the
// same genuine guarded loopback fetch; Source parsing/headers/error handling
// and marketplace persistence remain the original consumers.
if (input.url) {
  const { default: axios, AxiosError } = await import('axios')
  axios.defaults.adapter = async request => {
    const response = await fetch(axios.getUri(request), { method: request.method?.toUpperCase(), headers: request.headers.toJSON() as HeadersInit, signal: request.signal as AbortSignal })
    const data = await response.text()
    const result = { data: request.responseType === 'json' ? JSON.parse(data) : data, status: response.status, statusText: response.statusText, headers: Object.fromEntries(response.headers), config: request }
    if (response.status >= 300) throw new AxiosError('Request failed with status code ' + response.status, AxiosError.ERR_BAD_REQUEST, request, undefined, result)
    return result
  }
}
;(globalThis as any).MACRO = { VERSION: '999.0.0-plugin-reconciliation', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
const OriginalDate = Date
globalThis.Date = new Proxy(OriginalDate, { construct: (target, args) => Reflect.construct(target, args.length ? args : [1791244800000]) })
Date.now = () => 1791244800000
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
const cwd = fs.realpathSync(input.cwd ?? join(home, 'workspace'))
state.setCwdState(cwd)
state.setOriginalCwd(cwd)
state.setProjectRoot(cwd)
state.setIsInteractive(input.operation === 'startup' || input.operation === 'interactive-mount')
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
if (input.additional?.length) state.setAdditionalDirectoriesForClaudeMd(input.additional)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDeclaredMarketplaces, loadKnownMarketplacesConfigSafe } = await import('../../../src/utils/plugins/marketplaceManager')
const { diffMarketplaces, reconcileMarketplaces } = await import('../../../src/utils/plugins/reconciler')
const before = await loadKnownMarketplacesConfigSafe()
const declared = getDeclaredMarketplaces()
const diff = diffMarketplaces(input.declared ?? declared, input.known ?? before)
const observations: any[] = []
let result: any
let final: any
if (input.operation === 'canonical-lifetime') {
  observations.push(diffMarketplaces(input.declared, input.known))
  fs.writeFileSync(input.gitFile, input.gitReplacement)
  observations.push(diffMarketplaces(input.declared, input.known))
  for (const projectRoot of input.evictCanonicalRoots) diffMarketplaces(input.declared, input.known, { projectRoot })
  observations.push(diffMarketplaces(input.declared, input.known))
} else if (input.operation === 'reconcile') {
  result = await reconcileMarketplaces({
    onProgress: event => observations.push(event),
    ...(input.zip ? { skip: (_name: string, source: any) => !['github', 'git', 'url', 'settings'].includes(source.source) } : {}),
  })
} else if (input.operation === 'startup') {
  const { getDefaultAppState } = await import('../../../src/state/AppState')
  let appState = getDefaultAppState()
  appState.mcp.pluginReconnectKey = 19
  const { performStartupChecks } = await import('../../../src/utils/plugins/performStartupChecks')
  await performStartupChecks(update => {
    appState = update(appState)
    observations.push({ installationStatus: appState.plugins.installationStatus, needsRefresh: appState.plugins.needsRefresh })
  })
  final = { installationStatus: appState.plugins.installationStatus, needsRefresh: appState.plugins.needsRefresh, enabled: appState.plugins.enabled.map(plugin => plugin.name), commands: appState.plugins.commands.map(command => command.name), reconnectKey: appState.mcp.pluginReconnectKey }
} else if (input.operation === 'interactive-mount') {
  // The private piped renderer has an explicit terminal geometry. Ink's
  // ordinary fallback then needs neither /dev/tty nor a public tput process.
  process.env.COLUMNS = '100'
  process.env.LINES = '35'
  const React = await import('react')
  const { render } = await import('ink')
  const { PassThrough } = await import('node:stream')
  const { AppStoreContext, getDefaultAppState } = await import('../../../src/state/AppState')
  const { createStore } = await import('../../../src/state/store')
  const { useManagePlugins } = await import('../../../src/hooks/useManagePlugins')
  const loader = await import('../../../src/utils/plugins/pluginLoader')
  const cached = await loader.loadAllPluginsCacheOnly()
  // setup/main.tsx resolves commands/agent definitions and SessionStart loads
  // plugin hooks before rendering the REPL. Exercise those original memos.
  await (await import('../../../src/utils/plugins/loadPluginCommands')).getPluginCommands()
  await (await import('../../../src/utils/plugins/loadPluginAgents')).loadPluginAgents()
  await (await import('../../../src/utils/plugins/loadPluginHooks')).loadPluginHooks()
  if (input.addBeforeMount) {
    // main.tsx's getCommands -> loadAllCommands calls this same original memo
    // before mounting the REPL. A later full load does not clear that memo.
    fs.writeFileSync(join(home, 'market', 'plugins', 'sample', 'commands', 'added.md'), '---\ndescription: Added after startup command memo\n---\nACTUAL_ADDED_COMMAND')
  }
  const initial = getDefaultAppState()
  initial.plugins.needsRefresh = !!input.needsRefresh
  initial.mcp.pluginReconnectKey = 19
  const store = createStore(initial)
  const notifications: any[] = []
  const seen = new Set<string>()
  function inspect() {
    for (const notification of [store.getState().notifications.current, ...store.getState().notifications.queue]) {
      if (notification && !seen.has(notification.key)) {
        seen.add(notification.key)
        notifications.push(notification)
      }
    }
  }
  let resolveLoaded!: () => void
  const loaded = new Promise<void>(resolve => { resolveLoaded = resolve })
  const unsubscribe = store.subscribe(() => {
    inspect()
    if (store.getState().plugins.enabled !== initial.plugins.enabled) resolveLoaded()
  })
  function Harness() { useManagePlugins({ enabled: input.enabled !== false }); return null }
  const app = render(React.createElement(AppStoreContext.Provider, { value: store }, React.createElement(Harness)), { stdout: new PassThrough(), stderr: new PassThrough(), stdin: new PassThrough(), exitOnCtrlC: false, patchConsole: false })
  if (input.enabled !== false) await loaded
  const current = store.getState()
  const project = (plugins: typeof cached) => ({ enabled: plugins.enabled.map(plugin => plugin.name), disabled: plugins.disabled.map(plugin => plugin.name), errors: plugins.errors, slots: plugins.enabled.map(plugin => ({ name: plugin.name, mcpServers: plugin.mcpServers ?? null, lspServers: plugin.lspServers ?? null })) })
  final = { ...project(current.plugins), commands: current.plugins.commands.map(command => command.name), notifications, reconnectKey: current.mcp.pluginReconnectKey, agentsUnchanged: current.agentDefinitions === initial.agentDefinitions, cachedBefore: project(cached), cachedAfter: project(await loader.loadAllPluginsCacheOnly()) }
  if (input.addBeforeMount) {
    const { refreshActivePlugins } = await import('../../../src/utils/plugins/refresh')
    await refreshActivePlugins(update => store.setState(update))
    final.commandsAfterReload = store.getState().plugins.commands.map(command => command.name)
  }
  unsubscribe()
  app.unmount()
} else if (input.operation === 'headless') {
  result = await (await import('../../../src/utils/plugins/headlessPluginInstall')).installPluginsForHeadless()
} else if (input.operation === 'loader-held-full') {
  const loader = await import('../../../src/utils/plugins/pluginLoader')
  const cached = await loader.loadAllPluginsCacheOnly()
  let ready!: () => void, release!: () => void
  const held = new Promise<void>(resolve => { ready = resolve })
  const released = new Promise<void>(resolve => { release = resolve })
  let paused = false
  afterManifestRead = async () => {
    if (!paused) {
      paused = true
      ready()
      await released
    }
  }
  let fullCompleted = false
  const full = loader.loadAllPlugins().then(value => { fullCompleted = true; return value })
  await Promise.race([held, full.then(() => { throw new Error('Original full consumer completed before the held manifest read: ' + JSON.stringify(heldReadPaths)) })])
  const during = await loader.loadAllPluginsCacheOnly()
  observations.push({ fullCompleted, sameCachedPromiseResult: cached === during, enabled: during.enabled.map(plugin => plugin.name) })
  release()
  const after = await full
  observations.push({ fullCompleted, fullEnabled: after.enabled.map(plugin => plugin.name) })
} else if (input.operation === 'loader-lifetime') {
  const loader = await import('../../../src/utils/plugins/pluginLoader')
  const project = (value: Awaited<ReturnType<typeof loader.loadAllPlugins>>) => ({ enabled: value.enabled.map(plugin => ({ name: plugin.name, description: plugin.manifest.description ?? '' })), errors: value.errors.map(error => error.type) })
  observations.push(project(await loader.loadAllPluginsCacheOnly()))
  const full = await loader.loadAllPlugins()
  observations.push(project(full))
  if (full.enabled.length !== 1) throw new Error('Private loader lifetime requires exactly one actual installed plugin')
  const path = join(full.enabled[0]!.path, '.claude-plugin', 'plugin.json')
  const manifest = JSON.parse(fs.readFileSync(path, 'utf8'))
  manifest.description = 'NEW_PHYSICAL_MANIFEST'
  fs.writeFileSync(path, JSON.stringify(manifest))
  observations.push(project(await loader.loadAllPluginsCacheOnly()))
  observations.push(project(await loader.loadAllPlugins()))
  loader.clearPluginCache('owned source lifetime consumer')
  observations.push(project(await loader.loadAllPluginsCacheOnly()))
  observations.push(project(await loader.loadAllPlugins()))
} else if (input.operation !== 'diff') throw new Error('Unowned plugin startup operation')
const known = await loadKnownMarketplacesConfigSafe()
const files: Record<string, unknown> = {}
for (const relative of ['settings.json', 'plugins/installed_plugins.json', 'plugins/flagged-plugins.json']) {
  const path = join(home, '.claude', relative)
  if (fs.existsSync(path)) files[relative] = JSON.parse(fs.readFileSync(path, 'utf8'))
}
const zipMarketplaces: Record<string, unknown> = {}
if (input.zipCache) {
  const directory = join(home, '.claude', 'plugins', 'marketplaces')
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    if (entry.isFile() && entry.name.endsWith('.json')) zipMarketplaces[entry.name] = JSON.parse(fs.readFileSync(join(directory, entry.name), 'utf8'))
  }
}
if (caps.blocked.length) throw new Error('Plugin startup attempted unowned primitive: ' + JSON.stringify(caps.blocked))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declared, diff, result, observations, final, known, files, zipMarketplaces, capabilities: { blocked: caps.blocked, secureReads: caps.secureReads } }), error => error ? reject(error) : resolve()))
process.exit(0)
