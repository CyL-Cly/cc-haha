import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { spyOn } from 'bun:test'

const home = assertDisposableFixtureHome(process.env.HOME, 'Desktop OpenAI selection')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
Bun.plugin({ name: 'actual-desktop-runtime-selection', setup(build) {
  build.onLoad({ filter: /\/server\/ws\/handler\.ts$/ }, async args => ({
    contents: `${await Bun.file(args.path).text()}\nexport { normalizeRuntimeOverrideInput }`, loader: 'ts',
  }))
} })
const catalogModule = await import('../../../src/server/services/openaiModelCatalog')
const { OPENAI_CODEX_MODEL_CATALOG } = await import('../../../src/services/openaiAuth/models')
let catalog = OPENAI_CODEX_MODEL_CATALOG
let catalogCalls = 0
spyOn(catalogModule, 'getDesktopOpenAICodexModelCatalog').mockImplementation(async () => { catalogCalls++; return catalog })
const handler: any = await import('../../../src/server/ws/handler')
mkdirSync(join(home, '.claude', 'cc-haha'), { recursive: true })
const observations = []
for (const row of input.rows) {
  catalog = row.catalog ?? OPENAI_CODEX_MODEL_CATALOG
  catalogCalls = 0
  writeFileSync(join(home, '.claude', 'cc-haha', 'providers.json'), JSON.stringify({ schemaVersion: 6, activeId: 'openai-official', providers: [], officialProviderModels: {} }))
  writeFileSync(join(home, '.claude', 'cc-haha', 'settings.json'), JSON.stringify(row.managed ?? {}))
  writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify({ effort: 'max', model: 'unrelated-global-model' }))
  observations.push({ result: await handler.normalizeRuntimeOverrideInput(row.input), catalogCalls })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ observations, capabilities }), () => resolve()))
process.exit(0)
