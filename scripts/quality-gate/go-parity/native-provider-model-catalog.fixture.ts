import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Provider model catalog Source comparison')
if (process.env.CC_HAHA_PROVIDER_MODELS_FIXTURE !== '1') throw new Error('Missing provider models fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (input.now) {
  const OriginalDate = Date
  const timestamp = new OriginalDate(input.now).valueOf()
  class FixedDate extends OriginalDate { constructor(value?: string | number) { super(value === undefined ? timestamp : value) }; static now() { return timestamp } }
  globalThis.Date = FixedDate as DateConstructor
}
process.env.CLAUDE_CODE_LOCAL_VERSION = 'fixture-version'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture-build'
await import('../../../preload')
mkdirSync(join(home, '.claude'), { recursive: true })
writeFileSync(join(home, '.claude', '.claude.json'), '{}')
const { buildModelsUrlCandidates, fetchProviderModels } = await import('../../../src/server/services/providerModelCatalog')
const originalTimeout = AbortSignal.timeout
if (input.timeoutMs !== undefined) {
  // Replace only the timer primitive; actual original fetch and HTTP body
  // handling still execute against the private loopback server.
  AbortSignal.timeout = () => originalTimeout(input.timeoutMs)
}
const observations = []
for (const row of input.cases) {
  writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(row.settings ?? { network: { proxy: { mode: 'direct' } } }))
  delete process.env.CC_HAHA_SYSTEM_PROXY_URL
  delete process.env.CC_HAHA_SYSTEM_PROXY_ERROR
  for (const [key, value] of Object.entries(row.env ?? {})) process.env[key] = value as string
  if (row.saved) {
    mkdirSync(join(home, '.claude', 'cc-haha'), { recursive: true })
    writeFileSync(join(home, '.claude', 'cc-haha', 'providers.json'), JSON.stringify({ schemaVersion: 6, activeId: null, providers: [row.saved], providerOrder: [], officialProviderModels: {} }))
  }
  if (row.action === 'candidates') {
    try { observations.push({ value: buildModelsUrlCandidates(row.input.baseUrl, row.input) }) }
    catch (error) { observations.push({ error: (error as Error).message }) }
  } else if (row.action === 'fetch') observations.push(await fetchProviderModels(row.input))
  else if (row.action === 'connectionTest') {
    const { ProviderService } = await import('../../../src/server/services/providerService')
    observations.push(await new ProviderService().testProviderConfig(row.input))
  }
  else if (row.action === 'transformResponse') {
    try {
      const { openaiChatToAnthropic } = await import('../../../src/server/proxy/transform/openaiChatToAnthropic')
      const { openaiResponsesToAnthropic } = await import('../../../src/server/proxy/transform/openaiResponsesToAnthropic')
      observations.push({ value: row.format === 'openai_chat' ? openaiChatToAnthropic(row.value, row.model) : openaiResponsesToAnthropic(row.value, row.model) })
    } catch (error) { observations.push({ error: (error as Error).message }) }
  }
  else if (row.action === 'testSchema') {
    const { TestProviderSchema } = await import('../../../src/server/types/provider')
    const result = TestProviderSchema.safeParse(row.value)
    observations.push(result.success ? [] : result.error.issues.map((issue) => issue.message))
  }
  else if (row.action === 'api') {
    const { handleProvidersApi } = await import('../../../src/server/api/providers')
    const path = row.path ?? '/api/providers/models'
    const url = new URL(path, 'http://127.0.0.1:3000')
    const response = await handleProvidersApi(new Request(url, {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: row.body,
    }), url, url.pathname.split('/').filter(Boolean))
    observations.push({ status: response.status, body: await response.json() })
  } else throw new Error(`Unknown model catalog fixture action: ${row.action}`)
}
process.stdout.write(JSON.stringify(observations))
