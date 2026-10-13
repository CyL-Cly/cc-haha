import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native Auto gate')
if (process.env.CC_HAHA_AUTO_GATE_FIXTURE !== '1') throw new Error('Missing owned Auto gate fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(false)
const config = await import('../../../../src/utils/config')
const settingsCache = await import('../../../../src/utils/settings/settingsCache')
const growthbook = await import('../../../../src/services/analytics/growthbook')
const auto = await import('../../../../src/utils/permissions/autoModeState')
const setup = await import('../../../../src/utils/permissions/permissionSetup')
const { getNextPermissionMode } = await import('../../../../src/utils/permissions/getNextPermissionMode')
mkdirSync(join(home, '.claude'), { recursive: true })
config.enableConfigs()
const encode = (value: unknown) => JSON.parse(JSON.stringify(value, (_key, item) => item instanceof Map ? {map: [...item]} : item))
const rows = []
for (const row of input.rows) {
  process.env.USER_TYPE = row.userType
  process.env.CLAUDE_INTERNAL_FC_OVERRIDES = JSON.stringify(row.overrides ?? {})
  writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(row.settings ?? {}))
  settingsCache.resetSettingsCache()
  growthbook.resetGrowthBook()
  auto._resetForTesting()
  auto.setAutoModeFlagCli(row.cliFlag)
  const initial = await setup.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], permissionMode: row.mode === 'plan' ? 'auto' : row.mode, allowDangerouslySkipPermissions: false, addDirs: [] })
  const before = encode(initial.toolPermissionContext)
  let context = initial.toolPermissionContext
  if (context.mode === 'auto') {
    auto.setAutoModeActive(true)
    if (initial.dangerousPermissions.length) context = setup.stripDangerousPermissionsForAutoMode(context)
  }
  if (row.mode === 'plan') context = {...setup.transitionPermissionMode('auto', 'plan', context), mode:'plan'}
  const stripped = encode(context)
  const gate = await setup.verifyAutoModeGateAccess(context, row.fast)
  context = gate.updateContext(context)
  rows.push({before, stripped, verified:encode(context), active:auto.isAutoModeActive(), notification:gate.notification ?? '', next:getNextPermissionMode(context)})
}
if (capabilities.blocked.length) throw new Error('Blocked original Auto gate: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
