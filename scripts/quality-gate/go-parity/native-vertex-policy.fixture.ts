import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native Vertex beta policy')
if (process.env.CC_HAHA_VERTEX_POLICY_FIXTURE !== '1') throw new Error('Unowned Vertex policy fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: 'http://127.0.0.1:1', absentPaths: absentManagedSettingsFixturePaths() })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings ?? {}))
process.env.CLAUDE_CODE_USE_VERTEX = '1'
process.env.ANTHROPIC_API_KEY = 'private-cloud-policy-fake-key'
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const betas = await import('../../../src/utils/betas')
const models = await import('../../../src/utils/model/model')
const envUtils = await import('../../../src/utils/envUtils')
const rows = []
for (const row of input.rows) {
  for (const key of Object.keys(process.env)) if (key.startsWith('VERTEX_REGION_') || key === 'CLOUD_ML_REGION' || key.startsWith('ANTHROPIC_DEFAULT_') || ['ANTHROPIC_BETAS','CLAUDE_CODE_DISABLE_1M_CONTEXT','CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS','DISABLE_INTERLEAVED_THINKING'].includes(key)) delete process.env[key]
  Object.assign(process.env, row.env)
  state.setSdkBetas(row.sdk ?? [])
  betas.clearBetasCaches()
  const overrides = await import('../../../src/utils/model/modelSupportOverrides')
  overrides.get3PModelCapabilityOverride.cache?.clear?.()
  rows.push({ canonical: models.getCanonicalName(row.model), headers: betas.getMergedBetas(row.model, { isAgenticQuery: !!row.agentic }), region: envUtils.getVertexRegionForModel(row.model) })
}
if (capabilities.blocked.length) throw new Error('Blocked cloud policy primitive: ' + capabilities.blocked.join(', '))
console.log(JSON.stringify({ rows }))
