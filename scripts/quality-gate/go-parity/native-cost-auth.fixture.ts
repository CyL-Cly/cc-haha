import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; cases: Array<{ name: string; environment: Record<string, string>; supported: boolean }> }
assertDisposableFixtureHome(input.homeDir, 'Native cost auth')
if (process.env.CC_HAHA_NATIVE_COST_AUTH_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native cost auth requires isolated production profile')
const keys = new Set(['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_REMOTE', 'CLAUDE_CODE_ENTRYPOINT', 'ANTHROPIC_UNIX_SOCKET', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR', 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI', 'USER_TYPE', 'COO_RUNNING_ON_HOMESPACE'])
for (const record of input.cases) for (const [name, value] of Object.entries(record.environment)) if (!keys.has(name) || typeof value !== 'string') throw new Error('Unrelated auth fixture environment input')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] })
// Inject only an empty external legacy credential-store observation. The real
// auth and cost consumers remain untouched, and no security subprocess runs.
mock.module(new URL('../../../src/utils/secureStorage/keychainPrefetch.ts', import.meta.url).pathname, () => ({
  getLegacyApiKeyPrefetchResult: () => ({ stdout: null }),
  clearLegacyApiKeyPrefetch: () => {},
  ensureKeychainPrefetchCompleted: async () => {},
  startKeychainPrefetch: () => {},
}))
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
mkdirSync(join(input.homeDir, '.git'), { recursive: true })
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const auth = await import('../../../src/utils/auth')
const cost = await import('../../../src/commands/cost/cost')
const observations = []
for (const record of input.cases) {
  for (const key of keys) delete process.env[key]
  Object.assign(process.env, record.environment)
  auth.getClaudeAIOAuthTokens.cache.clear()
  const enabled = auth.isAnthropicAuthEnabled()
  const subscriber = auth.isClaudeAISubscriber()
  const result = await cost.call('', {} as any)
  observations.push({ scenario: record, enabled, subscriber, resultType: result.type, subscriptionMessage: result.type === 'text' && result.value.includes('using your subscription'), formatsCost: result.type === 'text' && result.value.includes('Total cost:') })
}
process.stdout.write(JSON.stringify({ observations, sourceOnly: { consumer: 'actual src/utils/auth isAnthropicAuthEnabled/isClaudeAISubscriber + commands/cost call', runtime: process.version, capabilities } }))
