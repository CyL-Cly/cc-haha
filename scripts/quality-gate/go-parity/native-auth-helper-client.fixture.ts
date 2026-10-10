import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Native auth helper client')
if (process.env.CC_HAHA_AUTH_HELPER_CLIENT_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Unowned bare helper client fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalExeca = await import('execa')
let current: any, executions = 0, unexpectedCommands = 0
mock.module('execa', () => ({ ...originalExeca, execa: async (command: string) => {
  if (command !== 'private-credential-helper') { unexpectedCommands++; throw new Error('Refused unowned helper command') }
  executions++
  return { failed: current.fail === true, timedOut: false, exitCode: current.fail ? 7 : 0, stdout: current.fail ? '' : '  helper-value\n', stderr: current.fail ? ' helper failed\n' : '' }
} }))
await installClientFixtureCapabilities(process.env.HOME!)
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(process.env.HOME!)
bootstrap.setCwdState(process.env.HOME!)
bootstrap.setProjectRoot(process.env.HOME!)
bootstrap.setIsInteractive(false)
bootstrap.setFlagSettingsInline({ apiKeyHelper: 'private-credential-helper', awsCredentialExport: 'unowned-cloud-helper', awsAuthRefresh: 'unowned-cloud-helper', gcpAuthRefresh: 'unowned-cloud-helper' })
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { clearApiKeyHelperCache } = await import('../../../src/utils/auth')
const { getAnthropicClient } = await import('../../../src/services/api/client')
const result = []
for (const row of input.rows) {
  current = row
  executions = 0
  clearApiKeyHelperCache()
  for (const key of ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_CUSTOM_HEADERS']) delete process.env[key]
  Object.assign(process.env, row.env)
  let auth: any
  const client = await getAnthropicClient({ model: 'fixture-model', maxRetries: 0, source: 'sdk', fetchOverride: async (_, init) => {
    const headers = new Headers(init?.headers)
    auth = { key: headers.get('x-api-key'), token: headers.get('authorization') }
    return new Response('event: message_start\ndata: {"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n', { headers: { 'Content-Type': 'text/event-stream' } })
  } })
  const stream = await client.beta.messages.create({ model: 'fixture-model', max_tokens: 32, stream: true, messages: [{ role: 'user', content: 'fixture' }] })
  for await (const _ of stream) { /* original SDK */ }
  result.push({ ...auth, executions })
}
process.stdout.write(JSON.stringify(result))
if (unexpectedCommands !== 0) throw new Error('Direct provider executed dormant cloud commands')
