import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native plugin loader')
if (process.env.CC_HAHA_PLUGIN_LOADER_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Plugin loader contract requires its isolated production fixture')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
// Policy reads remain in this disposable home. The original Ant-only path
// override is selected before any production loader/settings imports.
if (input.loadCache) {
  process.env.USER_TYPE = 'ant'
  process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${process.env.HOME}/managed`
}
const schemas = await import('../../../src/utils/plugins/schemas')
const { HooksSchema } = await import('../../../src/schemas/hooks')

// Structural declarations only: no parse result, model output, business graph
// or runtime plugin instance is a schema definition.
function recipe(schema: any): unknown {
  const def = schema._zod.def
  const value: Record<string, unknown> = { type: def.type }
  const serialize = (v: any): any => {
    if (v instanceof RegExp) return { regex: v.source, flags: v.flags }
    if (typeof v === 'function') return { function: v.toString() }
    if (Array.isArray(v)) return v.map(serialize)
    if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k, v]) => [k, k === 'error' && typeof v === 'function' ? v({}) : serialize(v)]))
    return v
  }
  for (const key of ['values', 'entries', 'defaultValue', 'catchValue', 'discriminator']) {
    if (key in def) value[key] = serialize(def[key])
  }
  if (def.shape) value.shape = Object.fromEntries(Object.entries(def.shape).map(([key, child]) => [key, recipe(child)]))
  for (const key of ['innerType', 'element', 'keyType', 'valueType', 'in', 'out', 'catchall']) {
    if (def[key]) value[key] = recipe(def[key])
  }
  if (def.options) value.options = def.options.map(recipe)
  if (def.getter) value.inner = recipe(def.getter())
  if (def.transform) value.transform = def.transform.toString()
  if (def.checks?.length) value.checks = def.checks.map((check: any) => serialize(check._zod.def))
  return value
}
if (input.schema) {
  process.stdout.write(JSON.stringify({ manifest: recipe(schemas.PluginManifestSchema()), hooks: recipe(HooksSchema()), marketplace: recipe(schemas.PluginMarketplaceSchema()), known: recipe(schemas.KnownMarketplacesFileSchema()), installed: recipe(schemas.InstalledPluginsFileSchema()) }))
} else {
  const { createPluginFromPath, mergePluginSources, loadAllPlugins, loadAllPluginsCacheOnly, loadInstalledPluginsForProject, clearPluginCache } = await import('../../../src/utils/plugins/pluginLoader')
  const { verifyAndDemote } = await import('../../../src/utils/plugins/dependencyResolver')
  const created = []
  for (const item of input.create ?? []) {
    try { created.push({ result: await createPluginFromPath(item.path, item.source, item.enabled, item.fallback, item.strict) }) }
    catch (error) { created.push({ error: (error as Error).message }) }
  }
  const merged = (input.merge ?? []).map((item: any) => mergePluginSources({ ...item, managedNames: item.managedNames === null ? null : new Set(item.managedNames) }))
  const dependencies = (input.dependencies ?? []).map((items: any) => {
    const result = verifyAndDemote(items)
    return { demoted: [...result.demoted], errors: result.errors }
  })
  let cache
  let pluginSettings
  if (input.loadCache) {
    process.chdir(input.runtime.cwd)
    const state = await import('../../../src/bootstrap/state')
    const settings = await import('../../../src/utils/settings/settingsCache')
    const installed = await import('../../../src/utils/plugins/installedPluginsManager')
    const builtin = await import('../../../src/plugins/builtinPlugins')
    state.setOriginalCwd(input.runtime.cwd)
    state.setProjectRoot(input.runtime.cwd)
    state.setCwdState(input.runtime.cwd)
    state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
    state.setInlinePlugins(input.runtime.inlinePaths ?? [])
    state.setAdditionalDirectoriesForClaudeMd(input.runtime.additionalDirs ?? [])
    builtin.clearBuiltinPlugins()
    for (const definition of input.runtime.builtins ?? []) {
      const { available, ...declaration } = definition
      builtin.registerBuiltinPlugin({ ...declaration, isAvailable: () => available !== false })
    }
    clearPluginCache('native-fixture')
    installed.clearInstalledPluginsCache()
    settings.resetSettingsCache()
    cache = input.runtime.project
      ? await loadInstalledPluginsForProject(input.runtime.project)
      : input.loadFull ? await loadAllPlugins() : await loadAllPluginsCacheOnly()
    pluginSettings = settings.getPluginSettingsBase() ?? null
  }
  const validated = (input.manifests ?? []).map((value: unknown) => {
    const result = schemas.PluginManifestSchema().safeParse(value)
    return result.success ? { value: result.data } : { issues: result.error.issues.map((issue: any) => ({ path: issue.path, message: issue.message })) }
  })
  process.stdout.write(JSON.stringify({ created, merged, dependencies, validated, cache, pluginSettings }))
}
