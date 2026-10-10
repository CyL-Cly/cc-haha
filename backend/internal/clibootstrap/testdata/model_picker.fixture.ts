import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MODEL_PICKER_FIXTURE !== '1'
  || Object.keys(input).some(key => !['home', 'cases', 'prices'].includes(key))) throw new Error('Model picker requires original private inputs and Bun 1.3.14')
const home = assertDisposableFixtureHome(input.home, 'native model picker')
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
const cache = await import('../../../../src/utils/settings/settingsCache')
const model = await import('../../../../src/utils/model/model')
const { getModelOptions } = await import('../../../../src/utils/model/modelOptions')
const effort = await import('../../../../src/utils/effort')
const cost = await import('../../../../src/utils/modelCost')
const support = await import('../../../../src/utils/model/modelSupportOverrides')
const rows = []
const allowed = new Set(['ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_FABLE_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'ANTHROPIC_CUSTOM_MODEL_OPTION', 'ANTHROPIC_CUSTOM_MODEL_OPTION_NAME', 'ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION', 'CLAUDE_CODE_ALWAYS_ENABLE_EFFORT', 'CLAUDE_CODE_DISABLE_1M_CONTEXT', ...['FABLE', 'SONNET', 'OPUS', 'HAIKU'].flatMap(tier => ['NAME', 'DESCRIPTION', 'SUPPORTED_CAPABILITIES'].map(suffix => `ANTHROPIC_DEFAULT_${tier}_MODEL_${suffix}`))])
for (const record of input.cases) {
  for (const key of allowed) delete process.env[key]
  for (const [key, value] of Object.entries(record.env ?? {})) {
    if (!allowed.has(key) || typeof value !== 'string') throw new Error(`Unrelated model picker input ${key}`)
    process.env[key] = value
  }
  fs.mkdirSync(join(home, '.claude'), { recursive: true })
  fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(record.settings ?? {}))
  cache.resetSettingsCache()
  state.resetModelStringsForTestingOnly()
  // Each row is an independent original process profile; reset Source memo.
  support.get3PModelCapabilityOverride.cache.clear()
  state.setMainLoopModelOverride(record.override)
  state.setInitialMainLoopModel(record.initial ?? null)
  config.getGlobalConfig().additionalModelOptionsCache = record.additional ?? []
  const options = getModelOptions(false)
  rows.push({ name: record.name, options: options.map(option => {
    const resolved = option.value === null ? model.getDefaultMainLoopModel() : model.parseUserSpecifiedModel(option.value)
    return { value: option.value, label: option.label, description: option.description, supportsEffort: effort.modelSupportsEffort(resolved), supportsMax: effort.modelSupportsMaxEffort(resolved), defaultEffort: effort.convertEffortValueToLevel(effort.getDefaultEffortForModel(resolved) ?? 'high') }
  }) })
}
process.stdout.write(JSON.stringify({ rows, prices: input.prices.map((value: string) => cost.getModelPricingString(value) ?? null), capabilities }))
