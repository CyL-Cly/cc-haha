import { assertDisposableFixtureHome } from './fixture-guard'
import { readFileSync, realpathSync, writeFileSync } from 'node:fs'
import { isAbsolute, relative, resolve, sep } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native plugin settings')
if (process.env.CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Native plugin settings require the isolated production fixture')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Object.keys(input).some(key => !['schemas', 'runtime', 'operations'].includes(key))) throw new Error('Undeclared plugin settings fixture input')
// Source permits an explicit disposable managed directory in its Ant build.
// No policy, configuration, registry or loader business function is mocked.
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
const { SettingsSchema } = await import('../../../src/utils/settings/types')
const { formatZodError } = await import('../../../src/utils/settings/validation')
const schemas = (input.schemas ?? []).map((raw: string) => {
  const parsed = SettingsSchema().safeParse(JSON.parse(raw))
  return parsed.success ? { settings: JSON.stringify(parsed.data), errors: '[]' } : { settings: null, errors: JSON.stringify(formatZodError(parsed.error, 'fixture')) }
})
const observations = []
if (input.runtime) {
  const cwd = input.runtime.cwd
  const within = relative(realpathSync(home), realpathSync(cwd))
  if (!isAbsolute(cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned plugin project')
  process.chdir(cwd)
  const state = await import('../../../src/bootstrap/state')
  const settings = await import('../../../src/utils/settings/settings')
  const cache = await import('../../../src/utils/settings/settingsCache')
  const { loadAllPluginsCacheOnly, loadAllPlugins, loadInstalledPluginsForProject, clearPluginCache } = await import('../../../src/utils/plugins/pluginLoader')
  const { clearInstalledPluginsCache } = await import('../../../src/utils/plugins/installedPluginsManager')
  const { loadPluginOutputStyles, clearPluginOutputStyleCache } = await import('../../../src/utils/plugins/loadPluginOutputStyles')
  state.setOriginalCwd(cwd)
  state.setProjectRoot(cwd)
  state.setCwdState(cwd)
  state.setAllowedSettingSources(input.runtime.allowed ?? ['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
  state.setAdditionalDirectoriesForClaudeMd(input.runtime.additionalDirs ?? [])
  state.setInlinePlugins(input.runtime.inlinePaths ?? [])
  if (input.runtime.flagPath) state.setFlagSettingsPath(input.runtime.flagPath)
  cache.resetSettingsCache()
  clearPluginCache()
  clearInstalledPluginsCache()
  for (const op of input.operations ?? []) {
    if (op.kind === 'load') {
      const plugins = op.project ? await loadInstalledPluginsForProject(op.project) : op.full ? await loadAllPlugins() : await loadAllPluginsCacheOnly()
      observations.push({ plugins, settings: settings.getInitialSettings(), base: cache.getPluginSettingsBase() ?? null })
      if (input.runtime.includeStyles) {
        observations.at(-1)!.styles = (await loadPluginOutputStyles()).map(style => ({ name: style.name, prompt: style.prompt, description: style.description, source: style.source, forceForPlugin: style.forceForPlugin === true }))
      }
    } else if (op.kind === 'invalidate') {
      cache.resetSettingsCache()
      clearPluginCache()
      clearPluginOutputStyleCache()
    } else if (op.kind === 'write') {
      const path = resolve(home, op.path)
      const within = relative(resolve(home), path)
      if (within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned fixture write')
      // File must already exist in the parent-owned disposable project.
      readFileSync(path)
      writeFileSync(path, op.content)
    } else if (op.kind === 'skillHTTP') {
      const url = new URL(op.path, 'http://127.0.0.1')
      if (url.origin !== 'http://127.0.0.1' || !url.pathname.startsWith('/api/skills')) throw new Error('Undeclared Skills fixture endpoint')
      const requestedCwd = resolve(cwd, url.searchParams.get('cwd') || cwd)
      const within = relative(realpathSync(home), realpathSync(requestedCwd))
      if (within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned Skills fixture project')
      const { handleSkillsApi } = await import('../../../src/server/api/skills')
      const response = await handleSkillsApi(new Request(url, { method: op.method }), url, url.pathname.split('/').filter(Boolean))
      observations.push({ status: response.status, body: await response.text() })
    } else throw new Error('Undeclared plugin settings operation')
  }
}
process.stdout.write(JSON.stringify({ schemas, observations }))
