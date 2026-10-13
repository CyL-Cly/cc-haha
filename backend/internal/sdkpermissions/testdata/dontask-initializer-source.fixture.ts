import { mkdirSync, writeFileSync, symlinkSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Permission startup')
if (process.env.CC_HAHA_PERMISSION_STARTUP_FIXTURE !== '1') throw new Error('Missing permission startup fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {
  absent.push(join(directory, '.git'))
  if (directory === dirname(directory)) break
}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
const config = await import('../../../../src/utils/config')
const { resetSettingsCache } = await import('../../../../src/utils/settings/settingsCache')
const { resetGrowthBook } = await import('../../../../src/services/analytics/growthbook')
const { initialPermissionModeFromCLI, initializeToolPermissionContext } = await import('../../../../src/utils/permissions/permissionSetup')
const { _resetForTesting: resetAutoModeState } = await import('../../../../src/utils/permissions/autoModeState')
const cwd = join(input.homeDir, 'project')
mkdirSync(cwd, { recursive: true })
for (const path of input.directories ?? []) mkdirSync(join(input.homeDir, path), { recursive: true })
for (const path of input.files ?? []) writeFileSync(join(input.homeDir, path), 'actual fixture file')
symlinkSync(cwd, join(input.homeDir, 'project-alias'))
state.setCwdState(cwd)
state.setOriginalCwd(cwd)
state.setProjectRoot(cwd)
state.setIsInteractive(false)
config.enableConfigs()
const rows = []
for (const row of input.rows) {
  for (const key of ['PWD', 'CLAUDE_CODE_REMOTE', 'USER_TYPE', 'CLAUDE_INTERNAL_FC_OVERRIDES']) delete process.env[key]
  Object.assign(process.env, row.env ?? {})
  writeFileSync(join(input.homeDir, '.claude/settings.json'), JSON.stringify(row.settings ?? {}))
  config.saveGlobalConfig(old => ({ ...old, growthBookOverrides: row.configOverrides ?? {}, cachedStatsigGates: { tengu_disable_bypass_permissions_mode: true }, cachedGrowthBookFeatures: { tengu_disable_bypass_permissions_mode: true } }))
  resetSettingsCache()
  resetGrowthBook()
  resetAutoModeState()
  state.setAdditionalDirectoriesForClaudeMd(row.addDirs ?? [])
  const mode = initialPermissionModeFromCLI({ permissionModeCli: row.mode, dangerouslySkipPermissions: row.skip })
  // Registry filtering is independently covered by existing Source fixtures.
  // Omit the optional base-list input here so these mode/workspace observations
  // do not invoke account/model discovery through getToolsForDefaultPreset.
  const result = await initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], permissionMode: mode.mode, allowDangerouslySkipPermissions: row.allow ?? false, addDirs: row.addDirs ?? [] })
  const context = result.toolPermissionContext
  rows.push({ mode, directories: Array.from(context.additionalWorkingDirectories.entries()), instructionDirectories: state.getAdditionalDirectoriesForClaudeMd(), warnings: result.warnings, bypassAvailable: context.isBypassPermissionsModeAvailable, autoAvailable: context.isAutoModeAvailable })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
