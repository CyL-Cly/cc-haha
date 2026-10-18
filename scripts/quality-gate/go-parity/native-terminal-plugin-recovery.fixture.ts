import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original failed-plugin recovery caller and actual uninstall operation')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_RECOVERY_SOURCE !== '1') throw new Error('Missing plugin recovery Source guard')
const sdk = process.env.CC_HAHA_MCPB_SOURCE_SDK
if (!sdk || JSON.parse(fs.readFileSync(path.join(sdk, 'package.json'), 'utf8')).version !== '1.2.0' || JSON.parse(fs.readFileSync(path.join(sdk, 'node_modules/zod/package.json'), 'utf8')).version !== '3.25.67') throw new Error('Missing verified compatible published MCPB 1.2.0/Zod 3.25.67 reference')
const schemas = await import(pathToFileURL(path.join(sdk, 'dist/schemas/latest.js')).href)
const compiler = await import(pathToFileURL(path.join(sdk, 'dist/shared/config.js')).href)
mock.module('@anthropic-ai/mcpb', () => ({ ...schemas, ...compiler }))
for (const [target, encoded] of Object.entries(input.files)) {
  if (!path.resolve(target).startsWith(home + path.sep)) throw new Error('Unowned Source bundle/config path')
  fs.mkdirSync(path.dirname(target), { recursive: true })
  fs.writeFileSync(target, Buffer.from(encoded as string, 'base64'))
}
fs.mkdirSync(path.join(home, 'managed'), { recursive: true })
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = path.join(home, 'managed')
const managed = await import('../../../src/utils/settings/managedPath')
if (managed.getManagedFilePath() !== path.join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const execa = await import('execa')
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('Private unavailable Keychain') } }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.workDir)
state.setProjectRoot(input.workDir)
state.setCwdState(input.workDir)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const config = await import('../../../src/utils/config')
config.enableConfigs()
const operations = await import('../../../src/services/plugins/pluginOperations')
const settings = await import('../../../src/utils/settings/settings')
const caches = await import('../../../src/utils/plugins/cacheUtils')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const relative = '../../../src/commands/plugin/ManagePlugins.tsx'
const source = ts.createSourceFile(relative, fs.readFileSync(new URL(relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let callback: any
function visit(node: any) {
  if (ts.isPropertyAssignment(node) && node.name?.text === 'select:accept' && node.initializer?.getText(source).includes('Plugin was never installed')) callback = node.initializer
  ts.forEachChild(node, visit)
}
visit(source)
if (!callback) throw new Error('Original failed-plugin accept caller changed')
const code = ts.transpileModule('const accept = ' + callback.getText(source), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
let view: any = { type: 'failed-plugin-details', plugin: input.plugin }
let error: string | null = null
const events: any[] = []
const deps: any = {
  viewState: view, isInstallableScope: operations.isInstallableScope,
  uninstallPluginOp: async (...args: any[]) => { events.push({ op: 'uninstall', args }); const result = await operations.uninstallPluginOp(...args); events.push({ op: 'uninstall-result', result }); return result },
  getSettingsForSource: settings.getSettingsForSource, updateSettingsForSource: settings.updateSettingsForSource,
  clearAllCaches: caches.clearAllCaches, onManageComplete: async () => events.push({ op: 'manage-complete' }),
  setIsProcessing: (value: boolean) => events.push({ op: 'processing', value }), setProcessError: (value: string) => { error = value }, setViewState: (value: any) => { view = value }
}
const accept = new Function(...Object.keys(deps), code + '; return accept')(...Object.values(deps))
accept()
const deadline = Date.now() + 10000
while (view !== 'plugin-list' && !error) {
  if (Date.now() > deadline) throw new Error('Original recovery caller did not settle')
  await new Promise(resolve => setTimeout(resolve, 10))
}
const files: any = {}
for (const target of input.observe) files[target.slice(home.length)] = JSON.parse(fs.readFileSync(target, 'utf8'))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ view, error, events, files }), failure => failure ? reject(failure) : resolve()))
