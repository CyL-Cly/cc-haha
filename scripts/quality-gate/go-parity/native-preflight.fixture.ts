import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; scenarios: Array<{ name: string; model: string; env?: Record<string, string>; history?: unknown[]; sdkBetas?: string[] }> }
assertDisposableFixtureHome(input.homeDir, 'Native preflight')
if (process.env.CC_HAHA_NATIVE_PREFLIGHT_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native preflight requires isolated production profile')
const allowed = new Set(['CLAUDE_CODE_MODEL_CONTEXT_WINDOWS', 'CLAUDE_CODE_MAX_OUTPUT_TOKENS', 'CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS', 'CLAUDE_CODE_AUTO_COMPACT_WINDOW', 'CLAUDE_AUTOCOMPACT_PCT_OVERRIDE', 'CLAUDE_CODE_DISABLE_1M_CONTEXT', ...['FABLE', 'OPUS', 'SONNET', 'HAIKU'].map(tier => `ANTHROPIC_DEFAULT_${tier}_MODEL`)])
for (const scenario of input.scenarios) for (const [key, value] of Object.entries(scenario.env ?? {})) if (!allowed.has(key) || typeof value !== 'string') throw new Error('Native preflight cannot override sandbox or provider origin')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
process.env.ANTHROPIC_BASE_URL = 'http://127.0.0.1:1'
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
state.setIsInteractive(false)
state.setSdkBetas([])
const { getEffectiveContextWindowSize, getAutoCompactThreshold } = await import('../../../src/services/compact/autoCompact')
const { stripSignatureBlocksAfterModelChange } = await import('../../../src/utils/messages')
const observations = []
for (const scenario of input.scenarios) {
  for (const name of allowed) delete process.env[name]
  Object.assign(process.env, scenario.env ?? {})
  state.setSdkBetas(scenario.sdkBetas ?? [])
  const history = scenario.history ?? []
  const projected = stripSignatureBlocksAfterModelChange(history as Parameters<typeof stripSignatureBlocksAfterModelChange>[0], scenario.model)
  observations.push({ scenario, observation: { effective: getEffectiveContextWindowSize(scenario.model), threshold: getAutoCompactThreshold(scenario.model), history: projected, sameInputArray: projected === history } })
}
process.stdout.write(JSON.stringify({ observations, sourceOnly: { consumer: 'src/services/compact/autoCompact.ts and src/utils/messages.ts', capabilities, runtime: process.version } }))
