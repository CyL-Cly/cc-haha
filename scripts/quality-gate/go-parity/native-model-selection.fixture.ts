import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import type { BetaUsage } from '@anthropic-ai/sdk/resources/beta/messages/messages.mjs'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as {
  homeDir: string
  cases: Array<{ name: string; env?: Record<string, string>; settings?: Record<string, unknown>; override?: string | null; parse?: string[]; allowed?: string[]; runtime?: Array<{ parent: string; mode: 'default' | 'plan'; exceeds200kTokens?: boolean }>; agents?: Array<{ agentModel?: string | null; parent: string; toolModel?: 'fable' | 'sonnet' | 'haiku' | 'opus'; mode?: 'default' | 'plan' }>; accounting?: Array<{ model: string; usage: BetaUsage }> }>
}
assertDisposableFixtureHome(input.homeDir, 'Native model selection')
if (process.env.CC_HAHA_NATIVE_MODEL_SELECTION_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Native model selection requires the isolated production profile')
const allowed = new Set(['ANTHROPIC_MODEL', 'ANTHROPIC_BASE_URL', 'ANTHROPIC_DEFAULT_FABLE_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'CLAUDE_CODE_SUBAGENT_MODEL', 'CLAUDE_CODE_DISABLE_1M_CONTEXT', 'CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI'])
for (const record of input.cases) for (const [key, value] of Object.entries(record.env ?? {})) if (!allowed.has(key) || typeof value !== 'string') throw new Error('Fixture attempted to replace an unrelated process input')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
state.setAllowedSettingSources(['userSettings'])
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const cache = await import('../../../src/utils/settings/settingsCache')
const model = await import('../../../src/utils/model/model')
const allowlist = await import('../../../src/utils/model/modelAllowlist')
const agentModel = await import('../../../src/utils/model/agent')
const modelCost = await import('../../../src/utils/modelCost')
const costTracker = await import('../../../src/cost-tracker')
const rows = []
for (const record of input.cases) {
  for (const key of allowed) delete process.env[key]
  process.env.ANTHROPIC_BASE_URL = 'http://127.0.0.1:43210'
  for (const [key, value] of Object.entries(record.env ?? {})) process.env[key] = value
  mkdirSync(join(input.homeDir, '.claude'), { recursive: true })
  writeFileSync(join(input.homeDir, '.claude/settings.json'), JSON.stringify(record.settings ?? {}))
  cache.resetSettingsCache()
  state.resetModelStringsForTestingOnly()
  state.setMainLoopModelOverride(record.override)
  state.resetCostState()
  rows.push({ scenario: record, observation: {
    selectedJSON: JSON.stringify(model.getMainLoopModel()),
    defaultJSON: JSON.stringify(model.getDefaultMainLoopModel()),
    specifiedJSON: JSON.stringify(model.getUserSpecifiedModelSetting()) ?? null,
    parsed: (record.parse ?? []).map(value => ({ input: value, valueJSON: JSON.stringify(model.parseUserSpecifiedModel(value)) })),
    allowed: (record.allowed ?? []).map(value => ({ input: value, value: modelIsAllowed(value) })),
    runtime: (record.runtime ?? []).map(value => ({ input: value, valueJSON: JSON.stringify(model.getRuntimeMainLoopModel({ permissionMode: value.mode, mainLoopModel: value.parent, exceeds200kTokens: value.exceeds200kTokens })) })),
    agents: (record.agents ?? []).map(value => ({ input: value, valueJSON: JSON.stringify(agentModel.getAgentModel(value.agentModel ?? undefined, value.parent, value.toolModel, value.mode)) })),
    accounting: (record.accounting ?? []).map(value => {
      const cost = modelCost.calculateUSDCost(value.model, value.usage)
      costTracker.addToTotalSessionCost(cost, value.usage, value.model)
      return { input: value, cost, total: state.getTotalCostUSD(), modelUsageJSON: JSON.stringify(state.getModelUsage()), unknown: state.hasUnknownModelCost() }
    }),
  } })
}
function modelIsAllowed(value: string) { return allowlist.isModelAllowed(value) }
process.stdout.write(JSON.stringify({ rows, sourceOnly: { consumer: 'src/utils/model/model.ts + modelAllowlist.ts', runtime: process.version, capabilities } }))
