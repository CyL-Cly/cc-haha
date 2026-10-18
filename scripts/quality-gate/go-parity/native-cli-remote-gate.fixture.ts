import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual cached remote backend gate')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REMOTE_GATE_SOURCE !== '1') throw new Error('Owned pinned remote gate required')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(current => ({ ...current, ...input.global }))
const growthbook = await import('../../../src/services/analytics/growthbook')
const values = []
for (const step of input.steps) {
  if (step.env !== undefined) process.env.CLAUDE_INTERNAL_FC_OVERRIDES = step.env
  if (step.override !== undefined) growthbook.setGrowthBookConfigOverride('tengu_remote_backend', step.override)
  values.push(Boolean(growthbook.getFeatureValue_CACHED_MAY_BE_STALE('tengu_remote_backend', false)))
}
if (caps.blocked.length) throw new Error('Remote gate escaped private capabilities ' + JSON.stringify(caps.blocked))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ values, blocked: caps.blocked }) + '\n', error => error ? reject(error) : resolve()))
process.exit(0)
