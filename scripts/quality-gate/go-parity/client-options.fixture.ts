import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { clientResolverEnvironmentNames, directClientEnvironmentNames, type DirectClientInput } from './client-options-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as DirectClientInput & { probeCapability?: string; probePath?: string }
assertDisposableFixtureHome(input.homeDir, 'Client options')
if (process.env.CC_HAHA_CLIENT_OPTIONS_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE !== '1' || process.env.CC_HAHA_DISABLE_TERMINAL_SHELL_ENV !== '1') throw new Error('Client options fixture requires the isolated direct bare profile')
for (const step of input.steps) {
  if (step.headers && step.headers.kind !== 'object') throw new Error('Client options fixture supports only object request headers')
  for (const [name, value] of [...Object.entries(step.env), ...Object.entries(step.beforeFetchEnv ?? {})]) {
    if (!directClientEnvironmentNames.has(name) || typeof value !== 'string') throw new Error('Client options fixture cannot override sandbox/provider-selection environment')
  }
}
const capabilities = await installClientFixtureCapabilities(input.homeDir)
if (input.probeCapability) {
  if (input.probeCapability === 'subprocess') require('node:child_process').execFileSync('security', ['find-generic-password'])
  else if (input.probeCapability === 'native') Bun.spawn(['security'])
  else if (input.probeCapability === 'network') await fetch('https://fixture.invalid')
  else if (input.probeCapability === 'filesystem') require('node:fs').readFileSync(input.probePath ?? '/outside-client-fixture')
  else if (input.probeCapability === 'filesystem-write') require('node:fs').writeFileSync(input.probePath, 'escaped')
  else throw new Error('Unknown client options capability probe')
}
for (const resolver of input.resolvers ?? []) {
  for (const [name, value] of Object.entries(resolver.env)) {
    if (!clientResolverEnvironmentNames.has(name) || typeof value !== 'string') throw new Error('Client options fixture cannot override sandbox/provider-selection environment')
  }
}
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { getAnthropicClient, resolveManagedProviderProxyAccessToken } = await import('../../../src/services/api/client')
const { runWithWorkload } = await import('../../../src/utils/workloadContext')
const { getAPIProvider, isFirstPartyAnthropicBaseUrl } = await import('../../../src/utils/model/providers')
const names = new Set([...input.steps.flatMap(step => [...Object.keys(step.env), ...Object.keys(step.beforeFetchEnv ?? {})]), ...(input.resolvers ?? []).flatMap(resolver => Object.keys(resolver.env))])
const results: unknown[] = []
let uuidCalls = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `request-fixture-${++uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
for (const step of input.steps) {
  uuidCalls = 0
  for (const name of names) delete process.env[name]
  for (const [name, value] of Object.entries(step.env)) process.env[name] = value
  let fetch: unknown = null
  let error: unknown = null
  const client = await runWithWorkload(step.workload, () => getAnthropicClient({
    apiKey: step.explicitApiKey, model: step.model ?? 'fixture-model', source: 'sdk', maxRetries: 0,
    fetchOverride: async (url, init) => {
      fetch = { url: String(url), method: init?.method, headers: [...new Headers(init?.headers)], body: init?.body }
      return new Response('data: {"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}\n\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}\n\ndata: {"type":"message_stop"}\n\n', { headers: { 'content-type': 'text/event-stream' } })
    },
  }))
  for (const [name, value] of Object.entries(step.beforeFetchEnv ?? {})) process.env[name] = value
  if (step.observeFetch !== false) {
    const requestHeaders: Record<string, unknown> = {}
    for (const [name, value] of step.headers?.entries ?? []) requestHeaders[name] = value
    for (const name of step.headers?.undefinedNames ?? []) requestHeaders[name] = undefined
    try {
      const stream = await client.beta.messages.create({ model: step.model ?? 'fixture-model', messages: [], max_tokens: 32, stream: true }, { headers: requestHeaders as any })
      for await (const _ of stream) { /* consume the actual SDK */ }
    } catch (failure) { error = { name: (failure as Error).name, message: (failure as Error).message } }
  }
  results.push({ name: step.name, apiKey: client.apiKey, authToken: client.authToken, baseURL: client.baseURL, timeout: client.timeout, defaultHeaders: (client as any)._options.defaultHeaders, fetch, error, ...(step.observeRequestIDs ? { uuidCalls } : {}) })
}
const resolverResults = (input.resolvers ?? []).map(resolver => {
  for (const name of names) delete process.env[name]
  for (const [name, value] of Object.entries(resolver.env)) process.env[name] = value
  return { name: resolver.name, managedToken: resolveManagedProviderProxyAccessToken({ requestUrl: resolver.requestURL }), injectRequestID: getAPIProvider() === 'firstParty' && isFirstPartyAnthropicBaseUrl() }
})
// Source-only evidence: Go receives already selected auth inputs. These reads
// are never added to the cross-language observations or represented as Go auth.
process.stdout.write(JSON.stringify({ results, resolverResults, capabilities, profile: 'direct-bare' }))
