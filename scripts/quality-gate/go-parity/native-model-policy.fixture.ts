import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { requestContextEnvironmentNames, requestContextWire, type RequestContextCase } from './request-context-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as RequestContextCase & { homeDir: string }
assertDisposableFixtureHome(input.homeDir, 'Native model policy')
if (process.env.CC_HAHA_NATIVE_MODEL_POLICY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Native model policy fixture requires isolated production profile')
const names = new Set(requestContextEnvironmentNames)
for (const tier of ['FABLE', 'OPUS', 'SONNET', 'HAIKU']) {
  names.add(`ANTHROPIC_DEFAULT_${tier}_MODEL`)
  names.add(`ANTHROPIC_DEFAULT_${tier}_MODEL_SUPPORTED_CAPABILITIES`)
}
for (const [name, value] of Object.entries(input.env ?? {})) if (!names.has(name) || typeof value !== 'string') throw new Error('Native model policy fixture cannot override sandbox, credentials or provider origin')
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() {
  return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'response-request', connection: 'keep-alive' } })
} })
const capabilities = await installClientFixtureCapabilities(input.homeDir, { loopbackFetchOrigin: server.url.origin, absentPaths: absentManagedSettingsFixturePaths() })
try {
  for (const [name, value] of Object.entries(input.env ?? {})) process.env[name] = value
  process.env.ANTHROPIC_BASE_URL = server.url.origin + (input.proxy ? '/proxy/providers/fixture' : '')
  ;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
  const state = await import('../../../src/bootstrap/state')
  state.setCwdState(input.homeDir)
  state.setOriginalCwd(input.homeDir)
  state.setProjectRoot(input.homeDir)
  state.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof state.switchSession>[0])
  state.setIsInteractive(false)
  state.setSdkBetas([])
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
  const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
  const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
  const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
  const observations: unknown[] = []
  for await (const _ of queryModelWithStreaming({
    messages: input.historySources.map(source => JSON.parse(source)),
    systemPrompt: asSystemPrompt([]), thinkingConfig: (input.thinking ?? { type: 'disabled' }) as any,
    tools: [], signal: new AbortController().signal,
    options: {
      model: 'fixture-model', querySource: 'sdk', isNonInteractiveSession: true, agents: [], mcpTools: [], hasAppendSystemPrompt: false,
      ...input.options, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
      fetchOverride: async (url, init) => {
        const parsed = new URL(String(url))
        if (parsed.origin !== server.url.origin || init?.method !== 'POST' || typeof init.body !== 'string') throw new Error('Native model policy escaped loopback POST')
        const body = JSON.parse(init.body)
        const policy: Record<string, unknown> = {}
        for (const key of ['model', 'max_tokens', 'thinking', 'temperature', 'output_config']) if (Object.hasOwn(body, key)) policy[key] = body[key]
        const beta = new Headers(init.headers).get('anthropic-beta')?.split(',') ?? []
        observations.push({ policy, effortBeta: beta.includes('effort-2025-11-24'), bindingBeta: beta.includes('thinking-binding-controls-2026-08-01') })
        return fetch(url, init)
      },
    },
  })) {}
  if (observations.length !== 1) throw new Error(`Native model policy expected one real Source request, got ${observations.length}`)
  process.stdout.write(JSON.stringify({ observation: observations[0], sourceOnly: { capabilities, consumer: 'src/services/api/claude.ts queryModelWithStreaming', transport: 'guarded loopback', runtime: process.version } }))
} finally {
  server.stop(true)
}
