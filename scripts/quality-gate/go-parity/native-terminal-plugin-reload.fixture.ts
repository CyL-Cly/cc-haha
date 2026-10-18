import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual reload-plugins complete local command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_RELOAD_SOURCE !== '1') throw new Error('Missing reload Source guard')
// TypeScript performs a module-location case-sensitivity probe at load time.
// Load this fixture compiler before the business capability boundary.
const ts = input.capturedOptions ? createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript') : null
const sdk = process.env.CC_HAHA_MCPB_SOURCE_SDK
if (!sdk || JSON.parse(fs.readFileSync(join(sdk, 'package.json'), 'utf8')).version !== '1.2.0') throw new Error('Verified official MCPB reference required')
const schemas = await import(pathToFileURL(join(sdk, 'dist/schemas/latest.js')).href)
const compiler = await import(pathToFileURL(join(sdk, 'dist/shared/config.js')).href)
mock.module('@anthropic-ai/mcpb', () => ({ ...schemas, ...compiler }))
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = dirname(home); ; parent = dirname(parent)) {
  const candidate = join(parent, '.git')
  if (!fs.existsSync(candidate)) absentPaths.push(candidate)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths })
for (const [path, encoded] of Object.entries(input.files ?? {})) {
  if (!path.startsWith(home + '/')) throw new Error('Unowned activation seed')
  fs.mkdirSync(dirname(path), { recursive: true })
  fs.writeFileSync(path, Buffer.from(encoded as string, 'base64'))
}
fs.mkdirSync(join(home, 'managed'), { recursive: true })
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const managed = await import('../../../src/utils/settings/managedPath')
if (managed.getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
;(globalThis as any).MACRO = { VERSION: '999.0.0-reload-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected Source OS primitive')
  caps.secureReads.push('owned-empty-legacy'); return null
} }))
const state = await import('../../../src/bootstrap/state')
process.env.CLAUDE_CODE_ENTRYPOINT = input.entrypoint
const work = input.workDir ?? home
state.setCwdState(work); state.setOriginalCwd(work); state.setProjectRoot(work); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppState')
const command = (await import('../../../src/commands/reload-plugins/index')).default
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
let app = getDefaultAppState()
if (input.activation || input.capturedOptions) {
  const { installPluginFromMarketplace } = await import('../../../src/utils/plugins/pluginInstallationHelpers')
  const operations = await import('../../../src/services/plugins/pluginOperations')
  const { updatePluginOp } = operations
  const { refreshActivePlugins } = await import('../../../src/utils/plugins/refresh')
  const { clearAllCaches } = await import('../../../src/utils/plugins/cacheUtils')
  const { savePluginOptions } = await import('../../../src/utils/plugins/pluginOptionsStorage')
  const installed = await installPluginFromMarketplace({ pluginId: 'zeta@mkt', marketplaceName: 'mkt', entry: { name: 'zeta', source: './plugins/zeta' }, scope: 'user' })
  if (!installed.success) throw new Error('Original activation install failed ' + JSON.stringify(installed))
  await refreshActivePlugins(update => { app = update(app) })
  if (input.capturedOptions) {
    const text = fs.readFileSync(new URL('../../../src/commands/plugin/ManagePlugins.tsx', import.meta.url), 'utf8')
    const parsed = ts.createSourceFile('ManagePlugins.tsx', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
    let operation: any
    function visit(node: any) {
      if (ts.isVariableDeclaration(node) && node.name.getText(parsed) === 'handleSingleOperation') operation = node.initializer
      ts.forEachChild(node, visit)
    }
    visit(parsed)
    if (!operation) throw new Error('Original operation caller missing')
    const { loadAllPlugins } = await import('../../../src/utils/plugins/pluginLoader')
    const plugin = (await loadAllPlugins()).enabled.find(plugin => plugin.source === 'zeta@mkt')!
    if (!plugin) throw new Error('Actual selected plugin missing')
    if (input.capturedOptions === 'enable') {
      const disabled = await operations.disablePluginOp('zeta@mkt')
      if (!disabled.success) throw new Error('Original pre-enable disable failed ' + JSON.stringify(disabled))
      clearAllCaches()
    }
    const selectedPlugin = { plugin, marketplace: 'mkt', scope: 'user' }
    if (input.capturedOptions === 'update') {
      const manifestPath = join(work, 'market/plugins/zeta/.claude-plugin/plugin.json')
      const changed = JSON.parse(fs.readFileSync(manifestPath, 'utf8'))
      changed.version = '2.0.0'
      changed.userConfig = { replacement: { type: 'string', title: 'Replacement schema', description: 'Actual replacement option', required: true } }
      fs.writeFileSync(manifestPath, JSON.stringify(changed))
    }
    const settings = await import('../../../src/utils/settings/settings')
    const installedStore = await import('../../../src/utils/plugins/installedPluginsManager')
    const directories = await import('../../../src/utils/plugins/pluginDirectories')
    let view: any = 'plugin-details'
    let processError: any = null
    let result: any = null
    let parent: any = null
    const deps = { selectedPlugin, ...operations, ...installedStore, ...directories, clearAllCaches,
      getSettings_DEPRECATED: settings.getSettings_DEPRECATED,
      setIsProcessing: () => {}, setProcessError: (value: any) => { processError = value }, setViewState: (value: any) => { view = value },
      setResult: (value: any) => { result = value }, setParentViewState: (value: any) => { parent = value }, onManageComplete: () => {},
      logError: (error: any) => { throw error }, toError: (error: any) => error }
    const code = ts.transpileModule('const operation = ' + operation.getText(parsed), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
    const original = new Function(...Object.keys(deps), code + ';return operation')(...Object.values(deps))
    await original(input.capturedOptions)
    const React = await import('react')
    const { PassThrough } = await import('node:stream')
    const { render } = await import('../../../src/ink')
    const { AppStateProvider } = await import('../../../src/state/AppState')
    const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
    const { parseBindings } = await import('../../../src/keybindings/parser')
    const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
    const { PluginOptionsFlow } = await import('../../../src/commands/plugin/PluginOptionsFlow')
    const stdin = new PassThrough() as any
    stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
    const stdout = new PassThrough() as any
    stdout.isTTY = false; stdout.columns = 100
    let output = ''; stdout.on('data', chunk => { output += chunk.toString() })
    const active = new Set(['Confirmation'])
    const provider = { bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: { current: new Map() }, pendingChordRef: { current: null }, pendingChord: null,
      setPendingChord: () => {}, activeContexts: active, registerActiveContext: (context: string) => active.add(context), unregisterActiveContext: (context: string) => active.delete(context) }
    const outcomes: any[] = []
    const node = React.createElement(AppStateProvider, { initialState: app }, React.createElement(KeybindingProvider, provider as any,
      React.createElement(PluginOptionsFlow, { plugin, pluginId: 'zeta@mkt', onDone: (...args: any[]) => outcomes.push(args) })))
    const instance = await render(node, { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
    const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 30)) }
    await settle()
    const mounted = output
    for (const key of input.keys ?? ['\x1b[200~Captured region\x1b[201~', '\r']) { stdin.write(key); await settle() }
    instance.unmount(); await settle()
    if (caps.blocked.length) throw new Error('Actual captured flow escaped fixture ' + JSON.stringify(caps.blocked))
    await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ view, processError, result, parent, mounted, outcomes, settings: settings.getSettings_DEPRECATED(), installed: installedStore.loadInstalledPluginsV2(), caps: { blocked: caps.blocked } }), error => error ? reject(error) : resolve()))
    process.exit(0)
  }
  const { getCommands } = await import('../../../src/commands')
  await getCommands(work)
  const selected = app.plugins.commands.find(row => row.name === 'zeta:run') as any
  if (!selected) throw new Error('Original active Skill missing')
  const context = { getAppState: () => app, abortController: new AbortController() }
  const first = await selected.getPromptForCommand('', context)
  const plugin = join(work, 'market/plugins/zeta')
  const manifestPath = join(plugin, '.claude-plugin/plugin.json')
  const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'))
  manifest.version = '2.0.0'
  fs.writeFileSync(manifestPath, JSON.stringify(manifest))
  fs.writeFileSync(join(plugin, 'skills/run/SKILL.md'), '---\ndescription: Actual updated Skill\n---\nUPDATE_SKILL_TWO_734')
  const updated = await updatePluginOp('zeta@mkt', 'user')
  if (!updated.success || updated.alreadyUpToDate) throw new Error('Original actual update failed ' + JSON.stringify(updated))
  clearAllCaches()
  savePluginOptions('zeta@mkt', { region: 'Updated region' }, manifest.userConfig)
  const beforeReload = await selected.getPromptForCommand('', context)
  const globalBeforeReload = await ((await getCommands(work)).find(row => row.name === 'zeta:run') as any).getPromptForCommand('', context)
  await refreshActivePlugins(update => { app = update(app) })
  const afterReload = await (app.plugins.commands.find(row => row.name === 'zeta:run') as any).getPromptForCommand('', context)
  if (caps.blocked.length) throw new Error('Actual activation escaped fixture ' + JSON.stringify(caps.blocked))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ first, beforeReload, globalBeforeReload, afterReload, updated, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), error => error ? reject(error) : resolve()))
  process.exit(0)
}
const caller = await processSlashCommand('/reload-plugins kept args', [], [], [], {
  getAppState: () => app, setAppState: (update: any) => { app = update(app) }, abortController: new AbortController(), messages: [],
  options: { commands: [command], tools: [], agentDefinitions: { activeAgents: [] } }
} as any, () => {})
if (caps.blocked.length) throw new Error('Actual reload escaped private fixture ' + JSON.stringify(caps.blocked))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ caller, caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), error => error ? reject(error) : resolve()))
process.exit(0)
