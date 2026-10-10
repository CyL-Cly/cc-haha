import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MODEL_PICKER_APPLY_FIXTURE !== '1') throw new Error('private original model picker consumer only')
const home = assertDisposableFixtureHome(input.home, 'native model picker selection')
// Resolve the pinned source before the capability boundary; reads still pass through it.
const modelPickerSourcePath = fs.realpathSync(fileURLToPath(new URL('../../../../src/components/ModelPicker.tsx', import.meta.url)))
const modelCommandSourcePath = fs.realpathSync(fileURLToPath(new URL('../../../../src/commands/model/model.tsx', import.meta.url)))
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', BUILD_TIME: '' } })
const state = await import('../../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
const config = await import('../../../../src/utils/config')
config.enableConfigs()
const settings = await import('../../../../src/utils/settings/settings')
const cache = await import('../../../../src/utils/settings/settingsCache')
const model = await import('../../../../src/utils/model/model')
const effortModule = await import('../../../../src/utils/effort')
const support = await import('../../../../src/utils/model/modelSupportOverrides')
const { configureEffortParams } = await import('../../../../src/services/api/claude')
const source = fs.readFileSync(modelPickerSourcePath, 'utf8')
const start = source.indexOf('    t14 = function handleSelect(')
const end = source.indexOf('\n    $[35]', start)
if (start < 0 || end < 0) throw new Error('original ModelPicker select consumer changed')
const callback = new Bun.Transpiler({ loader: 'tsx' }).transformSync('let t14;\n' + source.slice(start, end) + '\nreturn t14')
const helperStart = source.indexOf('function resolveOptionModel(')
const helperEnd = source.indexOf('\nfunction EffortLevelIndicator(', helperStart)
const defaultStart = source.indexOf('function getDefaultEffortLevelForOption(')
const defaultEnd = source.indexOf('\n//# sourceMappingURL', defaultStart)
if ([helperStart, helperEnd, defaultStart, defaultEnd].some(i => i < 0)) throw new Error('original picker model/default boundary changed')
const helpers = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(helperStart, helperEnd) + '\n' + source.slice(defaultStart, defaultEnd))
const originalHelpers = new Function('getDefaultMainLoopModel', 'parseUserSpecifiedModel', 'getDefaultEffortForModel', 'convertEffortValueToLevel', 'const NO_PREFERENCE="__NO_PREFERENCE__";\n' + helpers + '\nreturn {resolveOptionModel,getDefaultEffortLevelForOption}')(
  model.getDefaultMainLoopModel, model.parseUserSpecifiedModel, effortModule.getDefaultEffortForModel, effortModule.convertEffortValueToLevel,
)
const values = []
for (const record of input.rows) {
  fs.mkdirSync(join(home, '.claude'), { recursive: true })
  fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(record.settings))
  cache.resetSettingsCache()
  state.resetModelStringsForTestingOnly()
  state.setMainLoopModelOverride(record.initial)
  support.get3PModelCapabilityOverride.cache.clear()
  let appState = { effortValue: effortModule.getInitialEffortSetting() }
  let selected
  const handler = new Function('logEvent', 'effort', 'hasToggledEffort', 'onSelect', 'setAppState', 'skipSettingsWrite', 'resolvePickerEffortPersistence', 'getDefaultEffortLevelForOption', 'getSettingsForSource', 'toPersistableEffort', 'updateSettingsForSource', 'resolveOptionModel', 'modelSupportsEffort', 'NO_PREFERENCE', callback)(
    () => {}, record.effort, record.toggled ?? false, (...args) => { selected = args }, fn => { appState = fn(appState) }, record.skip ?? false,
    effortModule.resolvePickerEffortPersistence, originalHelpers.getDefaultEffortLevelForOption, settings.getSettingsForSource,
    effortModule.toPersistableEffort, settings.updateSettingsForSource, originalHelpers.resolveOptionModel, effortModule.modelSupportsEffort, '__NO_PREFERENCE__',
  )
  handler(record.model ?? '__NO_PREFERENCE__')
  const resolvedModel = originalHelpers.resolveOptionModel(record.model ?? '__NO_PREFERENCE__')
  const outputConfig = {}
  configureEffortParams(effortModule.resolveAppliedEffort(resolvedModel, appState.effortValue), outputConfig, {}, [], resolvedModel)
  values.push({ settings: JSON.parse(fs.readFileSync(join(home, '.claude/settings.json'), 'utf8')), appState, selected, apiEffort: outputConfig.effort })
}
const wrapper = fs.readFileSync(modelCommandSourcePath, 'utf8')
const labelStart = wrapper.indexOf('function renderModelLabel(')
const labelEnd = wrapper.indexOf('\n//# sourceMappingURL', labelStart)
if (labelStart < 0 || labelEnd < 0) throw new Error('original model command display boundary changed')
const labelSource = new Bun.Transpiler({loader:'ts'}).transformSync(wrapper.slice(labelStart,labelEnd))
const renderLabel = new Function('renderDefaultModelSetting','getDefaultMainLoopModelSetting',labelSource+';return renderModelLabel')(model.renderDefaultModelSetting,model.getDefaultMainLoopModelSetting)
process.stdout.write(JSON.stringify({ values, labels:(input.labels??[]).map(renderLabel), capabilities }))
