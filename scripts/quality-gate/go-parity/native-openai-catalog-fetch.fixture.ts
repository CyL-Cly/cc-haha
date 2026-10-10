import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI catalog fetch')
if (process.env.CC_HAHA_OPENAI_CATALOG_FETCH_FIXTURE !== '1') throw new Error('Unowned catalog fetch fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const url = new URL(input.url)
if (!['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) throw new Error('Catalog fetch fixture must be loopback')
for (const name of ['HTTP_PROXY', 'HTTPS_PROXY', 'http_proxy', 'https_proxy']) {
  if (process.env[name] && !['127.0.0.1', 'localhost', '[::1]'].includes(new URL(process.env[name]!).hostname)) throw new Error('Unowned fixture proxy')
}
const { fetchOpenAICodexModelCatalog } = await import('../../../src/services/openaiAuth/modelCatalog')
try {
  const models = await fetchOpenAICodexModelCatalog(((_original, options) => fetch(url, options)) as typeof fetch, { accessToken: 'fake-owned-access', accountId: 'owned-account' })
  process.stdout.write(JSON.stringify({ models }))
} catch (error) {
  process.stdout.write(JSON.stringify({ error: (error as Error).message }))
}
