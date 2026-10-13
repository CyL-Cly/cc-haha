import fs from 'node:fs'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const home = assertDisposableFixtureHome(process.env.HOME, 'Original CCR metadata restore')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned metadata restore fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
Object.assign(process.env, input.env)
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
fs.mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const config = await import('../../src/utils/config')
config.enableConfigs()
const { externalMetadataToAppState } = await import('../../src/state/onChangeAppState')
const model = await import('../../src/utils/model/model')
const results = []
for (const metadata of input.metadata) {
  state.setMainLoopModelOverride('prior-fixture-model')
  const prior: any = { toolPermissionContext: { mode: 'plan', prePlanMode: 'default', rulesFuture: { untouched: true } }, isUltraplanMode: false, unrelated: true }
  const restored = externalMetadataToAppState(metadata)(prior)
  if (typeof metadata.model === 'string') state.setMainLoopModelOverride(metadata.model)
  const resolvedModel = model.getMainLoopModel()
  results.push({ mode: restored.toolPermissionContext.mode, prePlanMode: restored.toolPermissionContext.prePlanMode, ultraplan: restored.isUltraplanMode, model: resolvedModel, rawModel: state.getMainLoopModelOverride(), nextModel: model.getRuntimeMainLoopModel({ permissionMode: restored.toolPermissionContext.mode, mainLoopModel: resolvedModel }) })
}
if (capabilities.blocked.length) throw new Error('Original metadata restore capability escape: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, capabilities }))
