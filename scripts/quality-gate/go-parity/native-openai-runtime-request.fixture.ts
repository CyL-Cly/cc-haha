import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { spyOn } from 'bun:test'
import Anthropic from '@anthropic-ai/sdk'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Native OpenAI runtime request')
if (process.env.CC_HAHA_NATIVE_OPENAI_RUNTIME_REQUEST_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Unowned runtime request fixture')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const path = join(process.env.CLAUDE_CONFIG_DIR!, 'private-openai.json')
writeFileSync(path, JSON.stringify(input.tokens), { mode: 0o600 })
process.env.OPENAI_CODEX_OAUTH_FILE = path
process.env.CC_HAHA_OPENAI_OAUTH_PROVIDER = '1'
process.env.CC_HAHA_OPENAI_REQUEST_COMPRESSION = 'off'
delete process.env.ANTHROPIC_BASE_URL
delete process.env.ANTHROPIC_API_KEY
if (input.proxy !== undefined) {
  const proxy = new URL(input.proxy)
  if (proxy.protocol !== 'http:' || proxy.hostname !== '127.0.0.1' || !proxy.port || proxy.username || proxy.password) throw new Error('Runtime proxy fixture is not loopback')
  process.env.HTTPS_PROXY = input.proxy
  process.env.NO_PROXY = input.noProxy
}
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.home)
state.setOriginalCwd(input.home)
state.setProjectRoot(input.home)
state.switchSession(input.session)
state.setIsInteractive(false)
state.setSdkBetas([])
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: input.device }))
const phases: string[] = []
const storage = await import('../../../src/services/openaiAuth/storage')
const auth = await import('../../../src/services/openaiAuth/index')
const get = storage.getOpenAIOAuthTokens
spyOn(storage, 'getOpenAIOAuthTokens').mockImplementation((...args: any[]) => { phases.push('cached_auth'); return (get as any)(...args) })
const fresh = auth.ensureFreshOpenAITokens
spyOn(auth, 'ensureFreshOpenAITokens').mockImplementation((...args: any[]) => { phases.push('fresh_tokens'); return (fresh as any)(...args) })
let originalBody: unknown, upstream: unknown
const physicalSDKFetch = (Anthropic.prototype as any).fetchWithTimeout
spyOn(Anthropic.prototype as any, 'fetchWithTimeout').mockImplementation(function (this: any, url: any, init: any, ...args: any[]) {
  if (new URL(String(url)).pathname.endsWith('/v1/messages')) {
    phases.push('sdk_fetch')
    originalBody = JSON.parse(String(init.body))
  }
  return physicalSDKFetch.call(this, url, init, ...args)
})
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
for await (const _ of queryModelWithStreaming({
  messages: input.history,
  systemPrompt: asSystemPrompt(input.system), thinkingConfig: { type: 'disabled' },
  tools: [], signal: new AbortController().signal,
  options: {
    model: input.model, querySource: 'sdk', isNonInteractiveSession: true,
    agents: [], mcpTools: [], hasAppendSystemPrompt: false,
    maxOutputTokensOverride: input.maxTokens,
    getToolPermissionContext: async () => getEmptyToolPermissionContext(),
    fetchOverride: async (url, init) => {
      if (String(url) !== 'https://chatgpt.com/backend-api/codex/responses' || init?.method !== 'POST') throw new Error('Runtime request escaped the exact fixture endpoint')
      if (upstream) throw new Error('Unexpected extra physical request')
      phases.push('upstream_fetch')
      upstream = { endpoint: String(url), body: JSON.parse(String(init.body)), headers: Object.fromEntries(new Headers(init.headers)), proxy: (init as any)?.proxy ?? '' }
      return new Response('event: response.completed\ndata: {"response":{"id":"private-response","status":"completed","output":[{"type":"message","id":"private-message","content":[{"type":"output_text","text":"private done"}]}],"usage":{"input_tokens":12,"output_tokens":3}}}\n\n', { headers: { 'content-type': 'text/event-stream', 'x-codex-turn-state': 'private-routing' } })
    },
  },
})) {}
if (!originalBody || !upstream || capabilities.blocked.length) throw new Error('Original consumer did not complete inside its fixture capabilities')
process.stdout.write(JSON.stringify({ originalBody, upstream, phases, capabilities }))
