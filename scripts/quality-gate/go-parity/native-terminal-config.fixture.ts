import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original Config callbacks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_CONFIG_SOURCE !== '1') throw new Error('Missing original Config guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const original = ts.createSourceFile('Config.tsx', fs.readFileSync(new URL('../../../src/components/Settings/Config.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let initializer: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node) && node.name.getText(original) === 'settingsItems') initializer = node.initializer
  ts.forEachChild(node, visit)
}
visit(original)
if (!initializer || !ts.isArrayLiteralExpression(initializer)) throw new Error('Original settings table identity changed')
const expression = ts.transpileModule('const settingsItems = '+initializer.getText(original),{compilerOptions:{target:ts.ScriptTarget.ESNext,jsx:ts.JsxEmit.React,module:ts.ModuleKind.ESNext}}).outputText
const instructionSource = ts.createSourceFile('instructionFiles.ts', fs.readFileSync(new URL('../../../src/utils/instructionFiles.ts', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const patchFunction = instructionSource.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'instructionFilesSettingsPatch')
if (!patchFunction) throw new Error('Original instruction patch identity changed')
const patchBody = new Bun.Transpiler({ loader: 'ts' }).transformSync(patchFunction.getText(instructionSource)).replace('export ', '')
const instructionFilesSettingsPatch = new Function('INSTRUCTION_FILES_PLUGIN', patchBody + '\nreturn instructionFilesSettingsPatch')('agents-md@builtin')
const authPortable=ts.createSourceFile('authPortable.ts',fs.readFileSync(new URL('../../../src/utils/authPortable.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
const normalizeFn=authPortable.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='normalizeApiKeyForConfig')
if(!normalizeFn)throw new Error('Missing actual normalized key producer')
const normalizedJS=ts.transpileModule(normalizeFn.getText(authPortable).replace('export ',''),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const normalizeApiKeyForConfig=new Function(normalizedJS+'\nreturn normalizeApiKeyForConfig')()
function merge(target: any, patch: any) {
  for (const key of Object.keys(patch)) {
    if (patch[key] === undefined) delete target[key]
    else if (patch[key] && typeof patch[key] === 'object' && !Array.isArray(patch[key])) merge(target[key] ??= {}, patch[key])
    else target[key] = structuredClone(patch[key])
  }
}
const results = []
for (const row of input.rows) {
  let global = structuredClone(row.global)
  const stores = { userSettings: structuredClone(row.user), localSettings: structuredClone(row.local) }
  let settingsData = { ...stores.userSettings, ...stores.localSettings }
  let app = { thinkingEnabled: true, verbose: global.verbose, settings: settingsData }
  const no = () => false
  const noop = () => {}
  const deps: any = {
    process:{env:row.env??{}},normalizeApiKeyForConfig,isRunningOnHomespace:()=>false,React:{createElement:()=>null},Text:()=>null,
    globalConfig: global, settingsData, thinkingEnabled: true, isFastMode: false, promptSuggestionEnabled: false, verbose: global.verbose,
    themeSetting: global.theme, currentOutputStyle: 'default', currentLanguage: undefined, mainLoopModel: null,
    isFileCheckpointingAvailable: true, showAutoInDefaultModePicker: false, showDefaultViewPicker: false,
    isConnectedToIde: false, shouldShowExternalIncludesToggle: false, autoUpdaterDisabledReason: '',
    isFastModeEnabled: no, isFastModeAvailable: no, getFeatureValue_CACHED_MAY_BE_STALE: no, feature: no,
    getInstructionFilesMode: () => 'claude-md-or-agents-md', INSTRUCTION_FILE_MODES: ['claude-md', 'claude-md-or-agents-md', 'claude-md-and-agents-md', 'managed-only'], instructionFilesSettingsPatch,
    getWorkflowSizeGuidelineFromSettings: () => undefined, WORKFLOW_SIZE_GUIDELINES: ['unrestricted', 'small', 'medium', 'large'],
    isSupportedTerminal: () => row.supported === true, isFullscreenEnvEnabled: no, isAgentSwarmsEnabled: no,
    isBridgeEnabled: no, setTheme: noop, onChangeMainModelConfig: noop,
    permissionModeFromString: (value: string) => value, isExternalPermissionMode: () => true, toExternalPermissionMode: (value: string) => value,
    getGlobalConfig: () => global, saveGlobalConfig: (update: any) => { global = update(global) }, setGlobalConfig: (value: any) => { global = value },
    getInitialSettings: () => ({ ...stores.userSettings, ...stores.localSettings }),
    updateSettingsForSource: (source: keyof typeof stores, patch: any) => { merge(stores[source] ??= {}, patch); return {} },
    setSettingsData: (update: any) => { settingsData = typeof update === 'function' ? update(settingsData) : update },
    setAppState: (update: any) => { app = update(app) }, setChanges: (update: any) => update({}),
    onChangeVerbose: (value: boolean) => { global = { ...global, verbose: value }; app.verbose = value },
    logEvent: noop, logError: noop, clearMemoryFileCaches: noop, getUserContext: { cache: { clear: noop } },
    EXTERNAL_PERMISSION_MODES: ['default', 'acceptEdits', 'plan', 'dontAsk', 'bypassPermissions'], PERMISSION_MODES: ['default', 'plan'],
  }
  const items = new Function(...Object.keys(deps), expression + '\nreturn settingsItems')(...Object.values(deps))
  const item = items.find((item: any) => item.id === row.id)
  if (!item) throw new Error('Original setting absent: ' + row.id)
  item.onChange(row.value)
  results.push({ id: row.id, global, user: stores.userSettings, local: stores.localSettings, thinkingEnabled: app.thinkingEnabled })
}
process.stdout.write(JSON.stringify({ results }))
