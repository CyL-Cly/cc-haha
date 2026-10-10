import { join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native session usage')
if (process.env.CC_HAHA_SESSION_USAGE_FIXTURE !== '1') throw new Error('Unowned session usage fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings))
await import('../../../preload')
process.chdir(home)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(false)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'policy')
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
for (const [key, value] of Object.entries(input.environment)) process.env[key] = String(value)
const cost = await import('../../../src/cost-tracker')
const pricing = await import('../../../src/utils/modelCost')
let clock = 1000
Date.now = () => clock
state.resetCostState()
const observations = [cost.getSessionUsageSnapshot()]
for (const row of input.records) {
  const amount = pricing.calculateUSDCost(row.model, row.usage)
  cost.addToTotalSessionCost(amount, row.usage, row.model)
  state.addToTotalLinesChanged(row.added, row.removed)
  state.addToTotalGenerationDuration(row.decode, row.ttft)
  state.addToTotalDurationState(row.api, row.apiWithoutRetries)
  clock += row.elapsed
  observations.push(cost.getSessionUsageSnapshot())
}
if (capabilities.blocked.length) throw new Error('Session usage accessed blocked capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ observations, capabilities }))
