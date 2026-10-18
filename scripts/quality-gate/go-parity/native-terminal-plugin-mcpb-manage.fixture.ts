import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original MCPB Manage Configure caller and physical bundle loader')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_MCPB_SOURCE !== '1') throw new Error('Missing MCPB Manage Source guard')
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
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setCwdState(home)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const config = await import('../../../src/utils/config')
config.enableConfigs()
const handler = await import('../../../src/utils/plugins/mcpbHandler')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const relative = '../../../src/commands/plugin/ManagePlugins.tsx'
const source = ts.createSourceFile(relative, fs.readFileSync(new URL(relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let configure: any
let save: any
let cancel: any
let detect: any
function visit(node: any) {
  if (ts.isObjectLiteralExpression(node)) {
    const label = node.properties.find((property: any) => property.name?.getText(source) === 'label' && property.initializer?.text === 'Configure')
    if (label) configure = node.properties.find((property: any) => property.name?.getText(source) === 'action')?.initializer
  }
  if (ts.isFunctionDeclaration(node) && node.name?.text === 'handleSave' && node.getText(source).includes('loadMcpbFile')) save = node
  if (ts.isFunctionDeclaration(node) && node.name?.text === 'handleCancel' && node.getText(source).includes('setConfigNeeded')) cancel = node
  if (ts.isFunctionDeclaration(node) && node.name?.text === 'detectMcpb') detect = node
  ts.forEachChild(node, visit)
}
visit(source)
if (!configure || !save || !cancel || !detect) throw new Error('Original Manage MCPB caller changed')
const code = ts.transpileModule('const configure = ' + configure.getText(source) + '\n' + save.getText(source) + '\n' + cancel.getText(source) + '\n' + detect.getText(source), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
let view = 'plugin-details'
let needed: any = null
let error: string | null = null
let result: string | null = null
const calls: any[] = []
let hasMcpb = false
const deps: any = {
  selectedPlugin: { plugin: input.plugin, marketplace: 'mkt' }, pluginId_12: 'sample@mkt',
  isMcpbSource: handler.isMcpbSource, errorMessage: (e: Error) => e.message,
  setIsLoadingConfig: () => {}, setViewState: (value: string) => { view = value }, setConfigNeeded: (value: any) => { needed = value },
  setProcessError: (value: string) => { error = value }, setResult: (value: string) => { result = value },
  fs: fs.promises, path, jsonParse: JSON.parse, logForDebugging: () => {}, setSelectedPluginHasMcpb: (value: boolean) => { hasMcpb = value },
  loadMcpbFile: async (...args: any[]) => { calls.push({ source: args[0], id: args[2], provided: args[4] ?? null, force: args[5] ?? false }); return handler.loadMcpbFile(...args) }
}
const original = new Function(...Object.keys(deps), 'getNeeded', code.replaceAll('configNeeded', 'getNeeded()') + '; return { configure, handleSave, handleCancel, detectMcpb }')(...Object.values(deps), () => needed)
const observations = []
for (const step of input.steps) {
  if (step.action === 'open') await original.configure()
  else if (step.action === 'save') await original.handleSave(step.values)
  else if (step.action === 'cancel') original.handleCancel()
  else if (step.action === 'detect') await original.detectMcpb()
  else throw new Error('Undeclared Manage transition')
  observations.push({ view, error, result, hasMcpb, needed: needed ? { title: needed.manifest.name, schema: needed.configSchema, values: needed.existingConfig } : null, calls: [...calls], pluginConfigs: JSON.parse(fs.readFileSync(path.join(home, '.claude/settings.json'), 'utf8')).pluginConfigs ?? {}, pluginSecrets: fs.existsSync(path.join(home, '.claude/.credentials.json')) ? JSON.parse(fs.readFileSync(path.join(home, '.claude/.credentials.json'), 'utf8')).pluginSecrets ?? {} : {} })
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(observations), error => error ? reject(error) : resolve()))
