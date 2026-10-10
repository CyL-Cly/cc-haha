import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Native helper schema')
if (process.env.CC_HAHA_AUTH_HELPER_SCHEMA_FIXTURE !== '1') throw new Error('Unowned helper schema fixture')
await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { SettingsSchema } = await import('../../../src/utils/settings/types')
const { formatZodError } = await import('../../../src/utils/settings/validation')
const { toSDKMessages } = await import('../../../src/utils/messages/mappers')
const schemas = input.schemas.map((row: any) => {
  const parsed = SettingsSchema().safeParse(row)
  return parsed.success ? { settings: parsed.data, errors: [] } : { settings: null, errors: formatZodError(parsed.error, 'fixture') }
})
process.stdout.write(JSON.stringify({ schemas, notices: input.notices.map((message: any) => toSDKMessages([message])) }))
