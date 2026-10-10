import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; scenarios: Array<{ name: string; endpoint: string; env: Record<string, string> }> }
assertDisposableFixtureHome(input.homeDir, 'Native proxy')
if (process.env.CC_HAHA_NATIVE_PROXY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native proxy requires isolated production profile')
const names = ['https_proxy', 'HTTPS_PROXY', 'http_proxy', 'HTTP_PROXY', 'no_proxy', 'NO_PROXY']
for (const scenario of input.scenarios) for (const [key, value] of Object.entries(scenario.env)) if (!names.includes(key) || typeof value !== 'string') throw new Error('Native proxy cannot override sandbox or transport capabilities')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
const { getProxyUrl, getNoProxy, shouldBypassProxy, getProxyFetchOptions } = await import('../../../src/utils/proxy')
const observations = input.scenarios.map(scenario => {
  for (const name of names) delete process.env[name]
  Object.assign(process.env, scenario.env)
  const proxy = getProxyUrl(), noProxy = getNoProxy()
  const fetchOptions = getProxyFetchOptions({ forAnthropicAPI: true, targetUrl: scenario.endpoint })
  return { scenario, observation: { proxy: proxy ?? '', bypass: !!proxy && shouldBypassProxy(scenario.endpoint, noProxy), fetchProxy: fetchOptions.proxy ?? '' } }
})
process.stdout.write(JSON.stringify({ observations, sourceOnly: { consumer: 'src/utils/proxy.ts getProxyFetchOptions', runtime: process.version, capabilities } }))
