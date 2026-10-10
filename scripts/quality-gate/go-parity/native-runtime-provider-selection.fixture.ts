import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Runtime provider default versus explicit Source')
await import('../../../preload')
const input = await Bun.stdin.json()
const config = process.env.CLAUDE_CONFIG_DIR!
await mkdir(join(config, 'cc-haha'), { recursive: true })
await writeFile(join(config, 'cc-haha/providers.json'), JSON.stringify(input.index))
const source = await import('../../../src/server/services/providerRuntimeEnv')
const { ProviderService } = await import('../../../src/server/services/providerService')
const service = new ProviderService()
const rows = []
for (const row of input.cases) {
  const environment = row.selected ? await service.getProviderRuntimeEnv(row.selected) : source.readActiveProviderManagedEnv(config)
  rows.push({ baseURL: environment?.ANTHROPIC_BASE_URL, token: environment?.ANTHROPIC_AUTH_TOKEN, apiKey: environment?.ANTHROPIC_API_KEY, model: environment?.ANTHROPIC_MODEL })
}
process.stdout.write(JSON.stringify(rows))
