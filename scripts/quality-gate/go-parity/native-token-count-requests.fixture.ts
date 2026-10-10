import { join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native token count requests')
if (process.env.CC_HAHA_TOKEN_REQUEST_FIXTURE !== '1') throw new Error('Unowned token count request fixture')
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: new URL(input.url).origin, absentPaths: absentManagedSettingsFixturePaths() })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings ?? {}))
await import('../../../preload')
for (const [key, value] of Object.entries(input.env)) process.env[key] = String(value)
process.env.ANTHROPIC_BASE_URL = input.url
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home)
state.setIsInteractive(false); state.switchSession(input.session)
state.setMainLoopModelOverride(input.model)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: input.device }))
const counter = await import('../../../src/services/tokenEstimation')
const { getModelBetas } = await import('../../../src/utils/betas')
const { getSmallFastModel } = await import('../../../src/utils/model/model')
const results = []
for (const row of input.rows) {
  results.push(await (row.fallback ? counter.countTokensViaHaikuFallback : counter.countMessagesTokensWithAPI)(row.messages, row.tools))
}
if (capabilities.blocked.length) throw new Error('Blocked token count request capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, betas: getModelBetas(input.model), smallModel: getSmallFastModel() }))
