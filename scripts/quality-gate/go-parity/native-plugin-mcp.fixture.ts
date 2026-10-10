import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native plugin MCP Source comparison')
if (process.env.CC_HAHA_PLUGIN_MCP_FIXTURE !== '1') throw new Error('Missing plugin MCP fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home, '.claude')
mkdirSync(config, { recursive: true })
mkdirSync(join(home, 'managed'), { recursive: true })
writeFileSync(join(config, 'settings.json'), input.settings)
writeFileSync(join(config, '.credentials.json'), input.credentials)
for (const [path, content] of Object.entries(input.files ?? {})) writeFileSync(path, content as string)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned policy path')
process.env.USER_TYPE = 'external'
for (const [key, value] of Object.entries(input.environment ?? {})) process.env[key] = value as string
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const execa = await import('execa')
// Native Keychain IO alone is isolated; original settings, merged options,
// channel secrets, producer and file/schema business execute unchanged.
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('isolated unavailable Keychain') } }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setCwdState(home)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const api = await import('../../../src/utils/plugins/mcpPluginIntegration')
const user = await import('../../../src/utils/plugins/mcpbHandler')
const options = await import('../../../src/utils/plugins/pluginOptionsStorage')
const directories = await import('../../../src/utils/plugins/pluginDirectories')
const plugins = input.plugins
const observations = []
const errors: any[] = []
for (const step of input.operations) {
  let value: unknown
  const plugin = plugins[step.plugin ?? 0]
  if (step.action === 'load') value = await api.loadPluginMcpServers(plugin, errors)
  else if (step.action === 'get') value = await api.getPluginMcpServers(plugin, errors)
  else if (step.action === 'extract') value = await api.extractMcpServersFromPlugins(plugins, errors)
  else if (step.action === 'channels') value = api.getUnconfiguredChannels(plugin)
  else if (step.action === 'resolve') value = api.resolvePluginMcpEnvironment(step.config, plugin, step.userConfig, errors, plugin.name, step.server)
  else if (step.action === 'scope') value = api.addPluginScopeToServers(step.servers, plugin.name, plugin.source)
  else if (step.action === 'load-user') value = user.loadMcpServerUserConfig(step.id, step.server)
  else if (step.action === 'save-user') user.saveMcpServerUserConfig(step.id, step.server, step.values, step.schema)
  else if (step.action === 'env') { if (step.value === null) delete process.env[step.key]; else process.env[step.key] = step.value }
  else if (step.action === 'clear-options') options.clearPluginOptionsCache()
  else if (step.action === 'files') for (const [path, content] of Object.entries(step.files)) writeFileSync(path, content as string)
  else throw new Error('Undeclared plugin MCP fixture action')
  const dataPath = directories.pluginDataDirPath(plugin.source)
  observations.push({ value: value ?? null, errors: structuredClone(errors), plugins: structuredClone(plugins), dataExists: existsSync(dataPath), settings: JSON.parse(readFileSync(join(config, 'settings.json'), 'utf8')), credentials: JSON.parse(readFileSync(join(config, '.credentials.json'), 'utf8')) })
}
process.stdout.write(JSON.stringify(observations))
