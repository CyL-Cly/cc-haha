import { assertDisposableFixtureHome } from './fixture-guard'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Connector plugin artifacts')
if (process.env.CC_HAHA_CONNECTOR_ARTIFACT_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Connector artifacts require the isolated production fixture')
}

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { renderConnectorSkill } = await import('../../../src/services/connectors/pluginBridge')
const { buildRemotePlugin } = await import('../../../src/services/connectors/remoteConnector')
const { mkdir, readFile, writeFile } = await import('node:fs/promises')
const { join } = await import('node:path')
if (input.store) {
  const clock = new Date(input.store.clock).getTime()
  const RealDate = Date
  globalThis.Date = class extends RealDate {
    constructor(value?: string | number) { super(value === undefined ? clock : value) }
    static now() { return clock }
  } as DateConstructor
  const { cacheAndRegisterPlugin } = await import('../../../src/utils/plugins/pluginInstallationHelpers')
  const { loadInstalledPluginsFromDisk, removePluginInstallation } = await import('../../../src/utils/plugins/installedPluginsManager')
  const { addMarketplaceSource } = await import('../../../src/utils/plugins/marketplaceManager')
  const { markPluginVersionOrphaned } = await import('../../../src/utils/plugins/cacheUtils')
  const config = process.env.CLAUDE_CONFIG_DIR!
  const root = join(config, 'connectors', 'marketplace')
  const plugin = join(root, 'plugins', 'office-fixture')
  await mkdir(join(root, '.claude-plugin'), { recursive: true })
  await mkdir(join(plugin, '.claude-plugin'), { recursive: true })
  await mkdir(join(plugin, 'skills', 'office-fixture'), { recursive: true })
  const manifest = { name: 'office-fixture', version: '1.0.0-connector.1', description: 'fixture', skills: './skills' }
  const entry = { name: 'office-fixture', version: manifest.version, source: './plugins/office-fixture', strict: true }
  await writeFile(join(root, '.claude-plugin', 'marketplace.json'), JSON.stringify({ name: 'haha-connectors', owner: { name: 'Claude Code Haha' }, plugins: [entry] }))
  await writeFile(join(plugin, '.claude-plugin', 'plugin.json'), JSON.stringify(manifest))
  await writeFile(join(plugin, 'skills', 'office-fixture', 'SKILL.md'), '---\nname: fixture\n---\nRead this fixture.\n')
  await writeFile(join(config, 'settings.json'), JSON.stringify({ enabledPlugins: {} }))
  await addMarketplaceSource({ source: 'directory', path: root })
  const cache = await cacheAndRegisterPlugin('office-fixture@haha-connectors', entry, 'user', undefined, plugin)
  const before = loadInstalledPluginsFromDisk()
  const known = JSON.parse(await readFile(join(config, 'plugins', 'known_marketplaces.json'), 'utf8'))
  removePluginInstallation('office-fixture@haha-connectors', 'user')
  await markPluginVersionOrphaned(cache)
  const result = { known, before, after: loadInstalledPluginsFromDisk(), marker: await readFile(join(cache, '.orphaned_at'), 'utf8'), skill: await readFile(join(cache, 'skills', 'office-fixture', 'SKILL.md'), 'utf8') }
  process.stdout.write(JSON.stringify(result))
  process.exit(0)
}
const rendered = input.skills.map((item: any) => {
  try { return { text: renderConnectorSkill(item.definition, item.installation) } }
  catch (error) { return { error: (error as Error).message } }
})
const remote = input.remote.map((item: any) => {
  try { return { plugin: buildRemotePlugin(item) } }
  catch (error) { return { error: (error as Error).message } }
})
process.stdout.write(JSON.stringify({ rendered, remote }))
