import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Plugin options Source comparison')
if (process.env.CC_HAHA_PLUGIN_OPTIONS_FIXTURE !== '1') throw new Error('Missing plugin options fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home, '.claude')
mkdirSync(config, { recursive: true })
mkdirSync(join(home, 'managed'), { recursive: true })
writeFileSync(join(config, 'settings.json'), input.settings)
writeFileSync(join(config, '.credentials.json'), input.credentials)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned policy path')
process.env.USER_TYPE = 'external'
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const execa = await import('execa')
// Source takes its real Keychain failure→plaintext fallback path. No actual
// Keychain command is permitted; all settings/option business remains original.
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('isolated unavailable Keychain') } }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setCwdState(home)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const options = await import('../../../src/utils/plugins/pluginOptionsStorage')
const observations = []
const failures: string[] = []
for (const step of input.operations) {
  if (step.action === 'load') observations.push(options.loadPluginOptions(step.id))
  else if (step.action === 'save') {
    if (step.captureError) {
      try { options.savePluginOptions(step.id, step.values, step.schema) }
      catch (error) { failures.push((error as Error).message) }
    } else options.savePluginOptions(step.id, step.values, step.schema)
  }
  else if (step.action === 'delete') options.deletePluginOptions(step.id)
}
process.stdout.write(JSON.stringify({ observations, settings: readFileSync(join(config, 'settings.json'), 'utf8'), credentials: readFileSync(join(config, '.credentials.json'), 'utf8'), ...(failures.length ? { failures } : {}) }))
