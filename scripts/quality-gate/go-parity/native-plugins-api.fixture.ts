import { assertDisposableFixtureHome } from './fixture-guard'
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { mock } from 'bun:test'

const home = assertDisposableFixtureHome(process.env.HOME, 'Plugins API Source comparison')
if (process.env.CC_HAHA_PLUGINS_FIXTURE !== '1') throw new Error('Missing plugins fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home, '.claude')
const workspace = join(home, 'workspace')
mkdirSync(config, { recursive: true })
mkdirSync(workspace, { recursive: true })
mkdirSync(join(home, 'managed'), { recursive: true })
mock.module('../../../src/utils/settings/managedPath', () => ({
  getManagedFilePath: () => join(home, 'managed'),
  getManagedSettingsDropInDir: () => join(home, 'managed', 'managed-settings.d'),
}))
writeFileSync(join(config, '.claude.json'), '{}')
writeFileSync(join(config, 'settings.json'), '{}')
process.env.CLAUDE_CODE_USE_NATIVE_FILE_SEARCH = '1'
await import('../../../preload')
// Bind the real file-based platform storage to the private fixture config.
// No host keychain process or developer credential entry is consulted.
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
mock.module('../../../src/utils/secureStorage/index', () => ({ getSecureStorage: () => plainTextStorage }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(workspace)
state.setOriginalCwd(workspace)
state.setProjectRoot(workspace)
state.setIsInteractive(false)
Date.now = () => 1791244800000
const expand = (value: string) => value.replaceAll('$HOME', home).replaceAll('$WORKSPACE', workspace).replaceAll('$CONFIG', config)
const reduce = (value: any) => JSON.parse(JSON.stringify(value).replaceAll(home, '$HOME').replace(/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z/g, '$DATE'))
function snapshot() {
  const files: Record<string, any> = {}
  const walk = (directory: string) => {
    if (!existsSync(directory)) return
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) {
        if (entry.name === '.git' || path === join(home, 'Library') || path === join(home, 'tmp') || path === join(config, 'debug') || path === join(config, 'cc-haha', 'diagnostics')) continue
        walk(path)
      }
      else if (entry.isFile() && !path.endsWith('.claude.json')) {
        const content = readFileSync(path, 'utf8')
        try { files[path.replace(home, '$HOME')] = entry.name.endsWith('.json') ? reduce(JSON.parse(content)) : content.replaceAll(home, '$HOME') }
        catch { files[path.replace(home, '$HOME')] = content.replaceAll(home, '$HOME') }
      }
    }
  }
  walk(home)
  return files
}
const observations = []
for (const row of input.cases) {
  if (row.reset) {
    rmSync(join(config, 'plugins'), { recursive: true, force: true })
    rmSync(join(workspace, '.claude'), { recursive: true, force: true })
    rmSync(join(home, 'market'), { recursive: true, force: true })
    writeFileSync(join(config, 'settings.json'), JSON.stringify(row.settings ?? {}))
    writeFileSync(join(home, 'managed', 'managed-settings.json'), '{}')
  }
  for (const file of row.files ?? []) {
    const path = expand(file.path)
    if (!path.startsWith(home + '/')) throw new Error('Unsafe plugins fixture path')
    mkdirSync(dirname(path), { recursive: true })
    if (file.symlink) symlinkSync(expand(file.symlink), path)
    else writeFileSync(path, expand(file.content))
  }
  if (row.git) {
    const directory = expand(row.git)
    if (!directory.startsWith(home + '/')) throw new Error('Unsafe private git directory')
    for (const args of [['init', '-q'], ['add', '.'], ['-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'fixture']]) {
      const result = spawnSync('/usr/bin/git', args, { cwd: directory, env: { PATH: process.env.PATH, HOME: home, GIT_CONFIG_NOSYSTEM: '1', GIT_AUTHOR_DATE: '2026-10-01T00:00:00Z', GIT_COMMITTER_DATE: '2026-10-01T00:00:00Z' } })
      if (result.status !== 0) throw new Error('Private git fixture failed')
    }
  }
  const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
  const { clearAllCaches } = await import('../../../src/utils/plugins/cacheUtils')
  resetSettingsCache()
  if (row.clear) clearAllCaches()
  if (row.action === 'lsp') {
    const { loadPluginLspServers } = await import('../../../src/utils/plugins/lspPluginIntegration')
    const errors: any[] = []
    const plugin = JSON.parse(expand(JSON.stringify(row.plugin)))
    const servers = await loadPluginLspServers(plugin, errors)
    observations.push(reduce({ servers: servers ?? null, errors }))
    continue
  }
  if (row.action === 'lookup') {
    const { getPluginById } = await import('../../../src/utils/plugins/marketplaceManager')
    observations.push({ plugin: reduce(await getPluginById(row.id)), files: snapshot() })
    continue
  }
  if (row.action === 'update-op') {
    const { updatePluginOp } = await import('../../../src/services/plugins/pluginOperations')
    observations.push({ body: reduce(await updatePluginOp(row.id, row.scope)), files: snapshot() })
    continue
  }
  if (row.action === 'marketplace-auto-update') {
    const { setMarketplaceAutoUpdate } = await import('../../../src/utils/plugins/marketplaceManager')
    let error
    try { await setMarketplaceAutoUpdate(row.name, row.enabled) } catch (failure) { error = failure instanceof Error ? failure.message : String(failure) }
    observations.push({ files: snapshot(), ...(error === undefined ? {} : { error }) })
    continue
  }
  if (row.action === 'hooks') {
    const { loadPluginHooks, pruneRemovedPluginHooks, clearPluginHookCache } = await import('../../../src/utils/plugins/loadPluginHooks')
    if (row.callbacks) state.registerHookCallbacks(row.callbacks)
    if (row.operation === 'load') await loadPluginHooks()
    else if (row.operation === 'clear') clearPluginHookCache()
    else if (row.operation === 'prune') await pruneRemovedPluginHooks()
    else throw new Error('Invalid hooks operation')
    observations.push(reduce({ hooks: state.getRegisteredHooks() }))
    continue
  }
  const { handlePluginsApi } = await import('../../../src/server/api/plugins')
  const url = new URL(expand(row.path), 'http://127.0.0.1:3000')
  const raw = row.raw === undefined ? row.body === undefined ? undefined : expand(JSON.stringify(row.body)) : expand(row.raw)
  const response = await handlePluginsApi(new Request(url, { method: row.method, headers: { 'Content-Type': 'application/json' }, body: raw }), url, url.pathname.split('/').filter(Boolean))
  if (row.rawResponse) observations.push({ status: response.status, bodyRaw: (await response.text()).replaceAll(home, '$HOME') })
  else observations.push({ status: response.status, body: reduce(await response.json()), files: snapshot() })
}
process.stdout.write(JSON.stringify(observations))
