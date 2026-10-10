import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Native count tokens SDK')
if (process.env.CC_HAHA_COUNT_SDK_FIXTURE !== '1') throw new Error('Unowned count SDK fixture')
const capabilities = await installClientFixtureCapabilities(input.home, { loopbackFetchOrigin: new URL(input.url).origin })
const { default: Anthropic } = await import('@anthropic-ai/sdk')
const client = new Anthropic({ apiKey: 'offline-count-key', baseURL: input.url, maxRetries: input.retries ?? 0, defaultHeaders: input.headers ?? { 'anthropic-beta': 'original-client-default' } })
const results = []
for (const params of input.params) {
  try {
    results.push(await (input.fallback ? client.beta.messages.create(params) : client.beta.messages.countTokens(params)))
  } catch (error) {
    if (!input.catchErrors) throw error
    results.push({ error: true, status: (error as any).status })
  }
}
if (capabilities.blocked.length) throw new Error('Blocked counting capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify(results))
