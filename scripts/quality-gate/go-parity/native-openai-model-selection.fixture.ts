import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Native OpenAI model selection')
if (process.env.CC_HAHA_NATIVE_OPENAI_MODEL_SELECTION_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned OpenAI model fixture')
const allowed = new Set(['ANTHROPIC_BASE_URL', 'ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_FABLE_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'ANTHROPIC_SMALL_FAST_MODEL', 'OPENAI_CODEX_MODEL', 'OPENAI_CODEX_HAIKU_MODEL', 'OPENAI_CODEX_SONNET_MODEL', 'OPENAI_CODEX_OPUS_MODEL', 'CLAUDE_CODE_DISABLE_1M_CONTEXT', 'CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP', 'CLAUDE_CODE_SUBAGENT_MODEL'])
for (const row of input.cases) for (const [key, value] of Object.entries(row.env)) if (!allowed.has(key) || typeof value !== 'string') throw new Error('Unexpected model environment')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const path = join(process.env.CLAUDE_CONFIG_DIR!, 'private-openai.json')
writeFileSync(path, JSON.stringify({ accessToken: 'private-access', refreshToken: 'private-refresh', expiresAt: 1000000000000000 }), { mode: 0o600 })
process.env.OPENAI_CODEX_OAUTH_FILE = path
delete process.env.ANTHROPIC_API_KEY
delete process.env.ANTHROPIC_AUTH_TOKEN
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.home)
state.setOriginalCwd(input.home)
state.setProjectRoot(input.home)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const cache = await import('../../../src/utils/settings/settingsCache')
const model = await import('../../../src/utils/model/model')
const auth = await import('../../../src/utils/auth')
const agent = await import('../../../src/utils/model/agent')
const rows = []
for (const row of input.cases) {
  for (const key of allowed) delete process.env[key]
  Object.assign(process.env, row.env)
  writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'settings.json'), JSON.stringify(row.settings))
  cache.resetSettingsCache()
  state.resetModelStringsForTestingOnly()
  state.setMainLoopModelOverride(row.override)
  if (!auth.isOpenAIAuthActive()) throw new Error('Actual saved identity did not activate')
  rows.push({ apiKeySource: auth.getAnthropicApiKeyWithSource().source, selected: model.getMainLoopModel(), default: model.getDefaultMainLoopModelSetting(), smallFast: model.getSmallFastModel(), parsed: row.parse.map((value: string) => model.parseUserSpecifiedModel(value)), runtime: model.getRuntimeMainLoopModel({ permissionMode: 'plan', mainLoopModel: 'private-parent' }), agent: agent.getAgentModel('haiku', 'private-parent', undefined, 'default') })
}
if (capabilities.blocked.length) throw new Error('Original consumer left the isolated boundary')
process.stdout.write(JSON.stringify({ rows, capabilities, consumer: 'src/utils/model/model.ts + auth.ts + model/agent.ts' }))
