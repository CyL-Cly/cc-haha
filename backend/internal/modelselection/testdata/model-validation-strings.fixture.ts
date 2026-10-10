import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MODEL_FALLBACK_FIXTURE !== '1') throw new Error('Unowned model strings Source')
const home = assertDisposableFixtureHome(input.home, 'model validation fallback')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../../src/utils/config'); config.enableConfigs()
const cache = await import('../../../../src/utils/settings/settingsCache')
const { ALL_MODEL_CONFIGS } = await import('../../../../src/utils/model/configs')
const { getModelStrings } = await import('../../../../src/utils/model/modelStrings')
const rows = []
for (const record of input.rows) {
  for (const key of ['CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI']) delete process.env[key]
  Object.assign(process.env, record.env)
  fs.mkdirSync(join(home, '.claude'), { recursive: true }); fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(record.settings))
  cache.resetSettingsCache()
  state.setModelStrings(Object.fromEntries(Object.entries(ALL_MODEL_CONFIGS).map(([key, value]) => [key, value[record.provider]])))
  const actual = getModelStrings()
  rows.push([actual.opus41, actual.sonnet45, actual.sonnet40])
}
if (capabilities.blocked.length) throw new Error('Fallback Source capability blocked')
console.log(JSON.stringify({ rows }))
