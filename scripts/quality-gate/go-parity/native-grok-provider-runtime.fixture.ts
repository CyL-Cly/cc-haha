import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Grok provider runtime')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
await import('../../../preload')
const { ProviderService } = await import('../../../src/server/services/providerService')
const { mergeActiveProviderManagedEnv } = await import('../../../src/server/services/providerRuntimeEnv')
const directory = join(home, '.claude', 'cc-haha')
await mkdir(directory, { recursive: true })
const service = new ProviderService()
const { setGrokRuntimeModelCatalog, GROK_MODEL_CATALOG } = await import('../../../src/services/grokAuth/models')
const observations = []
for (const row of input.rows) {
  setGrokRuntimeModelCatalog(row.catalog ?? GROK_MODEL_CATALOG)
  await writeFile(join(directory, 'providers.json'), JSON.stringify(row.index))
  await writeFile(join(directory, 'settings.json'), JSON.stringify(row.managed))
  const provider = await service.getProvider('grok-official')
  const runtime = await service.getProviderRuntimeEnv('grok-official')
  await service.activateProvider('grok-official')
  observations.push({
    provider,
    runtime,
    resolved: mergeActiveProviderManagedEnv(input.base, join(home, '.claude')),
    index: JSON.parse(await readFile(join(directory, 'providers.json'), 'utf8')),
    managed: await service.getManagedSettings(),
  })
}
process.stdout.write(JSON.stringify(observations))
