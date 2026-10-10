const input = await Bun.stdin.json()
const root = process.env.CC_HAHA_MARKET_SOURCE_ROOT
if (!root) throw new Error('Explicit captured original Source root required')
const { handleMarketApi } = await import(`${root}/src/server/api/market.ts`)
const { resetMarketCacheForTests } = await import(`${root}/src/server/services/market/cache.ts`)
const { resetClawhubOwnerCacheForTests } = await import(`${root}/src/server/services/market/clawhubProvider.ts`)
const { resetInstallLocksForTests } = await import(`${root}/src/server/services/market/installService.ts`)
resetMarketCacheForTests()
resetClawhubOwnerCacheForTests()
resetInstallLocksForTests()
let clock = input.now
Date.now = () => clock
const originalFetch = globalThis.fetch
const admitted = new Set([new URL(input.base).origin])
globalThis.fetch = (async (url, options) => {
  const target = typeof url === 'string' ? url : url instanceof URL ? url.href : url.url
  if (!admitted.has(new URL(target).origin)) throw new Error('Market Source fixture blocked external transport')
  return originalFetch(url, options)
}) as typeof fetch
const results = []
for (const step of input.steps) {
  if (step.now !== undefined) clock = step.now
  if (step.disabled !== undefined) process.env.HAHA_MARKET_DISABLE_PROVIDERS = step.disabled
  if (step.control !== undefined) await fetch(input.base + '/__control', {method:'POST',body:JSON.stringify(step.control)})
  const url = new URL(step.path, 'http://fixture.invalid')
  const req = new Request(url, { method: step.method || 'GET', ...(step.body === undefined ? {} : { body: typeof step.body === 'string' ? step.body : JSON.stringify(step.body) }) })
  const response = await handleMarketApi(req, url, url.pathname.split('/').filter(Boolean))
  results.push({ status: response.status, body: await response.json() })
}
console.log('MARKET_SOURCE_RESULT:' + JSON.stringify(results))
