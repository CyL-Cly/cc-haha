import { assertDisposableFixtureHome } from './fixture-guard'
import { readdir, lstat, readFile, readlink } from 'node:fs/promises'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native plugin materialization')
if (process.env.CC_HAHA_PLUGIN_MATERIALIZE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Plugin materialization requires its isolated production fixture')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
for (const [key, value] of Object.entries(input.environment ?? {})) {
  if (!key.startsWith('CLAUDE_CODE_PLUGIN_') && !['CLAUDE_CODE_USE_COWORK_PLUGINS', 'CLAUDE_CODE_REMOTE'].includes(key)) throw new Error('Unexpected fixture environment')
  process.env[key] = value as string
}
const OriginalDate = Date
if (input.now !== undefined) {
  globalThis.Date = class extends OriginalDate {
    constructor(...args: any[]) { super(...(args.length ? args : [input.now]) as [any]) }
    static now() { return input.now }
  } as DateConstructor
}
if (input.random !== undefined) Math.random = () => input.random
const loader = await import('../../../src/utils/plugins/pluginLoader')
const versions = await import('../../../src/utils/plugins/pluginVersioning')
const directories = await import('../../../src/utils/plugins/pluginDirectories')
const installed = await import('../../../src/utils/plugins/installedPluginsManager')
const installation = await import('../../../src/utils/plugins/pluginInstallationHelpers')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd ?? home)
state.setCwdState(input.cwd ?? home)
state.setProjectRoot(input.cwd ?? home)
state.setUseCoworkPlugins(input.cowork ?? false)
process.chdir(input.cwd ?? home)

async function tree(root: string): Promise<unknown[]> {
  const rows = []
  async function walk(path: string, relative: string) {
    let stat
    try { stat = await lstat(path) } catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return; throw error }
    rows.push({ path: relative, type: stat.isSymbolicLink() ? 'link' : stat.isDirectory() ? 'directory' : 'file', mode: stat.mode & 0o777,
      ...(stat.isSymbolicLink() ? { link: await readlink(path) } : stat.isFile() ? { bytes: (await readFile(path)).toString('base64') } : {}) })
    if (stat.isDirectory()) for (const name of await readdir(path)) await walk(join(path, name), relative ? `${relative}/${name}` : name)
  }
  await walk(root, '')
  return rows
}
const results = []
for (const operation of input.operations) {
  try {
    let value
    switch (operation.type) {
      case 'directories': value = { primary: directories.getPluginsDirectory(), seeds: directories.getPluginSeedDirs() }; break
      case 'versionedPath': value = loader.getVersionedCachePathIn(operation.base, operation.id, operation.version); break
      case 'temporaryName': value = loader.generateTemporaryCacheNameForPlugin(operation.source); break
      case 'version': value = await versions.calculatePluginVersion(operation.id, operation.source, operation.manifest, operation.installPath, operation.provided, operation.sha); break
      case 'copy': await loader.copyDir(operation.source, operation.target); value = null; break
      case 'copyVersioned': value = await loader.copyPluginToVersionedCache(operation.source, operation.id, operation.version, operation.entry, operation.marketplaceDir); break
      case 'cache': value = await loader.cachePlugin(operation.source, operation.manifest ? { manifest: operation.manifest } : undefined); break
      case 'installAndRegister': value = await installation.cacheAndRegisterPlugin(operation.id, operation.entry, operation.scope, operation.project, operation.localSource); break
      case 'gitClone': await loader.gitClone(operation.url, operation.target, operation.ref, operation.sha); value = null; break
      case 'gitSubdir': value = await loader.installFromGitSubdir(operation.url, operation.target, operation.path, operation.ref, operation.sha) ?? null; break
      case 'session': value = installed.getInMemoryInstalledPlugins(); break
      case 'disk': value = installed.loadInstalledPluginsFromDisk(); break
      case 'clearInstalled': installed.clearInstalledPluginsCache(); value = null; break
      case 'addInstalled': installed.addInstalledPlugin(operation.id, operation.metadata, operation.scope, operation.project); value = null; break
      case 'updateDisk': installed.updateInstallationPathOnDisk(operation.id, operation.scope, operation.project, operation.path, operation.version, operation.sha); value = null; break
      case 'removeInstalled': installed.removePluginInstallation(operation.id, operation.scope, operation.project); value = null; break
      case 'pending': value = { count: installed.getPendingUpdateCount(), details: installed.getPendingUpdatesDetails(), pending: installed.hasPendingUpdates() }; break
      default: throw new Error(`Unknown fixture operation ${operation.type}`)
    }
    results.push({ value })
  } catch (error) { results.push({ error: (error as Error).message }) }
}
const trees = []
for (const path of input.inspect ?? []) trees.push(await tree(path))
process.stdout.write(JSON.stringify({ results, trees }))
