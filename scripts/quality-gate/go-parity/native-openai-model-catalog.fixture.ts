import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI model catalog')
if (process.env.CC_HAHA_OPENAI_CATALOG_FIXTURE !== '1') throw new Error('Unowned OpenAI catalog fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { fetchOpenAICodexModelCatalog, clearOpenAICodexModelCatalogCache } = await import('../../../src/services/openaiAuth/modelCatalog')
const { getDesktopOpenAICodexModelCatalog } = await import('../../../src/server/services/openaiModelCatalog')
const { hahaOpenAIOAuthService } = await import('../../../src/server/services/hahaOpenAIOAuthService')
const { getOpenAIModelCatalogEntry } = await import('../../../src/services/openaiAuth/models')
const observations: unknown[] = []
for (const row of input.rows ?? []) {
  let request: unknown
  const fetchOverride = async (url: any, options: any) => {
    request = { url: String(url), method: options.method, headers: Object.fromEntries(new Headers(options.headers)) }
    return new Response(row.body ?? JSON.stringify({ models: row.models }), { status: row.status ?? 200 })
  }
  try { observations.push({ value: await fetchOpenAICodexModelCatalog(fetchOverride as any, row.tokens ?? { accessToken: 'fake-access', accountId: 'fake-account' }), request }) }
  catch (error) { observations.push({ error: (error as Error).message, request }) }
}
const originalNow = Date.now
let now = input.now ?? 1770000000000
Date.now = () => now
clearOpenAICodexModelCatalogCache()
const requests: unknown[] = []
const cache: unknown[] = []
let reply: any = { models: [{ slug: 'remote-a', visibility: 'list' }] }
let pending: (() => void) | undefined
let hold = false
const fetchOverride: typeof fetch = (async (_url, options) => {
  const headers = new Headers(options?.headers)
  requests.push({ account: headers.get('ChatGPT-Account-Id'), authorization: headers.get('Authorization') })
  const captured = reply
  if (hold) await new Promise<void>(resolve => { pending = resolve })
  if (captured.error) throw new Error(captured.error)
  return new Response(JSON.stringify(captured), { status: captured.status ?? 200 })
}) as typeof fetch
try {
  for (const action of input.actions ?? []) {
    if (action.now !== undefined) now = action.now
    if (action.reply !== undefined) reply = action.reply
    if (action.hold !== undefined) hold = action.hold
    if (action.tokens !== undefined) {
      if (action.tokens === null) await hahaOpenAIOAuthService.deleteTokens()
      else await hahaOpenAIOAuthService.saveTokens(action.tokens)
    }
    if (action.complete) { pending?.(); pending = undefined; await new Promise(resolve => setTimeout(resolve, 0)) }
    if (action.wait) await new Promise(resolve => setTimeout(resolve, 0))
    if (action.resolve) {
      const models = await getDesktopOpenAICodexModelCatalog({ fetchOverride, forceRefresh: action.force })
      cache.push({ models: models.map(model => model.value), calls: requests.length })
    }
  }
  const lookups = (input.lookups ?? []).map((row: any) => getOpenAIModelCatalogEntry(row.model, row.catalog) ?? null)
  process.stdout.write(JSON.stringify({ observations, cache, requests, lookups }))
} finally {
  pending?.()
  hahaOpenAIOAuthService.dispose()
  clearOpenAICodexModelCatalogCache()
  Date.now = originalNow
}
