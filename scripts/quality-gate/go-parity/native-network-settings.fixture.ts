import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native network settings')
if (process.env.CC_HAHA_NATIVE_NETWORK_SETTINGS_FIXTURE !== '1') throw new Error('Unowned network settings fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { normalizeNetworkSettings, buildNetworkEnvironment } = await import('../../../src/server/services/networkSettings')
const rows = input.rows.map((row: any) => {
  const settings = normalizeNetworkSettings(JSON.parse(row.settings))
  try { return { settings, environment: buildNetworkEnvironment(settings, row.environment) } }
  catch (e: any) { return { settings, error: e.message } }
})
process.stdout.write(JSON.stringify({ rows, capabilities }))
