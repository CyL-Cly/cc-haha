import { assertDisposableFixtureHome } from './fixture-guard'
import { isAbsolute, relative, sep } from 'node:path'
import { realpathSync } from 'node:fs'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native plugin commands')
if (process.env.CC_HAHA_NATIVE_PLUGIN_COMMAND_FIXTURE !== '1') throw new Error('Undeclared plugin command fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const within = relative(realpathSync(home), realpathSync(input.cwd))
if (!isAbsolute(input.cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned plugin command project')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
process.chdir(input.cwd)
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd)
state.setProjectRoot(input.cwd)
state.setCwdState(input.cwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { loadInstalledPluginsForProject } = await import('../../../src/utils/plugins/pluginLoader')
const { loadPluginCommandsFromEnabledPlugins, loadPluginSkillsFromEnabledPlugins } = await import('../../../src/utils/plugins/loadPluginCommands')
const plugins = await loadInstalledPluginsForProject(input.cwd)
const commands = await loadPluginCommandsFromEnabledPlugins(plugins.enabled)
const skills = await loadPluginSkillsFromEnabledPlugins(plugins.enabled)
const project = (command: typeof commands[number]) => {
  const result = JSON.parse(JSON.stringify(command))
  result.userFacingName = command.userFacingName?.() ?? command.name
  return result
}
process.stdout.write(JSON.stringify({ commands: commands.map(project), skills: skills.map(project), errors: plugins.errors }))
