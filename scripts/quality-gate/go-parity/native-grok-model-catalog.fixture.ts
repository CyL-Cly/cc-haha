import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native Grok model catalog')
if (process.env.CC_HAHA_GROK_CATALOG_FIXTURE !== '1') throw new Error('Unowned Grok catalog fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { fetchGrokModelCatalog, getGrokModelCatalog, clearGrokModelCatalogCache } = await import('../../../src/services/grokAuth/modelCatalog')
const { hahaGrokOAuthService } = await import('../../../src/server/services/hahaGrokOAuthService')
const { resolveGrokModel, resolveGrokReasoningEffort, GROK_MODEL_CATALOG } = await import('../../../src/services/grokAuth/models')
const observations: unknown[] = []
for (const row of input.rows ?? []) {
  let request: unknown
  const fetchOverride = async (url: any, options: any) => {
    request = { url: String(url), method: options.method, headers: Object.fromEntries(new Headers(options.headers)) }
    return new Response(row.body ?? JSON.stringify(row.models), { status: row.status ?? 200 })
  }
  try { observations.push({ value: await fetchGrokModelCatalog(fetchOverride as any, 'fake-access'), request }) }
  catch (error) { observations.push({ error: (error as Error).message, request }) }
}
const originalNow = Date.now
let now = input.now ?? 1770000000000
Date.now = () => now
clearGrokModelCatalogCache()
const requests: unknown[] = []
const cache: unknown[] = []
let reply: any = { models: [{ id: 'remote-a' }] }
let pending: (() => void) | undefined
let hold = false
const fetchOverride: typeof fetch = (async (_url, options) => {
  const headers = new Headers(options?.headers)
  requests.push({ authorization: headers.get('Authorization') })
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
      if (action.tokens === null) await hahaGrokOAuthService.deleteTokens()
      else await hahaGrokOAuthService.saveTokens(action.tokens)
    }
    if (action.complete) { pending?.(); pending = undefined; await new Promise(resolve => setTimeout(resolve, 0)) }
    if (action.wait) await new Promise(resolve => setTimeout(resolve, 0))
    if (action.resolve) {
      const tokens = await hahaGrokOAuthService.ensureFreshTokens()
      const models = await getGrokModelCatalog({ fetchOverride, forceRefresh: action.force, ...(tokens?.accessToken ? { accessToken: tokens.accessToken } : {}), accountKey: tokens?.email ?? (tokens ? 'authenticated-default' : 'logged-out') })
      cache.push({ models: models.map(model => model.value), calls: requests.length })
    }
  }
  const lookups = (input.lookups ?? []).map((row: any) => ({ model: resolveGrokModel(row.model), effort: resolveGrokReasoningEffort(row.model, row.effort, row.catalog ?? GROK_MODEL_CATALOG) ?? null }))
  process.stdout.write(JSON.stringify({ observations, cache, requests, lookups }))
} finally {
  pending?.()
  hahaGrokOAuthService.dispose()
  clearGrokModelCatalogCache()
  Date.now = originalNow
}
