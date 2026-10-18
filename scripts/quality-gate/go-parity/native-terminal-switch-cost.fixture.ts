import { join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual terminal resume costs')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SWITCH_COST_SOURCE !== '1') throw new Error('Unowned terminal cost fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude/settings.json'), '{}')
await import('../../../preload')
process.chdir(home)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(true)
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
state.setLastMainRequestId('keep-original-request')
const output: any[] = []
let target: any
for (const op of input.operations) {
  if (op.kind === 'charge') {
    cost.addToTotalSessionCost(pricing.calculateUSDCost(op.model, op.usage), op.usage, op.model)
    state.addToTotalLinesChanged(op.added, op.removed)
    state.addToTotalGenerationDuration(op.decode, op.ttft)
    state.addToTotalDurationState(op.api, op.apiWithoutRetries)
    clock += op.elapsed
  } else if (op.kind === 'save') {
    cost.saveCurrentSessionCosts()
    const project = config.getCurrentProjectConfig()
    const fields = Object.fromEntries(Object.entries(project).filter(([key]) => key.startsWith('last')))
    // SID comes from the actual original singleton, independent of Go UUIDs.
    fields.lastSessionId = 'source-current-session'
    output.push({ kind: 'saved', fields })
  } else if (op.kind === 'reset') {
    state.resetCostState()
    output.push({ kind: 'reset', usage: cost.getSessionUsageSnapshot(), lastRequestId: state.getLastMainRequestId() })
  } else if (op.kind === 'capture') {
    config.saveCurrentProjectConfig(() => op.project)
    target = cost.getStoredSessionCosts(op.sessionId)
    output.push({ kind: 'captured', present: target !== undefined })
  } else if (op.kind === 'replaceProject') {
    config.saveCurrentProjectConfig(() => op.project)
  } else if (op.kind === 'restore') {
    if (target) state.setCostStateForRestore(target)
    output.push({ kind: 'restored', usage: cost.getSessionUsageSnapshot() })
  }
}
if (capabilities.blocked.length) throw new Error('Terminal cost fixture blocked ' + capabilities.blocked.join(','))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ output, capabilities }), error => error ? reject(error) : resolve()))
