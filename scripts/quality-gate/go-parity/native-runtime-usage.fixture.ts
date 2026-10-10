import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; scenarios: Array<{ name: string; history: unknown[] }> }
assertDisposableFixtureHome(input.homeDir, 'Native runtime usage')
if (process.env.CC_HAHA_NATIVE_RUNTIME_USAGE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native runtime usage requires the isolated production profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const { doesMostRecentAssistantMessageExceed200k } = await import('../../../src/utils/tokens')
const observations = input.scenarios.map(scenario => ({ scenario, observation: doesMostRecentAssistantMessageExceed200k(scenario.history as Parameters<typeof doesMostRecentAssistantMessageExceed200k>[0]) }))
process.stdout.write(JSON.stringify({ observations, sourceOnly: { consumer: 'src/utils/tokens.ts doesMostRecentAssistantMessageExceed200k', capabilities, runtime: process.version } }))
