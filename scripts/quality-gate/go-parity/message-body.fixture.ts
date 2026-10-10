import type { HeaderSpec, MessageBodyFixtureInput } from './message-body-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as MessageBodyFixtureInput
assertDisposableFixtureHome(input.homeDir, 'Message body')
if (process.env.CC_HAHA_MESSAGE_BODY_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Message body fixture requires isolated direct bare profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir)
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { signClaudeCodeCCHInString, signClaudeCodeCCHBody } = await import('../../../src/utils/claudeCodeCch')
const { getAnthropicClient } = await import('../../../src/services/api/client')
const { SDK_HELPER_SYMBOL } = await import('@anthropic-ai/sdk/helpers/beta/mcp')

function headers(spec: HeaderSpec | undefined): any {
  if (!spec) return undefined
  if (spec.kind === 'tuples') return spec.entries
  if (spec.kind === 'headers') {
    const result = new Headers()
    for (const [key, values] of spec.entries) for (const value of Array.isArray(values) ? values : [values]) result.append(key, String(value))
    return result
  }
  const result: Record<string, unknown> = {}
  for (const [key, value] of spec.entries) result[key] = value
  for (const key of spec.undefinedNames ?? []) result[key] = undefined
  return result
}

const results: unknown[] = []
const observations: unknown[] = []
for (const step of input.steps) {
  if (step.operation === 'sign') {
    if (step.bytes) {
      const source = Uint8Array.from(step.bytes)
      const signed = signClaudeCodeCCHBody(source) as Uint8Array
      results.push({ operation: 'sign', bytes: [...signed], identitySame: signed === source })
    } else results.push({ operation: 'sign', body: signClaudeCodeCCHInString(step.body) })
    continue
  }
  process.env.ANTHROPIC_BASE_URL = 'http://127.0.0.1:43210'
  if (step.customHeaders !== undefined) process.env.ANTHROPIC_CUSTOM_HEADERS = step.customHeaders
  else delete process.env.ANTHROPIC_CUSTOM_HEADERS
  if (step.authToken !== undefined) process.env.ANTHROPIC_AUTH_TOKEN = step.authToken
  else delete process.env.ANTHROPIC_AUTH_TOKEN
  const params = JSON.parse(step.paramsSource)
  for (const marker of step.helpers ?? []) {
    let value = params
    for (const part of marker.path) value = value[part]
    value[SDK_HELPER_SYMBOL] = marker.name
  }
  const before = JSON.stringify(params)
  let prepared: unknown = null
  let transport: unknown = null
  let signedBody: string | null = null
  let error: unknown = null
  try {
    const client = await getAnthropicClient({
      apiKey: 'offline-fixture-key', maxRetries: 0, model: 'fixture-model', source: 'sdk',
      fetchOverride: async (url, init) => {
        // Production buildFetch already signed this body. Never call a fixture
        // implementation of JSON projection, SDK headers, or CCH here.
        signedBody = typeof init?.body === 'string' ? init.body : new TextDecoder().decode(init?.body as Uint8Array)
        transport = { path: new URL(String(url)).pathname + new URL(String(url)).search, method: init?.method, headers: [...new Headers(init?.headers)] }
        return new Response('{"id":"fixture-response","type":"message","role":"assistant","content":[],"model":"fixture-model","stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0}}', { headers: { 'content-type': 'application/json' } })
      },
    })
    const post = client.post.bind(client)
    // Observe, then delegate to the real SDK. The symbol-bearing nullable
    // header object is produced by beta.messages itself, not this fixture.
    client.post = ((path: string, options: any) => {
      prepared = { path, body: JSON.stringify(options.body), headers: [...options.headers.values], removedHeaders: [...options.headers.nulls].sort() }
      return post(path, options)
    }) as typeof client.post
    const response = await client.beta.messages.create(params, { headers: headers(step.headers) }).withResponse()
    if ('controller' in response.data) response.data.controller.abort()
  } catch (failure) {
    error = { name: (failure as Error).name, message: (failure as Error).message }
  }
  results.push({ operation: 'prepare', before, after: JSON.stringify(params), prepared, signedBody, error })
  observations.push({ transport })
}
process.stdout.write(JSON.stringify({ results, ...(input.observeTransport ? { observations } : {}), capabilities }))
