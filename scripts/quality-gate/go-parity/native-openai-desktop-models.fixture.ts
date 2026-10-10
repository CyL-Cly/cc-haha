import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { spyOn } from 'bun:test'

const home = assertDisposableFixtureHome(process.env.HOME, 'Desktop OpenAI model API')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
await import('../../../preload')
const catalogModule = await import('../../../src/server/services/openaiModelCatalog')
const { OPENAI_CODEX_MODEL_CATALOG } = await import('../../../src/services/openaiAuth/models')
let catalog = OPENAI_CODEX_MODEL_CATALOG
let catalogCalls = 0
spyOn(catalogModule, 'getDesktopOpenAICodexModelCatalog').mockImplementation(async () => { catalogCalls++; return catalog })
const { handleModelsApi } = await import('../../../src/server/api/models')
const { ProviderService } = await import('../../../src/server/services/providerService')
const directory = join(home, '.claude', 'cc-haha')
await mkdir(directory, { recursive: true })
const observations = []
for (const row of input.rows) {
  catalog = row.catalog ?? OPENAI_CODEX_MODEL_CATALOG
  catalogCalls = 0
  await writeFile(join(directory, 'providers.json'), JSON.stringify({ schemaVersion: 6, activeId: 'openai-official', providers: [], officialProviderModels: { 'openai-official': { main: 'unrelated-alias', haiku: '', sonnet: '', opus: '' } } }))
  await writeFile(join(directory, 'settings.json'), JSON.stringify(row.managed ?? {}))
  const url = new URL(row.path, 'http://localhost:3456')
  const request = new Request(url, { method: row.method ?? 'GET', ...(row.body ? { body: row.body } : {}) })
  const response = await handleModelsApi(request, url, url.pathname.split('/').filter(Boolean))
  observations.push({ status: response.status, body: await response.json(), catalogCalls, managed: await new ProviderService().getManagedSettings() })
}
process.stdout.write(JSON.stringify(observations))
