import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original Discover bulk selection and real installation')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_BULK_SOURCE !== '1') throw new Error('Missing bulk install Source guard')
const sdk = process.env.CC_HAHA_MCPB_SOURCE_SDK
if (!sdk || JSON.parse(fs.readFileSync(path.join(sdk, 'package.json'), 'utf8')).version !== '1.2.0' || JSON.parse(fs.readFileSync(path.join(sdk, 'node_modules/zod/package.json'), 'utf8')).version !== '3.25.67') throw new Error('Verified compatible published MCPB seed required')
const schemas = await import(pathToFileURL(path.join(sdk, 'dist/schemas/latest.js')).href)
const compiler = await import(pathToFileURL(path.join(sdk, 'dist/shared/config.js')).href)
mock.module('@anthropic-ai/mcpb', () => ({ ...schemas, ...compiler }))
for (const [target, encoded] of Object.entries(input.files)) {
  if (!path.resolve(target).startsWith(home + path.sep)) throw new Error('Unowned Source install path')
  fs.mkdirSync(path.dirname(target), { recursive: true })
  fs.writeFileSync(target, Buffer.from(encoded as string, 'base64'))
}
fs.mkdirSync(path.join(home, 'managed'), { recursive: true })
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = path.join(home, 'managed')
const managed = await import('../../../src/utils/settings/managedPath')
if (managed.getManagedFilePath() !== path.join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.workDir)
state.setProjectRoot(input.workDir)
state.setCwdState(input.workDir)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const config = await import('../../../src/utils/config')
config.enableConfigs()
const installations = await import('../../../src/utils/plugins/pluginInstallationHelpers')
const caches = await import('../../../src/utils/plugins/cacheUtils')
const format = await import('../../../src/utils/plugins/marketplaceHelpers')
const strings = await import('../../../src/utils/stringUtils')
const installedPlugins = await import('../../../src/utils/plugins/installedPluginsManager')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const relative = '../../../src/commands/plugin/DiscoverPlugins.tsx'
const source = ts.createSourceFile(relative, fs.readFileSync(new URL(relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let callback: any
let order: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node) && node.name?.text === 'installSelectedPlugins') callback = node.initializer
  if (ts.isCallExpression(node) && node.expression?.getText(source) === 'uninstalledPlugins.sort' && node.arguments[0]?.getText(source).includes('countA')) order = node.arguments[0]
  ts.forEachChild(node, visit)
}
visit(source)
if (!callback || !order) throw new Error('Original Discover bulk/order caller changed')
const orderCode = ts.transpileModule('const compare = ' + order.getText(source), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const counts = new Map(input.counts.map((row: any) => [row.plugin, row.unique_installs]))
const compare = new Function('counts', orderCode + '; return compare')(counts)
const available = input.available.filter((row: any) => !installedPlugins.isPluginGloballyInstalled(row.pluginId)).sort(compare)
const code = ts.transpileModule('const install = ' + callback.getText(source), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const events: any[] = []
let result: string | null = null
let error: string | null = null
const deps: any = {
  selectedForInstall: new Set(input.selected), availablePlugins: available,
  installPluginFromMarketplace: async (args: any) => { const value = await installations.installPluginFromMarketplace(args); events.push({ op: 'install', id: args.pluginId, scope: args.scope, success: value.success }); return value },
  setInstallingPlugins: (value: Set<string>) => events.push({ op: 'installing', ids: [...value] }),
  setSelectedForInstall: (value: Set<string>) => events.push({ op: 'selected', ids: [...value] }),
  clearAllCaches: () => { caches.clearAllCaches(); events.push({ op: 'clear' }) },
  plural: strings.plural, formatFailureDetails: format.formatFailureDetails,
  setResult: (value: string) => result = value, setError: (value: string) => error = value,
  onInstallComplete: async () => events.push({ op: 'complete' }),
  setParentViewState: (value: any) => events.push({ op: 'parent', value })
}
const install = new Function(...Object.keys(deps), code + '; return install')(...Object.values(deps))
await install()
const settings = JSON.parse(fs.readFileSync(path.join(process.env.CLAUDE_CONFIG_DIR!, 'settings.json'), 'utf8'))
const installedPath = path.join(process.env.CLAUDE_CONFIG_DIR!, 'plugins/installed_plugins.json')
const installed = fs.existsSync(installedPath) ? JSON.parse(fs.readFileSync(installedPath, 'utf8')) : null
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ available: available.map((row: any) => row.pluginId), events, result, error, settings, installed }), failure => failure ? reject(failure) : resolve()))
