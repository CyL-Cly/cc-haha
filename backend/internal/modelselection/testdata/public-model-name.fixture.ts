import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PUBLIC_MODEL_NAME_FIXTURE !== '1') throw new Error('actual Source public model display fixture only')
const home = assertDisposableFixtureHome(input.home, 'public model display')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', BUILD_TIME: '' } })
const state = await import('../../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../../src/utils/config')
config.enableConfigs()
const cache = await import('../../../../src/utils/settings/settingsCache')
const { ALL_MODEL_CONFIGS } = await import('../../../../src/utils/model/configs')
const { getPublicModelDisplayName } = await import('../../../../src/utils/model/model')
const flags = ['CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI']
const rows = []
for (const record of input.rows) {
  for (const key of flags) delete process.env[key]
  Object.assign(process.env, record.env)
  fs.mkdirSync(join(home, '.claude'), { recursive: true })
  fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(record.settings))
  cache.resetSettingsCache()
  // Only the preexisting model-ID snapshot is the external boundary here.
  // Execute the original display consumer and original override application;
  // no Bedrock profile discovery or model/API request is performed.
  state.setModelStrings(Object.fromEntries(Object.entries(ALL_MODEL_CONFIGS).map(([key, value]) => [key, value[record.provider]])))
  const ids = Object.values(ALL_MODEL_CONFIGS).map(value => value[record.provider])
  const values = [...ids.flatMap(id => [id, id + '[1m]', id + '[1M]', id + '-unknown-suffix', 'prefix-' + id]), ...Object.values(record.settings.modelOverrides ?? {}).flatMap(id => [id, id + '[1m]']), ...input.values]
  rows.push({ values, labels: values.map(getPublicModelDisplayName) })
}
process.stdout.write(JSON.stringify({ rows, capabilities, consumer: 'src/utils/model/model.ts:getPublicModelDisplayName + modelStrings.ts:getModelStrings' }))
