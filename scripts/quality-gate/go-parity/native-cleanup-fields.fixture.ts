import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; scenarios: Array<{ name: string; content: string }> }
assertDisposableFixtureHome(input.homeDir, 'Native cleanup settings')
if (process.env.CC_HAHA_NATIVE_CLEANUP_FIELDS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native cleanup fields require isolated production profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
const { SettingsSchema } = await import('../../../src/utils/settings/types')
const { formatZodError } = await import('../../../src/utils/settings/validation')
const observations = input.scenarios.map(scenario => {
  const parsed = SettingsSchema().safeParse(JSON.parse(scenario.content))
  return { scenario, observation: parsed.success ? { settingsSource: JSON.stringify(parsed.data), errorsSource: '[]' } : { settingsSource: null, errorsSource: JSON.stringify(formatZodError(parsed.error, 'fixture')) } }
})
process.stdout.write(JSON.stringify({ observations, sourceOnly: { consumer: 'src/utils/settings/types.ts actual SettingsSchema + formatZodError cleanupPeriodDays', runtime: process.version, capabilities } }))

