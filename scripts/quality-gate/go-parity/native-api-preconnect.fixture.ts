import fs from 'node:fs'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual native API preconnect')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_API_PRECONNECT !== '1') throw new Error('Pinned preconnect fixture required')
const capabilities = await installClientFixtureCapabilities(home)
for (const [name, value] of Object.entries(input.env)) process.env[name] = String(value)
const calls: Array<{ url: string; method: string; signal: boolean }> = []
const timeouts: number[] = []
const originalTimeout = AbortSignal.timeout
spyOn(AbortSignal, 'timeout').mockImplementation(delay => {
  timeouts.push(delay)
  return originalTimeout.call(AbortSignal, delay)
})
// Replace only the external fetch primitive. The complete original module
// retains its private once state, config getter, skip logic and catch handler.
globalThis.fetch = ((url: any, options: any) => {
  calls.push({ url: String(url), method: options.method, signal: options.signal instanceof AbortSignal })
  return Promise.reject(new Error('private rejected external preconnect'))
}) as typeof fetch
const { preconnectAnthropicApi } = await import('../../../src/utils/apiPreconnect')
const returned = preconnectAnthropicApi()
for (const name of ['CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI', 'HTTPS_PROXY', 'https_proxy', 'HTTP_PROXY', 'http_proxy', 'ANTHROPIC_UNIX_SOCKET', 'CLAUDE_CODE_CLIENT_CERT', 'CLAUDE_CODE_CLIENT_KEY']) delete process.env[name]
process.env.ANTHROPIC_BASE_URL = 'http://127.0.0.1:2/changed-after-once'
preconnectAnthropicApi()
await Promise.resolve()
if (capabilities.blocked.length) throw new Error('Preconnect escaped private fixture: ' + JSON.stringify(capabilities.blocked))
fs.writeSync(1, JSON.stringify({ calls, timeouts, returnedVoid: returned === undefined, blocked: capabilities.blocked }))
process.exit(0)
